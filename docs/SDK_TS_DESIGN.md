# TypeScript SDK — Interface Design (v0)

Package: `@0glabs/sandbox-sdk` · Targets: Node ≥ 18 + browser (fetch-based) · Dep: `viem` only

Wraps the two things every client must do today by hand:
1. **EIP-191 request signing** — `{action, expires_at, nonce, payload, resource_id}` JSON →
   `personal_sign` → `X-Wallet-Address` / `X-Signed-Message` (base64) / `X-Wallet-Signature` headers
   (nonce = 16 random bytes hex, expiry = now + 180 s, V normalised to 27/28).
2. **On-chain flow** — `deposit` / TappRegistry `acknowledgeApp` (with the trust-root checks
   `cmd/user acknowledge` performs) / balance / refund.

---

## Entry point

```ts
import { createSandboxSDK, privateKeySigner } from '@0glabs/sandbox-sdk'

const sdk = createSandboxSDK({
  providerUrl: 'https://provider-private-sandbox.0g.ai',
  signer: privateKeySigner('0x…'),
})

export interface SDKConfig {
  providerUrl: string          // billing proxy base URL
  signer: Signer
  chain?: Partial<ChainConfig> // defaults = 0G Galileo testnet (rpc/chainId baked in);
                               // settlementContract/tappRegistry default from GET /api/info
  fetch?: typeof fetch         // injectable for tests
}
```

### Signer abstraction

The SDK never touches the key directly — anything that can `personal_sign` works:

```ts
export interface Signer {
  readonly address: `0x${string}`
  signMessage(message: Uint8Array): Promise<`0x${string}`> // EIP-191, 65-byte sig
}

export function privateKeySigner(key: `0x${string}`): Signer        // viem privateKeyToAccount
export function fromViemAccount(account: Account): Signer           // any viem account
export function fromEip1193(provider: Eip1193Provider): Signer      // browser wallet (popup per request — documented limitation)
```

---

## `sdk.chain` — on-chain operations

Provider address is auto-resolved from `GET /api/info` (`provider_address`) and cached; every
method takes an optional `provider` override for multi-provider use.

```ts
interface ChainApi {
  providerAddress(): Promise<Address>

  balance(provider?): Promise<{ balance: bigint; pendingRefund: bigint; refundUnlockAt: bigint }>
  deposit(amount: bigint | { og: number }, provider?): Promise<TxReceipt>

  // Read-only review: commercial terms (SandboxServing.services) + trust root
  // (TappRegistry getAppInfo/getNodeList/getAckVersion) — lets a UI show what the
  // user is agreeing to BEFORE the tx. Throws TRUST_MISMATCH if the provider is
  // not an active node of its appId (same guard as cmd/user acknowledge).
  reviewProvider(provider?): Promise<ProviderReview>
  acknowledge(provider?): Promise<TxReceipt>       // runs reviewProvider checks first
  revokeAcknowledgement(provider?): Promise<TxReceipt>
  isAcknowledged(provider?): Promise<boolean>

  requestRefund(amount: bigint, provider?): Promise<TxReceipt>
  withdrawRefund(provider?): Promise<TxReceipt>
}
```

`ProviderReview`: `{ url, appId, createFee, pricePerCPUPerMin, pricePerMemGBPerMin, appOwner, ackVersion, composeHash, imageHashes, nodes: [{ signer, teeUrl }] }`.

---

## `sdk.sandbox` — lifecycle + execution

```ts
interface SandboxApi {
  create(opts?: CreateOptions): Promise<Sandbox>
  list(): Promise<SandboxInfo[]>
  get(id: string): Promise<Sandbox>
}

interface CreateOptions {
  name?: string
  snapshot?: string                       // locks cpu/memory/disk (server rule)
  class?: 'small' | 'medium' | 'large'
  cpu?: number; memory?: number; disk?: number
  env?: Record<string, string>
  sealed?: boolean                        // blocks SSH/toolbox; needs publicPorts ⊇ [8080]
  sealId?: string                         // 64-hex, random if unset
  publicPorts?: number[]                  // ≤16, no system ports; omit = all public
}

interface Sandbox {
  readonly id: string
  readonly info: SandboxInfo              // last-known state; refresh() to update
  readonly previewUrls: Record<number, string>  // from create response
  previewUrl(port: number): string        // http(s)://<port>-<id>.<proxyDomain>

  exec(cmd: string, opts?: { timeoutSec?: number; cwd?: string }): Promise<{ exitCode: number; output: string }>
  toolbox<T = unknown>(method: string, path: string, body?: unknown): Promise<T>  // raw Daytona toolbox escape hatch

  start(): Promise<void>
  stop(): Promise<void>                   // FS preserved (auto-backup)
  delete(): Promise<void>
  archive(): Promise<void>
  sshAccess(): Promise<SshAccessInfo>     // rejects with SEALED_FORBIDDEN on sealed
  refresh(): Promise<SandboxInfo>
}
```

`exec` maps to `POST /api/toolbox/{id}/toolbox/process/execute` (`{command, timeout}` →
`{exitCode, result}`), signed with `action: "toolbox"`, `resource_id: id`. File helpers
(`files.read/write/ls` over toolbox) are v0.1.

---

## `sdk.provider` — unauthenticated discovery

```ts
interface ProviderInfoApi {
  info(): Promise<ProviderInfo>          // GET /api/info: provider_address, owner, pricing, sealed_only, app_id
  snapshots(): Promise<SnapshotInfo[]>   // GET /api/snapshots
}
// module-level, no client needed:
export function discoverProviders(opts?: { brokerUrl?: string; rpcUrl?: string }): Promise<ProviderEntry[]>
```

---

## Errors

```ts
class SandboxSDKError extends Error {
  code: 'INSUFFICIENT_BALANCE' | 'NOT_ACKNOWLEDGED' | 'SEALED_ONLY' | 'SEALED_FORBIDDEN'
      | 'QUOTA_EXCEEDED' | 'UNAUTHORIZED' | 'TRUST_MISMATCH' | 'NOT_FOUND' | 'API_ERROR' | 'CHAIN_ERROR'
  httpStatus?: number
  details?: unknown   // raw server body
}
```

Codes are parsed from the billing-proxy error bodies (e.g. quota text from Daytona is mapped to
`QUOTA_EXCEEDED`), so callers branch on `code`, never on message strings.

---

## Advanced / escape hatch

```ts
// The raw signing primitive, exported for anything the typed surface doesn't cover:
sdk.signedFetch(method, path, { action, resourceId, payload, body }): Promise<Response>
export function buildAuthHeaders(signer, action, resourceId, payload): Promise<AuthHeaders>
```

---

## Package layout

```
sdk/ts/
  package.json        ESM + CJS dual build, viem as dependency
  src/signer.ts       request signing (golden-vector tested)
  src/http.ts         signedFetch + error mapping
  src/chain.ts        minimal ABI subset: deposit/getBalance/requestRefund/withdrawRefund/
                      services/getLastNonce + TappRegistry ack surface
  src/sandbox.ts      lifecycle + toolbox
  src/index.ts
  test/vectors.json   golden signed messages (shared input for a Go test that
                      verifies them with internal/auth.Recover — cross-language contract)
```

## Design notes

- **Signature is over the exact bytes sent** — the server verifies over the base64-decoded
  `X-Signed-Message` as-is, so JSON key order is not actually load-bearing; the SDK still emits
  alphabetical order to match the CLI. The golden vectors pin this.
- **Server-side observation (separate hardening issue, not SDK scope)**: `auth.Middleware` checks
  signature/expiry/nonce but does not bind `action`/`resource_id`/`payload` to the actual route
  and body — within the 3-minute window a captured header set authorizes any route as that
  wallet (nonce prevents replay of the same request, TLS prevents capture in practice).
  Tightening this server-side would be a breaking auth change; the SDK's per-route
  `action`/`resource_id` values are forward-compatible with such a check.
- **Browser wallets** work via `fromEip1193` but cost one popup per API call (protocol is
  sign-per-request). Fine for tx-style UIs; agents should use `privateKeySigner`. A session-key
  scheme is a future protocol change, out of SDK scope.
- **bigint everywhere for neuron amounts**; `{ og: number }` convenience accepted at input
  boundaries only.

## v0 acceptance

- E2E test against the dev environment: deposit → acknowledge → create (plain + sealed w/
  publicPorts) → exec → preview URL fetch → stop → delete → refund.
- Golden-vector test green in both TS (jest/vitest) and Go (`internal/auth`).
- README quick-start: agent use-case in ≤ 20 lines of TS.
