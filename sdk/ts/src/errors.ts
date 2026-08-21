export type ErrorCode =
  | 'INSUFFICIENT_BALANCE'
  | 'NOT_ACKNOWLEDGED'
  | 'SEALED_ONLY'
  | 'SEALED_FORBIDDEN'
  | 'QUOTA_EXCEEDED'
  | 'PUBLIC_PORTS_UNSUPPORTED'
  | 'UNAUTHORIZED'
  | 'FORBIDDEN'
  | 'NOT_FOUND'
  | 'TRUST_MISMATCH'
  | 'SIGNER_NO_TX'
  | 'API_ERROR'
  | 'CHAIN_ERROR';

export class SandboxSDKError extends Error {
  readonly code: ErrorCode;
  readonly httpStatus?: number;
  /** Raw server response body (parsed JSON when possible). */
  readonly details?: unknown;

  constructor(code: ErrorCode, message: string, httpStatus?: number, details?: unknown) {
    super(message);
    this.name = 'SandboxSDKError';
    this.code = code;
    this.httpStatus = httpStatus;
    this.details = details;
  }
}

/**
 * Map a billing-proxy error response to a typed error. Callers branch on
 * `code`, never on message strings — the strings here are the server's
 * (internal/proxy, internal/billing) and Daytona's quota texts.
 */
export function mapHttpError(status: number, bodyText: string): SandboxSDKError {
  let message = bodyText;
  let details: unknown = bodyText;
  try {
    const parsed = JSON.parse(bodyText);
    details = parsed;
    // gin handlers return {error}; Daytona passthrough returns
    // {statusCode, error: "Bad Request", message: "<specific>"} — prefer the
    // specific message when both exist.
    if (typeof parsed?.message === 'string') message = parsed.message;
    else if (typeof parsed?.error === 'string') message = parsed.error;
  } catch {
    /* non-JSON body — keep raw text */
  }
  const m = message.toLowerCase();

  let code: ErrorCode = 'API_ERROR';
  if (m.includes('insufficient balance')) code = 'INSUFFICIENT_BALANCE';
  else if (m.includes('not acknowledged') || m.includes('acknowledgement check failed')) code = 'NOT_ACKNOWLEDGED';
  else if (m.includes('only accepts sealed')) code = 'SEALED_ONLY';
  else if (m.includes('sealed sandbox:') || m.includes('sealed containers')) code = 'SEALED_FORBIDDEN';
  else if (m.includes('limit exceeded') || m.includes('quota')) code = 'QUOTA_EXCEEDED';
  else if (m.includes('publicports is not supported')) code = 'PUBLIC_PORTS_UNSUPPORTED';
  else if (status === 401) code = 'UNAUTHORIZED';
  else if (status === 403) code = 'FORBIDDEN';
  else if (status === 404) code = 'NOT_FOUND';

  return new SandboxSDKError(code, message, status, details);
}
