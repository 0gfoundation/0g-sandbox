import { buildAuthHeaders, type Signer } from './signer.js';
import { mapHttpError, SandboxSDKError } from './errors.js';

export type FetchLike = (url: string, init?: RequestInit) => Promise<Response>;

export interface SignedRequestOptions {
  action: string;
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

  /** Wallet-signed request. */
  async signed<T = unknown>(method: string, path: string, opts: SignedRequestOptions): Promise<T> {
    const headers: Record<string, string> = {
      ...(await buildAuthHeaders(this.signer, opts.action, opts.resourceId ?? '', opts.payload ?? {})),
    };
    let body: string | undefined;
    if (opts.body !== undefined) {
      headers['Content-Type'] = 'application/json';
      body = JSON.stringify(opts.body);
    }
    return this.do<T>(method, path, { headers, body, query: opts.query, timeoutMs: opts.timeoutMs });
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
    let url = this.baseUrl.replace(/\/+$/, '') + path;
    if (opts.query && Object.keys(opts.query).length > 0) {
      url += '?' + new URLSearchParams(opts.query).toString();
    }
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
