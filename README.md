# 0G Sandbox Billing

A Go billing proxy server that sits in front of Daytona (sandbox runtime) and charges users
in 0G tokens via TEE-signed on-chain vouchers. Users deposit funds into a Solidity settlement
contract; the proxy creates EIP-712-signed vouchers and settles them on-chain periodically.

> 中文版：[README.zh.md](README.zh.md)

---

## Contract Architecture

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

## Key Separation: TEE Key vs Provider Key

The system uses two separate private keys with distinct roles:

| Key | Env var | Role |
|-----|---------|------|
| **TEE key** | fetched from tapp-daemon gRPC (or `MOCK_APP_PRIVATE_KEY`) | Signs EIP-712 vouchers off-chain |
| **Provider key** | `PROVIDER_PRIVATE_KEY` | Sends settlement transactions on-chain (`msg.sender == provider`) |

The settlement contract requires `msg.sender == v.provider` in `SettleFeesWithTEE`. The TEE key handles off-chain signing; the provider key pays gas for on-chain settlement.

To find the TEE signer address:
```bash
tapp-cli -s http://<server>:50051 get-app-key --app-id billing
# → Ethereum Address: 0x61beb835...
```

---

## Scripts

### Deploy (first time)

Deploys the full beacon-proxy stack in 3 steps:
1. SandboxServing implementation (no constructor args)
2. UpgradeableBeacon (impl, deployer)
3. BeaconProxy (beacon, initialize(providerStake))

```bash
go run ./cmd/deploy/ \
  --rpc      https://evmrpc-testnet.0g.ai \
  --key      0x<deployer-private-key> \
  --chain-id 16602 \
  --stake    0
```

Output:
```
Implementation : 0x...
Beacon         : 0x...
Proxy (stable) : 0x...   ← set this as SETTLEMENT_CONTRACT
```

Flags:

| Flag | Default | Description |
|------|---------|-------------|
| `--rpc` | `https://evmrpc-testnet.0g.ai` | EVM RPC endpoint |
| `--key` | (required) | Deployer private key (hex, with or without 0x) |
| `--chain-id` | `16602` | Chain ID |
| `--stake` | `0` | `providerStake` passed to `initialize()` (neuron) |

---

### Upgrade

Deploys a new implementation and points the beacon at it.
**Proxy address is unchanged** — no `.env` update needed, no user re-acknowledgement required.

```bash
go run ./cmd/upgrade/ \
  --rpc      https://evmrpc-testnet.0g.ai \
  --key      0x<deployer-private-key> \
  --chain-id 16602 \
  --proxy    0x<proxy-address>
```

Flags:

| Flag | Default | Description |
|------|---------|-------------|
| `--rpc` | `https://evmrpc-testnet.0g.ai` | EVM RPC endpoint |
| `--key` | (required) | Deployer/owner private key |
| `--chain-id` | `16602` | Chain ID |
| `--proxy` | (required*) | BeaconProxy address — beacon resolved automatically |
| `--beacon` | (required*) | UpgradeableBeacon address (alternative to `--proxy`) |

\* Provide either `--proxy` or `--beacon`.

---

### Verify

Verifies all three contracts on the block explorer.
**Only the proxy address is needed** — beacon and impl are resolved automatically from chain.

```bash
./scripts/verify-contracts.sh --proxy 0x<proxy-address>
```

---

### Provider Registration

After deploying the contract, register the service on-chain using `cmd/provider`.
See [`CLI.md`](CLI.md) for full details.

```bash
# Get TEE signer address from tapp-daemon
tapp-cli -s http://<server>:50051 get-app-key --app-id billing
# → Ethereum Address: 0x61beb835...

PROVIDER_KEY=0x<provider-key> go run ./cmd/provider/ init-service \
  --tee-signer <TEE-signer-address> \
  --url        http://<billing-proxy>:8080
```

Then set `PROVIDER_ADDRESS` and `PROVIDER_PRIVATE_KEY` in `.env`.

---

## Running the Server

Copy `.env.example` to `.env`, fill in the required values:

```bash
cp .env.example .env
# edit .env
go run ./cmd/billing/
```

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `DAYTONA_API_URL` | (required) | Daytona API endpoint (internal; never expose publicly) |
| `DAYTONA_ADMIN_KEY` | (required) | Daytona admin key |
| `SETTLEMENT_CONTRACT` | (required) | BeaconProxy address |
| `RPC_URL` | (required) | EVM RPC endpoint |
| `CHAIN_ID` | (required) | Chain ID (e.g. 16602) |
| `PROVIDER_ADDRESS` | (required) | Provider's Ethereum address |
| `PROVIDER_PRIVATE_KEY` | (required) | Provider's private key — signs settlement transactions |
| `REDIS_ADDR` | `redis:6379` | Redis address |
| `COMPUTE_PRICE_PER_SEC` | `16667` | neuron/sec per sandbox (≈ 1M neuron/min) |
| `CREATE_FEE` | `5000000` | neuron flat fee per sandbox creation |
| `VOUCHER_INTERVAL_SEC` | `3600` | voucher flush interval (seconds) |
| `PORT` | `8080` | HTTP server port |
| `MOCK_TEE` | — | Set to `true` for local dev (uses `MOCK_APP_PRIVATE_KEY` instead of TDX gRPC) |
| `MOCK_APP_PRIVATE_KEY` | — | Hex private key used when `MOCK_TEE=true` |

### Docker Compose

```bash
docker compose up
```

The `billing` service image is pinned to a specific SHA256 digest in `docker-compose.yml`.
Update the digest after rebuilding: `docker inspect billing:latest --format '{{.Id}}'`.

---

## User Operations

Users interact with the system via:
1. **On-chain**: deposit funds and acknowledge the TEE signer
2. **HTTP API**: create, list, stop, and delete sandboxes (authenticated via EIP-191 signatures)

See [`CLI.md`](CLI.md) for the full `cmd/user` reference and onboarding flow.

**Minimum balance to create a sandbox:**
```
minBalance = CREATE_FEE + COMPUTE_PRICE_PER_SEC × VOUCHER_INTERVAL_SEC
           = 5,000,000 + 16,667 × 3,600 ≈ 65,001,200 neuron ≈ 0.000065 0G
```

Sandboxes are automatically stopped when the user's balance is exhausted.

---

## Development

```bash
# Build contracts (requires Docker)
make build-contracts

# Regenerate Go bindings
make abigen

# Run all tests
go test ./...

# Run Solidity tests (requires Docker)
make test-contracts
```

See [`TESTING.md`](TESTING.md) for unit, integration, and E2E test details.
