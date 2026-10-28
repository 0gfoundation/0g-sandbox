# 0G Sandbox — Contract Registry

Networks: **0G Mainnet** (chain ID 16661) · **0G Galileo Testnet** (chain ID 16602, used by dev + testnet). Each section below states its network, explorer, and RPC.

Owner (contract + beacon, read on chain 2026-10-28): mainnet = `0xc0603224dE8c3B910a4487368E476e19dC6127cF` (Fordefi MPC vault, since 2026-10-16; deployed by `0x73443d…`); testnet = `0x9C58F839451EBf3e7f82442bC48E4088746380Bb` (since 2026-10-14); dev = `0x2AB7F05bA6560ef2B21Aa1b52FCc96DB51Cfa0B3` (since 2026-10-19).


> Chinese version: [CONTRACTS.zh.md](README.zh.md)

---

## Mainnet Contract

> 0G Mainnet (chain ID 16661) · Explorer: https://chainscan.0g.ai · RPC: https://evmrpc.0g.ai
> Production deployment. Deployed 2026-09-09.

| Component | Address |
|-----------|---------|
| **Proxy** (stable) | `0x650C16a491ad52dB6b7f3053bCaD650cb43d2206` |
| Beacon | `0x3916925C9F34016d0182c731Cd8D1021fDEAe207` |
| TappRegistry | `0x54874F536301c993922Dd95097e3902e7FBfe612` |

**Upgrade history:**

| Date | Impl | Notes |
|------|------|-------|
| 2026-09-09 | `0x90a0f1BcaCE1A516CF4D1918178EB99E08c16f24` | Initial mainnet deploy (v2): provider IS the TEE signer; owner-managed register/remove/withdraw; payee-must-sign settlement. Bound to TappRegistry `0x5487…e612`. Owner = deployer `0x7344…431b`. |
| 2026-10-04 | (no impl change) | **Ownership transferred** — contract `owner` + beacon owner set to `0x87605Ec8E10Eb373C1d070e15E5d78FAC4D7621d` (previously `0x7344…431b`). No implementation change. |
| 2026-10-16 | (no impl change) | **Ownership transferred** — contract `owner` + beacon owner set to the Fordefi MPC vault `0xc0603224dE8c3B910a4487368E476e19dC6127cF` (previously `0x8760…621d`): contract tx `0xcf7fce1484530133dacc873d6e75b728de9c049b8005d737cb6c06ca333e243c`, beacon tx `0xa903456f00729094ba82f11823a1c1c741d50c3828b83f2e71d00d5538dca2da`. No implementation change. |

The KMS-provider implementation (dev 2026-10-26, testnet 2026-10-27) is not on mainnet yet; its beacon upgrade is a vault transaction.

```env
SETTLEMENT_CONTRACT=0x650C16a491ad52dB6b7f3053bCaD650cb43d2206
TAPP_REGISTRY=0x54874F536301c993922Dd95097e3902e7FBfe612
RPC_URL=https://evmrpc.0g.ai
CHAIN_ID=16661
```

---

## Dev Contract

> For local development and integration tests. Data may be reset at any time.

| Component | Address |
|-----------|---------|
| **Proxy** (stable) | `0x3D0F2D62A60c8e62095671FfB23D15Cc4C98ca7c` |
| Beacon | `0xBF04734BC87E12aB81E21bb4018b9bFa4c118721` |
| TappRegistry | `0x2Ce80374318B1d7Fb3345724457a182E0ad165c9` |

**Upgrade history:**

| Date | Impl | Notes |
|------|------|-------|
| initial | — | Initial deploy: per-provider balance isolation, owner model |
| 2026-03-10 | `0x9a3D6C66e3e6E020D8D40d851Db76D76EBfa93f2` | Removed `msg.sender == provider` check in `settleFeesWithTEE`; TEE key signs settlement txs directly, no `PROVIDER_PRIVATE_KEY` needed |
| 2026-07-19 | `0x47a8E809Cd81b94eD19874da73C0E3F82DD90E5C` | **v2 redeploy (new proxy/beacon)**: provider IS the TEE signer; owner-managed register/remove/withdraw; payee-must-sign settlement.  Previous dev proxy `0x2024eB0C…E9b3` retired (refund-only) |
| 2026-09-04 | (no impl change) | **Ownership transferred** — contract `owner` + beacon owner set to `0x3f1a683418dba4c38dd853a7b896f7327a9fef9f` (previously `0xB831…CEd7`, via `0xea69…1837` the same day). No implementation change. |
| 2026-10-19 | (no impl change) | **Ownership transferred** — contract `owner` + beacon owner set to `0x2AB7F05bA6560ef2B21Aa1b52FCc96DB51Cfa0B3` (previously `0x3f1a…9fef9f`): contract tx `0x23c2987fbec6627f11cec151145c1ff8676efb072038e2aa241cb29261e2e0e4`, beacon tx `0x6cc2047fb81fb9699f4481b813b02a6958495b56dc40f26a4ef6a7fb7ce11890`. No implementation change. |
| 2026-10-26 | `0xD3f1B3633F015D126e8374b5EDBE699a680DC3D3` | **KMS provider key** (#144): `addOrUpdateKmsService` registers a provider that is the app's KMS key, endorsed by an active node of the app (EIP-712 over `KmsProvider(provider, appId)`), and binds it to the app's current TappRegistry instance (`registeredAt`), which settlement and owner actions check; `repinKmsProvider` re-binds after a TappRegistry redeploy; new views `kmsBindings`, `kmsProviderDigest`, `isActiveProvider`. Node-signer providers unchanged; the new state takes one `__gap` slot. Deploy tx `0x4d6a26378fffd057fab27fe045f4febd06050b52bc1121062aa466f482e01143`, beacon `upgradeTo` tx `0xb07d9262ee6aaf700bef687a93cf19b50baa5c079151b1f8f2f65b90a9e70b0a`. Not yet verified on chainscan. On 2026-10-27 the dev sandbox's KMS provider `0x6051…c13a` was registered (tx `0x9fd5fc1dd675978669d69a03bd0b019e86c980f590b9b206dc9196c6f65ca6ea`) and its node-signer service `0x5f04…9965` removed (tx `0x73051b4d77c94d05bba27d2ee156d53b562931da7f70c6f4de7ca3fa29a90c6b`). |

```env
SETTLEMENT_CONTRACT=0x3D0F2D62A60c8e62095671FfB23D15Cc4C98ca7c
TAPP_REGISTRY=0x2Ce80374318B1d7Fb3345724457a182E0ad165c9
```

---

## Testnet Contract

> Production testnet deployment for provider registration and real billing tests.

| Component | Address |
|-----------|---------|
| **Proxy** (stable) | `0x3490B9053AC46F7Bf71A1ceBffcB2be2C1405b41` |
| Beacon | `0x79D6D7B5468AA134360bf73cc667FC63f704B62d` |
| TappRegistry | `0x2Ce80374318B1d7Fb3345724457a182E0ad165c9` |

**Upgrade history:**

| Date | Impl | Notes |
|------|------|-------|
| 2026-07-20 | `0x7a1A5FC5B1A6AC1127e2D8b63400615B2ea49C47` | **v2 redeploy (new proxy/beacon)**: provider IS the TEE signer; owner-managed register/remove/withdraw; payee-must-sign settlement. Verified on chainscan. Bound to TappRegistry `0x2Ce8…65c9` (repointed from `0x95a0…` via setTappRegistry right after deploy). Supersedes the v1 testnet proxies `0xA07b0033…FC12c` and `0x3d4d8a05…cf6f` — both retired (refund-only) |
| 2026-09-04 | (no impl change) | **Ownership transferred** — contract `owner` + beacon owner set to `0x3f1a683418dba4c38dd853a7b896f7327a9fef9f` (previously `0xB831…CEd7`, via `0xea69…1837` the same day). No implementation change. |
| 2026-10-14 | (no impl change) | **Ownership transferred** — contract `owner` + beacon owner set to `0x9C58F839451EBf3e7f82442bC48E4088746380Bb` (previously `0x3f1a…9fef9f`): contract tx `0x0ddd81829ab2a845ef16268bc993f64e235b6002af50e1342d3938366c0e9547`, beacon tx `0x555654195574a78476dd503d56f76db4e91f4bbf697d663a1c8cd34902faf3db`. No implementation change. |
| 2026-10-27 | `0x950Abcec39982F3e3eD6dF8F3D4e567AC69a180c` | The KMS-provider implementation (same source as dev's 2026-10-26 row, deployed separately): **KMS provider key** (#144): `addOrUpdateKmsService` registers a provider that is the app's KMS key, endorsed by an active node of the app (EIP-712 over `KmsProvider(provider, appId)`), and binds it to the app's current TappRegistry instance (`registeredAt`), which settlement and owner actions check; `repinKmsProvider` re-binds after a TappRegistry redeploy; new views `kmsBindings`, `kmsProviderDigest`, `isActiveProvider`. Node-signer providers unchanged; the new state takes one `__gap` slot. Deploy tx `0x242710d3277a0d813c3d38ee3bec3e32c18694267cdeb84ad9e9e89f1e720a83`, beacon `upgradeTo` tx `0x1931d7f2774576124a932d698f5dba4eaa96ca8f7d7ca7707b2b4cea3d3b5274`. Not yet verified on chainscan. The same day the testnet sandbox's KMS provider `0xb769…9A5a` was registered (tx `0xa6909e4d396c86554e81b80ffc30ffa4f9f024cb8a6b3c4dc7e1847a6378c2e2`). Its old node-signer service `0x3F7E…5467` is no longer an active provider but is still registered (`removeService` pending). |

**Provider stake:** held in TappRegistry per node (not in SandboxServing); see `minStakeAmount()` on the registry (1 0G at the time of writing).

```env
SETTLEMENT_CONTRACT=0x3490B9053AC46F7Bf71A1ceBffcB2be2C1405b41
TAPP_REGISTRY=0x2Ce80374318B1d7Fb3345724457a182E0ad165c9
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

## Interface

`SandboxServing` is the settlement contract. Users deposit 0G earmarked for a
specific provider; the provider binds commercial terms (URL, prices, createFee)
to a TappRegistry `appId`; TEE-signed vouchers then settle compute fees on-chain.
Trust identity — the active TEE signer set and user acknowledgements — lives in
**TappRegistry** and is queried on every settlement.

**User (billing)**

| Function | Notes |
|---|---|
| `deposit(recipient, provider)` payable | Fund `recipient`'s balance earmarked for `provider` |
| `requestRefund(provider, amount)` | Start a refund; withdrawable after `LOCK_TIME` |
| `withdrawRefund(provider)` | Withdraw an unlocked refund |
| `getBalance(user, provider)` → (balance, pendingRefund, refundUnlockAt) | view |
| `balanceOfBatch(users[], provider)` → uint256[] | view |
| `getLastNonce(user, provider)` → uint256 | view — last settled voucher nonce |
| `isTEEAcknowledged(user, provider)` → bool | view — delegates to `tapp.isAcknowledged(user, appId)` |

**Provider**

| Function | Notes |
|---|---|
| `addOrUpdateService(signer, url, appId, pricePerCPUPerMin, createFee, pricePerMemGBPerMin)` | App owner registers/updates a node's service; `signer` (= the provider address) must be an active TappRegistry node of the appId; `appId` set-once per signer |
| `addOrUpdateKmsService(provider, url, appId, pricePerCPUPerMin, createFee, pricePerMemGBPerMin, endorsement)` | App owner registers/updates a **KMS provider**: `provider` is the app's KMS key (the node runs `PROVIDER_KEY_SOURCE=kms`), `endorsement` an active node's EIP-712 signature over `KmsProvider(provider, appId)`. The first call binds the provider to the app's current TappRegistry instance (`registeredAt`); every later call must find that same instance |
| `kmsProviderDigest(provider, appId)` → bytes32 | view — the digest a node signs to endorse a KMS provider |
| `kmsBindings(provider)` → (epoch, appIdHash) | view — the app instance a KMS provider is bound to; `epoch == 0` for a node-signer provider |
| `isActiveProvider(provider)` → bool | view — whether the provider's own key may sign vouchers now (the settlement rule) |
| `removeService(signer)` | App owner removes a node's service (e.g. after a machine rebuild); sweeps pending earnings to the owner in the same tx; user balances stay refundable, nonce watermarks stay put |
| `withdrawEarnings(signer)` | App owner withdraws a node's accrued earnings to the owner's wallet |
| `services(provider)` / `serviceExists(provider)` | view — commercial terms (provider = the node's TEE signer address) |
| `getProviderEarnings(provider)` → uint256 | view |

**Settlement**

| Function | Notes |
|---|---|
| `settleFeesWithTEE(vouchers[])` → statuses[] | Permissionless; a voucher is valid only if signed BY its own payee (`recovered == v.provider`) and that payee's key is valid for the appId: an active TappRegistry node for a node-signer provider, the app instance it was bound to for a KMS provider. One provider can never settle vouchers naming another |
| `previewSettlementResults(vouchers[])` → statuses[] | view — dry-run statuses |

**Admin / setup**

| Function | Notes |
|---|---|
| `initialize(tappRegistry_)` | One-time, on the proxy |
| `owner()` / `transferOwnership(newOwner)` | Contract admin |
| `setTappRegistry(newRegistry)` | Repoint TappRegistry; ack state then reads from the new registry |
| `repinKmsProvider(provider)` | After a TappRegistry redeploy only: re-bind a KMS provider to its app's instance in the new registry. Contract owner only |
| `tappRegistry()` / `domainSeparator()` / `LOCK_TIME()` | view |

**Events:** `Deposited`, `RefundRequested`, `RefundWithdrawn`, `VoucherSettled`, `EarningsWithdrawn(provider, to, amount)`, `ServiceUpdated`, `ServiceRemoved(provider, appOwner)`, `KmsProviderBound(provider, appId, endorsingNode, epoch)`, `KmsProviderRepinned(provider, previousEpoch, epoch)`, `OwnershipTransferred`, `TappRegistryUpdated`.

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

See [`CLI.md`](../docs/CLI.md) for full flag reference.

```bash
# 1. Register the TEE-app trust root in TappRegistry (run with the provider wallet so it becomes the app owner)
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

**KMS provider** (node runs `PROVIDER_KEY_SOURCE=kms`): step 3 reads the provider address
and the node's endorsement from the node's `/api/info` instead of `--signer`:

```bash
OWNER_KEY=0x<owner-key> go run ./cmd/provider/ register --kms \
  --contract       0x<sandbox-serving-proxy> \
  --app-id         0g-sandbox-provider \
  --url            https://<sandbox-host> \
  --price-per-cpu  <neuron/cpu/min> \
  --price-per-mem  <neuron/memGB/min> \
  --fee            <neuron>
```

Fund the printed provider address once; it pays settlement gas from then on, across reboots.

---

## Design Notes

- **Proxy address never changes** — upgrading only replaces the implementation; the proxy address is the stable external-facing address
- **Open settlement** — `settleFeesWithTEE` can be called by anyone; the provider is identified by `v.provider` in the voucher, not `msg.sender`
- **Trust root delegation** — SandboxServing holds only commercial terms; TEE signer identity and user acknowledgement live in TappRegistry and are queried on every voucher verification
- **Provider IS the TEE signer (v2)** — every provider address (services key, voucher payee, balance bucket, earnings ledger) is the TEE signer address of one TappRegistry node. The signer key never leaves the enclave and dies with the machine, so all management (register/remove/withdraw) belongs to the appId's TappRegistry owner. One appId, many nodes: each signer has a fully isolated ledger — balances are deliberately NOT shared across nodes (independently-deployed billing proxies each run their own Redis reservation admission control and can't see each other's in-flight reservations; a shared balance would overcommit).
- **KMS provider** — the alternative to rotation: the provider is a key the app takes from the KMS (`GetSecretResource`, material hex(`provider`)), the same on every boot, so balances, nonces, the service and its gas survive reboots and rebuilds.
  - **Verification:** tapp-server ≥ 0.11.0 lists the key in each node's evidence (`runtime_data.kms_keys`), and an active node's endorsement registers it on chain.
  - **Settlement:** a KMS provider's vouchers settle while the app is still the TappRegistry instance it was bound to. The KMS gives the same key to whoever registers the appId after its last node is removed. The pin, kept even across `removeService`, keeps that registrant from the balances and earnings already held, including when users ack the new app.
  - **Prerequisite:** the KMS must run attested admission. Without it, the app owner could register a plain key as a node and take the provider key outside any TEE.
  - **One billing instance per appId:** every node of the app derives the same provider. Its balance bucket and voucher nonces would be shared by billing instances that cannot see each other's reservations or nonce counters, the same reason node-signer ledgers are isolated. When replacing a machine, stop the old billing before the new one starts.
- **Rotation** — a machine rebuild produces a new signer. Runbook: add the new node in TappRegistry (old + new coexist), drain the old signer's voucher queue, `rotate` the service entry (cmd/provider), remove the old node. Users move balances off the dead signer via the normal refund flow; `removeService` sweeps its earnings to the owner.
- **`appId` is set-once per signer** — once `addOrUpdateService` has bound a non-empty `appId` to a signer, subsequent calls must pass the same value. To bind a *different* `appId`, `removeService` first (user balances, pending refunds, and settled nonces are preserved — nonces stay put so old vouchers can't be replayed after a re-register).
