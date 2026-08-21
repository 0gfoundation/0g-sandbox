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
}

export class HttpClient {
  constructor(
    private readonly baseUrl: string,
    private readonly signer: Signer,
    private readonly fetchFn: FetchLike = (...args) => globalThis.fetch(...args),
  ) {}

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
    return this.do<T>(method, path, { headers, body, query: opts.query });
  }

  /** Unauthenticated request (public endpoints). */
  async public<T = unknown>(method: string, path: string, query?: Record<string, string>): Promise<T> {
    return this.do<T>(method, path, { query });
  }

  private async do<T>(
    method: string,
    path: string,
    opts: { headers?: Record<string, string>; body?: string; query?: Record<string, string> },
  ): Promise<T> {
    let url = this.baseUrl.replace(/\/+$/, '') + path;
    if (opts.query && Object.keys(opts.query).length > 0) {
      url += '?' + new URLSearchParams(opts.query).toString();
    }
    let res: Response;
    try {
      res = await this.fetchFn(url, { method, headers: opts.headers, body: opts.body });
    } catch (err) {
      throw new SandboxSDKError('API_ERROR', `request failed: ${(err as Error).message}`, undefined, err);
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
