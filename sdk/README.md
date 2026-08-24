# 0G Sandbox — Client SDKs

Client libraries for [0G Private Sandbox](../README.md). They wrap the two things every client
must otherwise do by hand:

1. **EIP-191 request signing** — each request carries a signed
   `{action, expires_at, nonce, payload, resource_id}` message in the
   `X-Wallet-Address` / `X-Signed-Message` / `X-Wallet-Signature` headers.
2. **On-chain billing** — deposit / acknowledge / balance / refund against SandboxServing +
   TappRegistry.

## Languages

| Dir | Package | Status |
|-----|---------|--------|
| [`ts/`](ts/) | `@0gfoundation/sandbox-sdk` | TypeScript (Node ≥ 18 + browser). See [ts/README.md](ts/README.md). |

Go and Python may follow as sibling directories; the signing protocol is pinned by shared
golden vectors (see below) so every language stays byte-compatible.

## Access paths

- **Direct** — `createSandboxSDK({ providerUrl })` when you already know your provider.
- **Broker** — `new Broker({ brokerUrl })` fronts many providers through one endpoint: it
  discovers, selects (explicit address today; snapshot-aware default; strategy reserved), and
  reverse-proxies to the chosen provider (browsers avoid CORS). Created sandbox is pinned to
  its origin provider.

## Interactions by target

**Broker** — discovery + routing:
- `broker.info()` — chain config the broker indexes
- `broker.providers()` — provider list
- provider selection via the `target` argument on each operation
- reverse-proxy routing to the selected provider

**Provider** — the actual work (direct, or proxied through the broker):
- `sandbox.*` — create / list / get / exec / toolbox / start / stop / delete / archive / sshAccess
- `chain.*` — deposit / acknowledge / balance / requestRefund / withdrawRefund (on-chain, straight to RPC and the `(you, provider)` bucket)
- `provider.info()` / `provider.snapshots()`

## Signing protocol contract

The signed message is compact JSON with alphabetical keys; the server verifies the signature
over the exact bytes sent in `X-Signed-Message`, so key order is not load-bearing (the SDK
still emits alphabetical order to match `cmd/user`). This is pinned by golden vectors in
`ts/test/vectors.json`, which the Go test `internal/auth/sdk_vectors_test.go` re-verifies with
the server's actual `auth.Recover` — if either side drifts, a test fails.

## Note: server-side auth binding (not an SDK issue)

`auth.Middleware` verifies signature, expiry, and nonce, but does **not** bind the signed
`action` / `resource_id` / `payload` to the actual route and body. Within the expiry window a
captured header set authorizes any route as that wallet (nonce blocks replay of the *same*
request; TLS prevents capture in practice). Tightening this server-side would be a breaking
auth change — the SDKs already fill `action`/`resource_id` per route, so they are
forward-compatible with such a check. Worth a follow-up issue.
