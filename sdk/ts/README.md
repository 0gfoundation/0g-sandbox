# @0gfoundation/sandbox-sdk

TypeScript SDK for [0G Private Sandbox](https://github.com/0gfoundation/0g-sandbox) — private,
isolated sandboxes billed in 0G tokens. Wraps the EIP-191 per-request signing protocol and the
on-chain deposit/acknowledge/refund flow so agents never touch raw headers or ABIs.

Works in Node ≥ 18 and browsers. Single dependency: [viem](https://viem.sh).

## Quick start (agent with a private key)

```ts
import { createSandboxSDK, privateKeySigner } from '@0gfoundation/sandbox-sdk';

const sdk = createSandboxSDK({
  providerUrl: 'https://provider-private-sandbox.0g.ai',
  signer: privateKeySigner(process.env.USER_KEY as `0x${string}`),
});

// One-time setup: fund the (user, provider) balance and acknowledge the TEE app.
await sdk.chain.deposit({ og: 0.5 });          // provider address auto-resolved from /api/info
await sdk.chain.acknowledge();                 // verifies the trust root on-chain first

// Sandbox lifecycle
const sb = await sdk.sandbox.create({ snapshot: '0g-ubuntu22', env: { FOO: 'bar' } });
const { exitCode, output } = await sb.exec('python3 --version');
console.log(sb.previewUrl(8080));              // http(s)://8080-<id>.<domain>
await sb.stop();                               // filesystem preserved, billing stops
await sb.delete();
```

Sealed (attested) sandbox:

```ts
const sb = await sdk.sandbox.create({ sealed: true, publicPorts: [8080], snapshot: '0g-sealed-app' });
// SSH/toolbox are blocked by the provider; interact via sb.previewUrl(8080)
```

## Browser wallets

```ts
import { createSandboxSDK, fromEip1193 } from '@0gfoundation/sandbox-sdk';
const signer = await fromEip1193(window.ethereum);
const sdk = createSandboxSDK({ providerUrl, signer });
```

Note: the protocol signs **every** API request, so interactive wallets show one popup per call.
Use `privateKeySigner` for agents and automation.

## Broker: one endpoint in front of many providers

Instead of hardcoding a provider, go through a broker. The `Broker` class handles
discovery, provider selection, and routing — every request is transparently reverse-proxied to
the chosen provider (same-origin, so browsers avoid CORS). You only ever hold the broker URL.

```ts
import { Broker, privateKeySigner } from '@0gfoundation/sandbox-sdk';

const broker = new Broker({ brokerUrl: 'https://private-sandbox-testnet.0g.ai', signer: privateKeySigner(key) });

await broker.providers();                                   // on-chain-indexed list (cached)

// Operate with an optional target. The returned Sandbox is pinned to the
// provider it was created on — exec/stop/delete auto-route back to it.
const sb = await broker.sandbox.create({ snapshot: '0g-openclaw' });        // no provider → picks one that has the snapshot
const sb2 = await broker.sandbox.create({}, { provider: '0xa19c…' });        // explicit provider
await broker.chain.deposit({ og: 0.5 }, { provider: '0xa19c…' });
```

**Provider resolution** (the `target` argument, both fields optional):

| `target` / create opts | picked provider |
|---|---|
| `{ provider: '0x…' }` | that address |
| omitted + `create({ snapshot })` | a provider with that snapshot **active** (else `NO_PROVIDER`) |
| omitted, no snapshot | first indexed provider |
| `{ strategy: … }` | reserved — throws `NOT_IMPLEMENTED` until broker-side routing lands |

On-chain **write** operations (`chain.deposit` / `requestRefund` / `withdrawRefund` /
`acknowledge` / `revokeAcknowledgement`) require an explicit `{ provider }` — they throw
`NO_PROVIDER` rather than default to an arbitrary provider, since list order isn't stable and
sending funds/acks to the wrong provider is a footgun. Reads (`balance`) and `create` may
default.

The direct path (`createSandboxSDK({ providerUrl })`) still works unchanged — use it when you
already know your provider; use `Broker` when you want discovery + selection handled for you.

## Error handling

All failures throw `SandboxSDKError` with a stable `code` — branch on it, not on messages:

```ts
import { SandboxSDKError } from '@0gfoundation/sandbox-sdk';
try {
  await sdk.sandbox.create();
} catch (e) {
  if (e instanceof SandboxSDKError && e.code === 'INSUFFICIENT_BALANCE') {
    await sdk.chain.deposit({ og: 0.1 });
  } else throw e;
}
```

Codes: `INSUFFICIENT_BALANCE` `NOT_ACKNOWLEDGED` `SEALED_ONLY` `SEALED_FORBIDDEN`
`QUOTA_EXCEEDED` `PUBLIC_PORTS_UNSUPPORTED` `UNAUTHORIZED` `FORBIDDEN` `NOT_FOUND`
`TRUST_MISMATCH` `SIGNER_NO_TX` `NO_PROVIDER` `INVALID_ARGUMENT` `NOT_IMPLEMENTED`
`API_ERROR` `CHAIN_ERROR`.

The SDK prefers a stable `code` field if the server sends one, and only falls back to
substring-matching the error text. `create()` validates **format invariants** client-side
(port range, `sealId` 64 hex, sealed⊇8080) and throws `INVALID_ARGUMENT` before any network
call — but it does **not** duplicate server *policy* (max port count, system-port list,
quotas); those stay authoritative on the server so the SDK never wrongly rejects on a limit
change.

HTTP requests carry a timeout (`SDKConfig.timeoutMs`, default 60s; `exec` extends it past the
command's own timeout). Sandboxes created via a `Broker` expose `sb.provider` so you can
re-target them later with `broker.sandbox.get(id, { provider: sb.provider })`.

## Chain defaults

Defaults to 0G Galileo testnet (chain 16602). The settlement contract is auto-discovered from
the provider's `GET /api/info`; TappRegistry defaults to the testnet deployment. Override any of
it:

```ts
createSandboxSDK({ providerUrl, signer, chain: { rpcUrl, chainId, settlementContract, tappRegistry } });
```

All neuron amounts are `bigint` (1 0G = 10^18 neuron); `{ og: 0.5 }` is accepted at input
boundaries.

## Preview URLs

The server attaches `preview_urls` to the create response only when `publicPorts` is set. For
all-ports-public creates, give the SDK the provider's proxy domain so `previewUrl()` can build
URLs locally:

```ts
createSandboxSDK({ providerUrl, signer, preview: { domain: 'provider-private-sandbox.0g.ai' } });
```

## Escape hatches

```ts
await sdk.signedFetch('POST', `/api/sandbox/${id}/ensure-billing`, { action: 'ensure-billing', resourceId: id });
await sb.toolbox('GET', '/git/status?path=/workspace');  // raw Daytona toolbox
```

## Development

```bash
npm install
npm test            # unit + golden signing vectors
npm run vectors     # regenerate test/vectors.json (verified by internal/auth Go tests)
npm run build       # dist/ (ESM + CJS + d.ts)
PROVIDER_URL=… USER_KEY=0x… npm run e2e   # live end-to-end against a provider
```

The golden vectors in `test/vectors.json` are the cross-language signing contract: the Go test
`internal/auth/sdk_vectors_test.go` verifies every vector with the server's actual
`auth.Recover`. Overview and the signing-protocol/security notes: [`../README.md`](../README.md).
