import type { Account } from 'viem';
import { privateKeyToAccount } from 'viem/accounts';

/**
 * Anything that can EIP-191 `personal_sign`. The SDK never touches the key
 * directly. Tx capability (for on-chain deposit/acknowledge) is optional —
 * signers built by the factories below carry it automatically.
 */
export interface Signer {
  readonly address: `0x${string}`;
  /** EIP-191 personal_sign over raw bytes; returns a 65-byte 0x-hex signature (V = 27/28). */
  signMessage(message: Uint8Array): Promise<`0x${string}`>;
  /** @internal transaction capability for sdk.chain; absent on sign-only signers. */
  readonly txCapability?: TxCapability;
}

export type TxCapability =
  | { kind: 'viem-account'; account: Account }
  | { kind: 'eip1193'; provider: Eip1193Provider };

export interface Eip1193Provider {
  request(args: { method: string; params?: unknown[] }): Promise<unknown>;
}

/** Signer from a raw private key (agents / server-side use). */
export function privateKeySigner(privateKey: `0x${string}`): Signer {
  const account = privateKeyToAccount(privateKey);
  return {
    address: account.address,
    signMessage: (message) => account.signMessage({ message: { raw: message } }),
    txCapability: { kind: 'viem-account', account },
  };
}

/** Signer from any viem local account. */
export function fromViemAccount(account: Account): Signer {
  if (!account.signMessage) throw new Error('viem account lacks signMessage capability');
  return {
    address: account.address,
    signMessage: (message) => account.signMessage!({ message: { raw: message } }),
    txCapability: { kind: 'viem-account', account },
  };
}

/**
 * Signer from a browser wallet (EIP-1193). Note: the auth protocol signs
 * every API request, so interactive wallets get one popup per call — fine
 * for tx-style UIs, use privateKeySigner for agents.
 */
export async function fromEip1193(provider: Eip1193Provider): Promise<Signer> {
  const accounts = (await provider.request({ method: 'eth_requestAccounts' })) as string[];
  if (!accounts?.length) throw new Error('wallet returned no accounts');
  const address = accounts[0] as `0x${string}`;
  return {
    address,
    signMessage: async (message) => {
      const hex = ('0x' + bytesToHexRaw(message)) as `0x${string}`;
      return (await provider.request({
        method: 'personal_sign',
        params: [hex, address],
      })) as `0x${string}`;
    },
    txCapability: { kind: 'eip1193', provider },
  };
}

// ── Request signing ───────────────────────────────────────────────────────────

export interface AuthHeaders {
  'X-Wallet-Address': string;
  'X-Signed-Message': string;
  'X-Wallet-Signature': `0x${string}`;
}

export interface SignOptions {
  /** Seconds until expiry (server caps future window at 5 min). Default 180. */
  ttlSec?: number;
  /** Override nonce (32 hex chars) — for deterministic tests only. */
  nonce?: string;
  /** Override "now" as unix seconds — for deterministic tests only. */
  nowSec?: number;
}

/**
 * Build the three auth headers the billing proxy expects. The signed message
 * is `{action, expires_at, nonce, payload, resource_id}` (alphabetical key
 * order, compact JSON — byte-compatible with cmd/user). The server verifies
 * the signature over the exact bytes sent in X-Signed-Message.
 */
export async function buildAuthHeaders(
  signer: Signer,
  action: string,
  resourceId: string,
  payload: unknown,
  opts: SignOptions = {},
): Promise<AuthHeaders> {
  const nonce = opts.nonce ?? randomNonce();
  const nowSec = opts.nowSec ?? Math.floor(Date.now() / 1000);
  const expiresAt = nowSec + (opts.ttlSec ?? 180);

  // Key order matters only for reproducibility (golden vectors), not for the
  // server, which signs/verifies the exact bytes. Keep it alphabetical.
  const message = JSON.stringify({
    action,
    expires_at: expiresAt,
    nonce,
    payload: payload ?? {},
    resource_id: resourceId,
  });
  const messageBytes = new TextEncoder().encode(message);
  const signature = await signer.signMessage(messageBytes);

  return {
    'X-Wallet-Address': signer.address,
    'X-Signed-Message': bytesToBase64(messageBytes),
    'X-Wallet-Signature': signature,
  };
}

function randomNonce(): string {
  const buf = new Uint8Array(16);
  globalThis.crypto.getRandomValues(buf);
  return bytesToHexRaw(buf);
}

function bytesToHexRaw(bytes: Uint8Array): string {
  let out = '';
  for (const b of bytes) out += b.toString(16).padStart(2, '0');
  return out;
}

function bytesToBase64(bytes: Uint8Array): string {
  if (typeof Buffer !== 'undefined') return Buffer.from(bytes).toString('base64');
  let bin = '';
  for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin);
}
