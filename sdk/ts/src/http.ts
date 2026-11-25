import { sha256, stringToBytes } from 'viem';
import { buildAuthHeaders, type Signer } from './signer.js';
import { mapHttpError, SandboxSDKError } from './errors.js';

export type FetchLike = (url: string, init?: RequestInit) => Promise<Response>;

export interface SignedRequestOptions {
  /** @deprecated Ignored — the signed action is derived from method, path and query. */
  action?: string;
  resourceId?: string;
  /** Value embedded in the signed message (NOT the HTTP body). Defaults to {}. */
  payload?: unknown;
  /** JSON HTTP body. */
  body?: unknown;
  query?: Record<string, string>;
  /** Per-request timeout override (ms). Defaults to the client's timeout. */
  timeoutMs?: number;
}

const DEFAULT_TIMEOUT_MS = 60_000;

export class HttpClient {
  private readonly timeoutMs: number;

  constructor(
    private readonly baseUrl: string,
    private readonly signer: Signer,
    private readonly fetchFn: FetchLike = (...args) => globalThis.fetch(...args),
    timeoutMs = DEFAULT_TIMEOUT_MS,
  ) {
    this.timeoutMs = timeoutMs;
  }

  /**
   * Wallet-signed request. The signature is bound to this exact request —
   * `METHOD:path?query` and the SHA-256 of the body bytes — so the server
   * rejects it for any other operation, path or body.
   */
  async signed<T = unknown>(method: string, path: string, opts: SignedRequestOptions = {}): Promise<T> {
    const provider = await this.resolveProvider();
    const m = method.toUpperCase();
    // One target string for both the signature and the URL: the server
    // compares the signed query verbatim.
    const target = path + queryString(opts.query);
    const body = opts.body !== undefined ? JSON.stringify(opts.body) : undefined;
    const headers: Record<string, string> = {
      ...(await buildAuthHeaders(this.signer, `${m}:${target}`, opts.resourceId ?? '', opts.payload ?? {}, {
        provider,
        bodySha256: sha256(stringToBytes(body ?? '')),
      })),
    };
    if (body !== undefined) headers['Content-Type'] = 'application/json';
    return this.do<T>(m, target, { headers, body, timeoutMs: opts.timeoutMs });
  }

  /**
   * Destination provider address for signature binding, resolved once from
   * GET /api/info and cached. Returns '' if the lookup fails — the server
   * accepts unbound messages while AUTH_STRICT is off, so a flaky info
   * endpoint degrades to legacy behavior instead of breaking every call.
   */
  private providerAddr: string | undefined;
  private async resolveProvider(): Promise<string> {
    if (this.providerAddr !== undefined) return this.providerAddr;
    try {
      const info = await this.public<{ provider_address?: string }>('GET', '/api/info');
      this.providerAddr = info.provider_address ?? '';
    } catch {
      this.providerAddr = '';
    }
    return this.providerAddr;
  }

  /** Unauthenticated request (public endpoints). */
  async public<T = unknown>(method: string, path: string, query?: Record<string, string>): Promise<T> {
    return this.do<T>(method, path, { query });
  }

  private async do<T>(
    method: string,
    path: string,
    opts: { headers?: Record<string, string>; body?: string; query?: Record<string, string>; timeoutMs?: number },
  ): Promise<T> {
    const url = this.baseUrl.replace(/\/+$/, '') + path + queryString(opts.query);
    // fetch has no default timeout — a hung server/RPC would hang forever.
    const timeout = opts.timeoutMs ?? this.timeoutMs;
    const signal = timeout > 0 ? AbortSignal.timeout(timeout) : undefined;
    let res: Response;
    try {
      res = await this.fetchFn(url, { method, headers: opts.headers, body: opts.body, signal });
    } catch (err) {
      const e = err as Error;
      const msg = e.name === 'TimeoutError' || e.name === 'AbortError' ? `request timed out after ${timeout}ms` : `request failed: ${e.message}`;
      throw new SandboxSDKError('API_ERROR', msg, undefined, err);
    }
    const text = await res.text();
    if (!res.ok) throw mapHttpError(res.status, text);
    if (!text) return undefined as T;
    try {
      return JSON.parse(text) as T;
    } catch {
      return text as unknown as T;
    }
  }
}

function queryString(query?: Record<string, string>): string {
  return query && Object.keys(query).length > 0 ? '?' + new URLSearchParams(query).toString() : '';
}
