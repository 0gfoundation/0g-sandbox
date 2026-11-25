# Stuck Settlement Recovery

[中文](SETTLEMENT_RECOVERY.zh.md)

The settler waits on each settlement transaction it broadcasts until the
transaction has a receipt or the provider's confirmed nonce passes it. It never
replaces a transaction, because a replacement that mined alongside the original
would charge the same usage twice. A settlement transaction that never mines
(for example one priced below what the chain accepts, or evicted from the
mempool) therefore blocks every later settlement.

`billing cancel-pending-settlement` frees the stuck nonce with a zero-value
self-transfer signed by the node's KMS provider key. Once it mines, the settler
sees the original as dropped, re-queues its vouchers and settles them.

## Signs

- Alert `settler_tx_failure` (critical), "Settlement tx stuck pending …", with the
  transaction in `tx`. It fires after 12 polls
  5 seconds apart.
- `eth_getTransactionReceipt` of that transaction returns null, and the
  provider's confirmed nonce (`eth_getTransactionCount <provider> latest`) does
  not move.

## Flags

| Flag | Value |
|------|-------|
| `--chain-id` | the node's `CHAIN_ID` |
| `--provider` | the node's provider address (`/api/info` `provider_address`) |
| `--tx-hash` | the stuck transaction (the alert's `tx`, or the `tx` field of the settler's log) |
| `--nonce` | the provider's confirmed nonce, in decimal |
| `--max-fee-per-gas-wei` | above the original's fee cap, and at least the base fee plus the tip |
| `--tip-wei` | optional; above the original's tip. Without it the command uses the RPC's suggested tip; give it when that suggestion is below the minimum tip the chain accepts |
| `--broadcast` | sign and send. Without it the command only prints the plan |

## Guards

The command signs nothing unless all of these hold:

- The node uses the KMS provider key over the Tapp socket (`PROVIDER_KEY_SOURCE=kms`,
  no `MOCK_TEE`), and `--chain-id` matches the node's config.
- The provider's pending-settlement record in Redis (`settler:pending-tx:<provider>`)
  names the same transaction and nonce. This is checked before signing and again
  right before sending.
- The original has no receipt, and the provider's confirmed nonce equals `--nonce`.
- The provider address has no code.
- If the RPC still returns the original, it is a `settleFeesWithTEE` call from the
  provider to the settlement contract, and its fee cap and tip are below the
  cancellation's.
- The attested KMS signer is the provider.
- The provider's balance covers 21000 gas at the fee cap.

## Running it on a TEE node

The provider key is only reachable inside the node, so the command runs as a
one-shot service of the node's compose, on the same billing image as `sandbox`.
Add two services with the incident's values:

```yaml
x-settlement-recovery: &settlement-recovery
  image: ${SANDBOX_IMAGE}
  group_add: ["0"]
  environment:
    DAYTONA_API_URL: http://api:3000
    DAYTONA_ADMIN_KEY: ${DAYTONA_ADMIN_KEY}
    REDIS_ADDR: sandbox-redis:6379
    REDIS_PASSWORD: ${REDIS_PASSWORD}
    SETTLEMENT_CONTRACT: ${SETTLEMENT_CONTRACT}
    CHAIN_ID: ${CHAIN_ID}
    RPC_URL: ${RPC_URL}
    BACKEND_APP_NAME: ${BACKEND_APP_NAME}
    BACKEND_TAPP_SOCKET: /run/tapp/tapp.sock
    PROVIDER_KEY_SOURCE: ${PROVIDER_KEY_SOURCE}
  volumes:
    - /run/tapp/tapp.sock:/run/tapp/tapp.sock
  depends_on:
    sandbox-redis:
      condition: service_healthy
  networks: [app-net]
  restart: "no"

services:
  settlement-recovery-preview:
    <<: *settlement-recovery
    profiles: [settlement-recovery-preview]
    command:
      - cancel-pending-settlement
      - --chain-id=<chain id>
      - --provider=<provider address>
      - --tx-hash=<stuck transaction>
      - --nonce=<confirmed nonce>
      - --max-fee-per-gas-wei=<wei>

  settlement-recovery-broadcast:
    <<: *settlement-recovery
    profiles: [settlement-recovery-broadcast]
    command:
      - cancel-pending-settlement
      - --chain-id=<chain id>
      - --provider=<provider address>
      - --tx-hash=<stuck transaction>
      - --nonce=<confirmed nonce>
      - --max-fee-per-gas-wei=<wei>
      - --broadcast
```

1. The compose is part of the app's measurement, so adding the services is an app
   update (`updateApp`) and a restart of the node, which interrupts running
   sandboxes. The profiles start only when named.
2. Start `settlement-recovery-preview` once (tapp-cli `start-service`). Its log
   prints `Cancellation plan: … fee_cap_wei=… tip_wei=… max_cost_wei=…` and
   `Dry run: cancellation not signed or broadcast.`
3. Check the plan, then start `settlement-recovery-broadcast` once. Its log prints
   `Broadcast cancellation <hash>`.
4. Wait for the cancellation's receipt before anything else. The settler then logs
   the original as dropped (it confirms on a second poll), re-queues the batch and
   settles it.
5. Remove both services at the node's next planned update.
