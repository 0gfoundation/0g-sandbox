# 0G Sandbox — Contract Registry

Network: **0G Galileo Testnet** (chain ID 16602)
Explorer: https://chainscan-galileo.0g.ai
Deployer/Owner: `0xB831371eb2703305f1d9F8542163633D0675CEd7`

> Chinese version: [CONTRACTS.zh.md](CONTRACTS.zh.md)

---

## Dev Contract

> For local development and integration tests. Data may be reset at any time.

| Component | Address |
|-----------|---------|
| **Proxy** (stable) | `0x2024eB0Cc14316fF8Cc425bFB7CC37FD8713E9b3` |
| Beacon | `0xaa77C82Dc6b4243Ff272d88619BD4f23455CCB6E` |

**Upgrade history:**

| Date | Impl | Notes |
|------|------|-------|
| initial | — | Initial deploy: per-provider balance isolation, owner model |
| 2026-03-10 | `0x9a3D6C66e3e6E020D8D40d851Db76D76EBfa93f2` | Removed `msg.sender == provider` check in `settleFeesWithTEE`; TEE key signs settlement txs directly, no `PROVIDER_PRIVATE_KEY` needed |

```env
SETTLEMENT_CONTRACT=0x2024eB0Cc14316fF8Cc425bFB7CC37FD8713E9b3
```

---

## Testnet Contract

> Production testnet deployment for provider registration and real billing tests.

| Component | Address |
|-----------|---------|
| **Proxy** (stable) | `0xd7e0CD227e602FedBb93c36B1F5bf415398508a4` |
| Beacon | `0xe75F37A353EbCbAA497Ea752a6c910c9d0462382` |
| Implementation | `0x6B789e297bcC3c2F375779f1224b534A4c576445` |

**Deployed:** 2026-03-10
**Provider stake:** 100 0G (`100000000000000000000` neuron)

```env
SETTLEMENT_CONTRACT=0xd7e0CD227e602FedBb93c36B1F5bf415398508a4
```

---

## Architecture

```
User/Billing ──► BeaconProxy  (stable address, all ETH/state lives here)
                     │ reads implementation from beacon
                     ▼
               UpgradeableBeacon  (stores current impl, owned by deployer)
                     │ delegatecall
                     ▼
               SandboxServing impl  (pure logic, no state, replaceable)
```

The **proxy address never changes**. Upgrading only replaces the implementation.
Given the proxy address, beacon and impl can always be derived on-chain:

```bash
# Beacon address — ERC-1967 slot
cast storage <proxy> 0xa3f0ad74e5423aebfd80d3ef4346578335a9a72aeaee59ff6cb3582b35133d50

# Current implementation
cast call <beacon> "implementation()(address)"

# Beacon owner
cast call <beacon> "owner()(address)"
```

---

## Deploy (first time)

Deploys the full beacon-proxy stack in 3 steps:
1. SandboxServing implementation (no constructor args)
2. UpgradeableBeacon (impl, deployer)
3. BeaconProxy (beacon, initialize(tappRegistry))

Prerequisite: TappRegistry deployed on the target chain (its address comes
from the 0g-tapp repo's deployment record).

```bash
go run ./cmd/deploy/ \
  --rpc      https://evmrpc-testnet.0g.ai \
  --key      0x<deployer-private-key> \
  --chain-id 16602 \
  --tapp     0x<tapp-registry-address>
```

Output:
```
Implementation : 0x...
Beacon         : 0x...
Proxy (stable) : 0x...   ← set this as SETTLEMENT_CONTRACT
TappRegistry   : 0x...   ← set this as TAPP_REGISTRY
```

| Flag | Default | Description |
|------|---------|-------------|
| `--rpc` | `https://evmrpc-testnet.0g.ai` | EVM RPC endpoint |
| `--key` | (required) | Deployer private key (hex, with or without 0x) |
| `--chain-id` | `16602` | Chain ID |
| `--tapp` | (required) | TappRegistry contract address — passed to `initialize()` |

---

## Upgrade

Deploys a new implementation and points the beacon at it.
**Proxy address is unchanged** — no `.env` update needed, no user re-acknowledgement required.

```bash
go run ./cmd/upgrade/ \
  --rpc      https://evmrpc-testnet.0g.ai \
  --key      0x<deployer-private-key> \
  --chain-id 16602 \
  --proxy    0x<proxy-address>
```

| Flag | Default | Description |
|------|---------|-------------|
| `--rpc` | `https://evmrpc-testnet.0g.ai` | EVM RPC endpoint |
| `--key` | (required) | Deployer/owner private key |
| `--chain-id` | `16602` | Chain ID |
| `--proxy` | (required*) | BeaconProxy address — beacon resolved automatically |
| `--beacon` | (required*) | UpgradeableBeacon address (alternative to `--proxy`) |

\* Provide either `--proxy` or `--beacon`.

---

## Verify

Verifies all three contracts on the block explorer.
**Only the proxy address is needed** — beacon and impl are resolved automatically from chain.

```bash
./scripts/verify-contracts.sh --proxy 0x<proxy-address>
```

---

## Provider Registration

The provider's trust root lives in **TappRegistry**: `appId` → composeHash, image hashes,
and the active TEE node set. The provider registers the app there first (via `tapp-cli`),
then binds commercial terms (URL, prices, createFee) to that `appId` in **SandboxServing**.

See [`CLI.md`](CLI.md) for full flag reference.

```bash
# 1. Register the TEE-app trust root in TappRegistry (provider key must own the appId)
tapp-cli -s http://<tapp-server>:50051 start-app \
  --app-id 0g-sandbox-provider \
  -f docker-compose.yml

# 2. Authorize SandboxServing as an invalidator of this app's acks
#    (so a price change in SandboxServing bumps ackVersion in TappRegistry)
tapp-cli authorize-invalidator-onchain \
  --app-id    0g-sandbox-provider \
  --contract  0x<sandbox-serving-proxy>

# 3. Bind commercial terms to the appId in SandboxServing
PROVIDER_KEY=0x<provider-key> go run ./cmd/provider/ register \
  --api            http://<billing-proxy>:8080 \
  --app-id         0g-sandbox-provider \
  --price-per-cpu  <neuron/cpu/min> \
  --price-per-mem  <neuron/memGB/min> \
  --create-fee     <neuron>
```

Then set `PROVIDER_ADDRESS` and `TAPP_REGISTRY` in `.env`, and ensure the provider
wallet holds enough 0G to pay gas for settlement.

---

## Design Notes

- **Proxy address never changes** — upgrading only replaces the implementation; the proxy address is the stable external-facing address
- **Open settlement** — `settleFeesWithTEE` can be called by anyone; the provider is identified by `v.provider` in the voucher, not `msg.sender`
- **Trust root delegation** — SandboxServing holds only commercial terms; TEE signer identity and user acknowledgement live in TappRegistry and are queried on every voucher verification
- **`appId` is set-once** — once `addOrUpdateService` has bound a non-empty `appId`, subsequent calls must use the same value (provider can only update URL / prices / createFee, not the trust root)
