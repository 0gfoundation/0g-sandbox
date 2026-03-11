# 0G Sandbox

Private, isolated sandboxes for vibe coding — powered by 0G Network.

> 中文版：[README.zh.md](README.zh.md)

---

## Problem

Vibe coding has two conflicting requirements:

1. **Local environments aren't isolated enough.** Running untrusted or experimental code locally
   risks polluting the development environment, leaking credentials, or causing irreversible
   side effects. A fully isolated remote sandbox is needed.

2. **Remote servers are controlled by others.** Renting a cloud VM solves isolation, but the
   host provider can inspect or tamper with the code and data running inside. The sandbox
   environment itself cannot be trusted.

## Solution

**0G Sandbox** combines [0G Tapp](https://0g.ai) (TEE-based trusted execution) with
[Daytona](https://daytona.io) (sandbox runtime) to satisfy both requirements simultaneously:

- **Isolation**: each sandbox is a fully containerized Daytona workspace, isolated from the
  user's local machine and from other users' sandboxes.
- **Confidentiality**: the billing proxy and its TEE signing key run inside a hardware TEE
  enclave (TDX) managed by 0G Tapp. The host cannot inspect the workload or forge vouchers.
  Critically, **the provider never sees the user's code** — sandbox workloads run inside the
  TEE and are opaque to the infrastructure operator.
- **Trustless billing**: users deposit funds into a Solidity contract on 0G Network. Compute
  fees are settled via EIP-712 vouchers signed by the TEE key — no trusted intermediary needed.

### Why not just rent a cloud TDX server?

A cloud TDX instance only secures the execution side. In a vibe coding workflow, the attack
surface is larger: your **prompts, context, and intermediate outputs** all pass through the AI
model, and cloud providers offer no confidentiality guarantee for inference.

0G Sandbox is designed to compose with [0G Compute](https://0g.ai) (TEE-based AI inference),
enabling a fully confidential vibe coding pipeline:

```
Prompt ──► 0G Compute (AI inference in TEE)
                │
                ▼ generated code
           0G Sandbox (execution in TEE)
                │
                ▼ results
           settled on 0G Network (trustless billing)
```

At every step — what you write, what the AI generates, what the code produces — the data
is invisible to any operator, including 0G itself. This end-to-end guarantee is something a
cloud provider, as a single point of trust, fundamentally cannot offer.

---

## Quickstart: OpenClaw in a Private Sandbox

The fastest way to spin up an [OpenClaw](https://github.com/0gfoundation/open-claw) AI gateway
inside a 0G Private Sandbox is to let Claude do the work for you.

**Prerequisites**: [Claude Code](https://claude.ai/claude-code) installed.

```bash
# 1. Clone this repo
git clone https://github.com/0gfoundation/0g-sandbox.git
cd 0g-sandbox

# 2. Start Claude Code
claude
```

Then just describe what you want in plain language, for example:

> "I want to use 0G private sandbox to play with OpenClaw"

Claude will walk you through the rest. When asked for configuration details, the key piece
of information you need is:

| Item | Value |
|------|-------|
| **Testnet contract** | `0xd7e0CD227e602FedBb93c36B1F5bf415398508a4` |
| **RPC** | `https://evmrpc-testnet.0g.ai` |
| **Chain ID** | `16602` |

Claude will handle onboarding, wallet setup, deposit, sandbox creation, and OpenClaw
configuration automatically.

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

## TEE Key

The TEE key is the single signing key for the entire system — it both signs EIP-712 vouchers
off-chain and sends settlement transactions on-chain. It is fetched automatically from the
tapp-daemon gRPC at startup (or from `MOCK_APP_PRIVATE_KEY` in dev mode).

The TEE address needs a small amount of 0G for gas to submit settlement transactions.

To find the TEE signer address:
```bash
tapp-cli -s http://<server>:50051 get-app-key --app-id 0g-sandbox
# → Ethereum Address: 0x...  ← fund this address with 0G for gas
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
tapp-cli -s http://<server>:50051 get-app-key --app-id 0g-sandbox
# → Ethereum Address: 0x61beb835...

PROVIDER_KEY=0x<provider-key> go run ./cmd/provider/ init-service \
  --tee-signer <TEE-signer-address> \
  --url        http://<billing-proxy>:8080
```

Then set `PROVIDER_ADDRESS` in `.env` and fund the TEE address with 0G for gas.

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
| `REDIS_ADDR` | `redis:6379` | Redis address |
| `COMPUTE_PRICE_PER_SEC` | `16667` | neuron/sec per sandbox (≈ 1M neuron/min) |
| `CREATE_FEE` | `5000000` | neuron flat fee per sandbox creation |
| `VOUCHER_INTERVAL_SEC` | `60` | voucher flush interval (seconds) |
| `SSH_GATEWAY_HOST` | — | SSH gateway host rewritten in SSH commands (e.g. `43.106.147.28`); falls back to browser hostname if unset |
| `PORT` | `8080` | HTTP server port |
| `MOCK_TEE` | — | Set to `true` for local dev (uses `MOCK_APP_PRIVATE_KEY` instead of TDX gRPC) |
| `MOCK_APP_PRIVATE_KEY` | — | Hex private key used when `MOCK_TEE=true` |

### SSH Gateway Key Generation

The SSH gateway requires two ed25519 key pairs stored as base64 in `.env`.
Generate them once per deployment:

```bash
# Generate the gateway private key (used by the SSH gateway service)
ssh-keygen -t ed25519 -C "daytona-gateway" -f /tmp/daytona_gw -N ""
DAYTONA_SSH_GATEWAY_PRIVATE_KEY=$(base64 -w0 < /tmp/daytona_gw)

# Generate the host key (identifies the server to SSH clients)
ssh-keygen -t ed25519 -C "daytona-gateway-host" -f /tmp/daytona_host -N ""
DAYTONA_SSH_GATEWAY_HOST_KEY=$(base64 -w0 < /tmp/daytona_host)

# Print values to paste into .env
echo "DAYTONA_SSH_GATEWAY_PRIVATE_KEY=$DAYTONA_SSH_GATEWAY_PRIVATE_KEY"
echo "DAYTONA_SSH_GATEWAY_HOST_KEY=$DAYTONA_SSH_GATEWAY_HOST_KEY"

# Clean up temp files
rm -f /tmp/daytona_gw /tmp/daytona_gw.pub /tmp/daytona_host /tmp/daytona_host.pub
```

These values must never be committed to source control (`.gitignore` covers `*.key`/`*.pem`;
the base64 values live only in `.env`).

### Docker Compose

```bash
docker compose up
```

The `billing` service image is pinned to a specific SHA256 digest in `docker-compose.yml`.
Update the digest after rebuilding: `docker inspect billing:latest --format '{{.Id}}'`.

### Tapp Deployment (Production)

The billing server runs inside a 0G Tapp TEE enclave. Deploy via `tapp-cli`:

```bash
# Build the image
docker build -t billing:latest .

# Deploy (or redeploy after changes)
tapp-cli -s http://<tapp-server>:50051 stop-app  --app-id 0g-sandbox
tapp-cli -s http://<tapp-server>:50051 start-app --app-id 0g-sandbox -f docker-compose.yml

# Check container status
tapp-cli -s http://<tapp-server>:50051 get-app-container-status --app-id 0g-sandbox

# Tail logs
tapp-cli -s http://<tapp-server>:50051 get-app-logs --app-id 0g-sandbox -n 100
```

The TEE key is automatically generated and managed by the tapp-daemon. Retrieve the
Ethereum address for provider registration:

```bash
tapp-cli -s http://<tapp-server>:50051 get-app-key --app-id 0g-sandbox
# → Ethereum Address: 0x...  ← use as --tee-signer when registering the provider
```

> **Note**: if the app is redeployed and the TEE key changes, re-register the provider
> with the new signer address (`cmd/provider register --tee-signer <new-addr>`).
> This increments `signerVersion` — all users must re-acknowledge before vouchers settle.

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
