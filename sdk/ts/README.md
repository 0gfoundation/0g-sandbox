# @0glabs/sandbox-sdk

TypeScript SDK for [0G Private Sandbox](https://github.com/0gfoundation/0g-sandbox) — private,
isolated sandboxes billed in 0G tokens. Wraps the EIP-191 per-request signing protocol and the
on-chain deposit/acknowledge/refund flow so agents never touch raw headers or ABIs.

Works in Node ≥ 18 and browsers. Single dependency: [viem](https://viem.sh).

## Quick start (agent with a private key)

```ts
import { createSandboxSDK, privateKeySigner } from '@0glabs/sandbox-sdk';

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
import { createSandboxSDK, fromEip1193 } from '@0glabs/sandbox-sdk';
const signer = await fromEip1193(window.ethereum);
const sdk = createSandboxSDK({ providerUrl, signer });
```

Note: the protocol signs **every** API request, so interactive wallets show one popup per call.
Use `privateKeySigner` for agents and automation.

## Error handling

All failures throw `SandboxSDKError` with a stable `code` — branch on it, not on messages:

```ts
import { SandboxSDKError } from '@0glabs/sandbox-sdk';
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
`TRUST_MISMATCH` `SIGNER_NO_TX` `API_ERROR` `CHAIN_ERROR`.

## Chain defaults

Defaults to 0G Galileo testnet (chain 16602). The settlement contract is auto-discovered from
the provider's `GET /api/info`; TappRegistry defaults to the testnet deployment. Override any of
it:

```ts
createSandboxSDK({ providerUrl, signer, chain: { rpcUrl, chainId, settlementContract, tappRegistry } });
```

All neuron amounts are `bigint` (1 0G = 10^18 neuron); `{ og: 0.5 }` is accepted at input
boundaries.

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
`auth.Recover`. Interface rationale: [`docs/SDK_TS_DESIGN.md`](../../docs/SDK_TS_DESIGN.md).
