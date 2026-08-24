import type { HttpClient } from './http.js';
import { SandboxSDKError } from './errors.js';

export interface CreateOptions {
  name?: string;
  /** Snapshot name — locks cpu/memory/disk to the snapshot's spec (server rule). */
  snapshot?: string;
  class?: 'small' | 'medium' | 'large';
  cpu?: number;
  memory?: number;
  disk?: number;
  env?: Record<string, string>;
  /** Sealed sandbox: TEE attestation injected, SSH/toolbox blocked. Requires publicPorts to include 8080. */
  sealed?: boolean;
  /** Caller-chosen seal_id (64 hex chars); random if unset. */
  sealId?: string;
  /** Only these ports are publicly reachable (≤16, no system ports). Omit = all ports public. */
  publicPorts?: number[];
}

/** Loosely-typed sandbox record — Daytona fields pass through unmodified. */
export interface SandboxInfo {
  id: string;
  state?: string;
  name?: string;
  cpu?: number;
  memory?: number;
  disk?: number;
  labels?: Record<string, string>;
  preview_urls?: Record<string, string>;
  seal_id?: string;
  [key: string]: unknown;
}

export interface ExecResult {
  exitCode: number;
  output: string;
}

export interface ExecOptions {
  timeoutSec?: number;
  cwd?: string;
}

/**
 * Fallback for previewUrl(): the provider's PROXY_DOMAIN. Only needed when
 * the create response carries no preview_urls (i.e. no publicPorts was set —
 * the server attaches URLs only for restricted-port creates).
 */
export interface PreviewConfig {
  /** e.g. 'provider-private-sandbox.0g.ai' or '1.2.3.4.nip.io:4000' */
  domain: string;
  protocol?: 'http' | 'https';
}

export class SandboxApi {
  constructor(
    private readonly httpClient: HttpClient,
    private readonly preview?: PreviewConfig,
  ) {}

  async create(opts: CreateOptions = {}): Promise<Sandbox> {
    const body: Record<string, unknown> = {};
    if (opts.name) body.name = opts.name;
    if (opts.snapshot) body.snapshot = opts.snapshot;
    if (opts.class) body.class = opts.class;
    if (opts.cpu) body.cpu = opts.cpu;
    if (opts.memory) body.memory = opts.memory;
    if (opts.disk) body.disk = opts.disk;
    if (opts.env && Object.keys(opts.env).length > 0) body.env = opts.env;
    if (opts.sealed) body.sealed = true;
    if (opts.sealId) body.seal_id = opts.sealId;
    if (opts.publicPorts?.length) body.publicPorts = opts.publicPorts;

    const info = await this.httpClient.signed<SandboxInfo>('POST', '/api/sandbox', {
      action: 'create',
      payload: body,
      body,
    });
    return new Sandbox(this.httpClient, info, this.preview);
  }

  async list(): Promise<SandboxInfo[]> {
    return this.httpClient.signed<SandboxInfo[]>('GET', '/api/sandbox', { action: 'list' });
  }

  async get(id: string): Promise<Sandbox> {
    const info = await this.httpClient.signed<SandboxInfo>('GET', `/api/sandbox/${id}`, {
      action: 'get',
      resourceId: id,
    });
    return new Sandbox(this.httpClient, info, this.preview);
  }
}

export class Sandbox {
  constructor(
    private readonly httpClient: HttpClient,
    public info: SandboxInfo,
    private readonly preview?: PreviewConfig,
  ) {}

  get id(): string {
    return this.info.id;
  }

  get previewUrls(): Record<string, string> {
    return this.info.preview_urls ?? {};
  }

  /**
   * Preview URL for a port: http(s)://<port>-<id>.<proxyDomain>. Uses the
   * create-response preview_urls when present, otherwise derives the domain
   * pattern from any known entry.
   */
  previewUrl(port: number): string {
    const exact = this.previewUrls[String(port)];
    if (exact) return exact;
    const any = Object.values(this.previewUrls)[0];
    if (any) {
      const m = any.match(/^(https?:\/\/)(\d+)-(.+)$/);
      if (m) return `${m[1]}${port}-${m[3]}`;
    }
    if (this.preview) {
      return `${this.preview.protocol ?? 'http'}://${port}-${this.id}.${this.preview.domain}`;
    }
    throw new SandboxSDKError(
      'API_ERROR',
      'preview URL unavailable: the server only returns preview_urls for publicPorts creates — ' +
        'pass publicPorts on create, or set SDKConfig.preview = { domain } to construct URLs locally',
    );
  }

  /** Run a shell command via the Daytona toolbox. Rejects SEALED_FORBIDDEN on sealed sandboxes. */
  async exec(cmd: string, opts: ExecOptions = {}): Promise<ExecResult> {
    const body: Record<string, unknown> = { command: cmd, timeout: opts.timeoutSec ?? 30 };
    if (opts.cwd) body.cwd = opts.cwd;
    const res = await this.toolbox<{ exitCode: number; result: string }>('POST', '/process/execute', body);
    return { exitCode: res.exitCode, output: res.result };
  }

  /** Raw Daytona toolbox escape hatch: path is relative, e.g. '/files', '/git/status'. */
  async toolbox<T = unknown>(method: string, path: string, body?: unknown): Promise<T> {
    const rel = path.startsWith('/') ? path : '/' + path;
    return this.httpClient.signed<T>(method, `/api/toolbox/${this.id}/toolbox${rel}`, {
      action: 'toolbox',
      resourceId: this.id,
      body,
    });
  }

  async start(): Promise<void> {
    await this.lifecycle('POST', `/api/sandbox/${this.id}/start`, 'start');
  }

  /** Stop the sandbox. Filesystem is preserved (auto-backup); billing session closes. */
  async stop(): Promise<void> {
    await this.lifecycle('POST', `/api/sandbox/${this.id}/stop`, 'stop');
  }

  async delete(): Promise<void> {
    await this.lifecycle('DELETE', `/api/sandbox/${this.id}`, 'delete');
  }

  async archive(): Promise<void> {
    await this.lifecycle('POST', `/api/sandbox/${this.id}/archive`, 'archive');
  }

  /**
   * Lifecycle ops retry on 409 ("state change in progress") — Daytona rejects
   * a transition while the previous one is still settling, e.g. delete right
   * after stop.
   */
  private async lifecycle(method: string, path: string, action: string): Promise<void> {
    const deadline = Date.now() + 60_000;
    for (;;) {
      try {
        await this.httpClient.signed(method, path, { action, resourceId: this.id });
        return;
      } catch (err) {
        if (err instanceof SandboxSDKError && err.httpStatus === 409 && Date.now() < deadline) {
          await new Promise((r) => setTimeout(r, 2_000));
          continue;
        }
        throw err;
      }
    }
  }

  /** SSH access token. Rejects SEALED_FORBIDDEN on sealed sandboxes. */
  async sshAccess(): Promise<Record<string, unknown>> {
    return this.httpClient.signed('POST', `/api/sandbox/${this.id}/ssh-access`, {
      action: 'ssh-access',
      resourceId: this.id,
    });
  }

  async refresh(): Promise<SandboxInfo> {
    this.info = await this.httpClient.signed<SandboxInfo>('GET', `/api/sandbox/${this.id}`, {
      action: 'get',
      resourceId: this.id,
    });
    return this.info;
  }
}
