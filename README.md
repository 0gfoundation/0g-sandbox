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

### Option A — Install the plugin (recommended, no cloning needed)

```
/plugin marketplace add 0gfoundation/0g-sandbox
/plugin install 0g-private-sandbox@0g-sandbox
/reload-plugins
```

Then invoke the skill anytime:

```
/0g-private-sandbox
```

### Option B — Clone the repo

```bash
git clone https://github.com/0gfoundation/0g-sandbox.git
cd 0g-sandbox
claude
```

Then just describe what you want in plain language, for example:

> "I want to use 0G private sandbox to play with OpenClaw"

---

Claude will walk you through the rest. When asked for configuration details, the only piece
of information you need is the hosted testnet broker:

| Item | Value |
|------|-------|
| **Testnet broker** | `https://private-sandbox-testnet.0g.ai` |

Claude (or the CLI) reads the rest — `contract_address`, `rpc_url` (`https://evmrpc-testnet.0g.ai`),
and `chain_id` (`16602`) — from the broker's `GET /api/info`, so there's nothing else to copy by hand.

Claude will handle onboarding, wallet setup, deposit, sandbox creation, and OpenClaw
configuration automatically.

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

## Contract Deployment

See [`CONTRACTS.md`](contracts/README.md) for architecture, deploy/upgrade/verify instructions, and contract addresses.

---

## Running the Server

Copy `docker/sandbox/.env.dev` to `.env`, fill in the required values:

```bash
cp docker/sandbox/.env.dev .env
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
| `BACKEND_APP_NAME` | (required) | tapp app-id = TappRegistry appId; the app owner (standing admin) and provider identity (TEE signer) are both derived from it at runtime |
| `REDIS_ADDR` | `redis:6379` | Redis address |
| `COMPUTE_PRICE_PER_SEC` | `16667` | neuron/sec fallback (used only when per-resource on-chain pricing is not set) |
| `CREATE_FEE` | `5000000` | neuron flat fee fallback (on-chain value takes priority after provider registration) |
| `VOUCHER_INTERVAL_SEC` | `60` | voucher flush interval (seconds) |
| `SSH_GATEWAY_HOST` | — | SSH gateway host rewritten in SSH commands (e.g. `<provider-ip>`); falls back to browser hostname if unset |
| `PROXY_DOMAIN` | — | Domain template for sandbox service-port URLs: `http://<port>-<id>.<PROXY_DOMAIN>/<path>`. Use `<your-ip>.nip.io:4000` (nip.io) or `sandbox.yourdomain.com` (real domain with nginx). |
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

### Tapp Deployment (Production)

The billing server runs inside a 0G Tapp TEE enclave. Deploy via `tapp-cli`:

```bash
# Build the image
docker build --target sandbox -t 0g-sandbox:dev .

# Deploy (or redeploy after changes)
tapp-cli -s http://<tapp-server>:50051 stop-app  --app-id 0g-sandbox
tapp-cli -s http://<tapp-server>:50051 start-app --app-id 0g-sandbox -f docker-compose.yml

# Check container status
tapp-cli -s http://<tapp-server>:50051 get-app-container-status --app-id 0g-sandbox

# Tail logs
tapp-cli -s http://<tapp-server>:50051 get-app-logs --app-id 0g-sandbox -n 100
```

The TEE key is automatically generated and managed by the tapp-daemon. The provider
registers an `appId` rather than a raw signer address; TappRegistry stores the active
TEE node set and the billing proxy reads it on every voucher verification.

```bash
# Inspect the app's current trust root (compose hash, image hashes, TEE nodes)
tapp-cli -s http://<tapp-server>:50051 get-app-info --app-id 0g-sandbox
```

> If the TEE key rotates (redeploy, manual `addNode` / `removeNode`), TappRegistry
> bumps `ackVersion(appId)` and every prior user acknowledgement becomes stale.
> Users re-acknowledge via `cmd/user acknowledge` before further vouchers settle.

---

## User Operations

Users interact with the system through one of two surfaces, both backed by the
same on-chain `(deposit + ack appId)` and EIP-191 sandbox API model:

- **Broker web frontend** (`GET /` on the broker, served from `web/user.html`) —
  recommended path. Connect a web3 wallet, the page reads broker's own `appId`
  from `GET /api/info` and prompts to `tap.acknowledgeApp(brokerAppId)`, then
  shows the indexed provider marketplace. Picking a provider and clicking
  `+ new` triggers `tap.acknowledgeApp(providerAppId)` and the sandbox create
  flow goes through the broker's `/proxy/:provider/*` reverse proxy. See
  [Broker: User Entry Portal](#broker-user-entry-portal) below.
- **`cmd/user` CLI** — same operations from the terminal, useful for scripted
  / CI flows. Documented in [`CLI.md`](CLI.md).

**Minimum balance to create a sandbox:**
```
minBalance = CREATE_FEE + COMPUTE_PRICE_PER_SEC × VOUCHER_INTERVAL_SEC
```
Exact values depend on the provider's on-chain registration. Check `GET /api/info` for the live
`create_fee`, `compute_price_per_sec`, and `min_balance` for the provider you're using.

Sandboxes are automatically stopped when the user's balance is exhausted.

---

## Broker: User Entry Portal

The **Broker** is a TEE-hosted service that acts as the entry point for users. Rather than
connecting directly to a provider, users connect to the Broker, which handles provider
discovery, request routing, and automatic balance top-ups on their behalf.

```
User ──► Broker (TEE)
              │
              ├── Provider Marketplace   index provider URLs from chain events
              ├── Reverse Proxy          route /proxy/:addr/* → provider backend (no CORS)
              ├── Balance Monitor        poll on-chain balance every T_monitor seconds
              └── Payment Layer client   call deposit(user, provider, amount) when balance low
```

### Why TEE-hosted?

The Broker signs Payment Layer requests with its TEE key. This lets the Payment Layer
verify that top-up instructions came from a legitimate, unmodified Broker — not a spoofed
caller. Trust is rooted in the TEE hardware, not in operator configuration.

### Provider Marketplace

The Broker indexes registered providers from chain events (via `cmd/broker` → `internal/indexer`)
and exposes them at `GET /api/providers`. Each entry includes the provider's `app_id`
(the appId bound to its SandboxServing service) so the user frontend can route an ack
straight to TappRegistry without an extra chain read.

### Web Frontend (`GET /`)

The broker embeds `web/user.html` and serves it at `/`. This is the production
entry point for end users — no CLI install required.

The full UX flow:

1. Page loads → `GET /api/info` returns `{contract_address, tapp_registry, app_id, chain_id, rpc_url}`.
   `app_id` is the broker's own tapp app-id (read from `BACKEND_APP_NAME`);
   `tapp_registry` is the TappRegistry contract address. Both are needed to
   call `tap.acknowledgeApp` from the browser.
2. User clicks **Connect Wallet** → broker prompts with the on-chain trust root
   (composeHash, image hashes, active TEE nodes + their teeUrl, ackVersion)
   pulled live from `tap.getAppInfo(brokerAppId)` and asks them to sign
   `tap.acknowledgeApp(brokerAppId)`. The modal includes a `tapp-cli get-evidence`
   pointer for users who want to remote-attest the TEE before signing.
3. `GET /api/providers` populates the marketplace; each provider card shows
   address, app_id, prices, balance, and the user's sandbox list (fetched
   through `/proxy/:provider/api/sandbox_list?wallet=`).
4. **+ new** on a provider card runs the same trust-root prompt for that
   provider's appId, then opens the create modal. Without ack, the create
   step aborts cleanly — no sandbox is opened until both acks succeed.
5. All `signedFetch` calls (create, stop, delete, etc.) go through the broker's
   `/proxy/:providerAddr/*` reverse proxy back to the sandbox provider, so the
   browser stays on the broker origin (no CORS to manage) and the broker can
   observe the request stream for balance monitoring.

### Balance Monitoring & Automatic Top-up

When a sandbox starts or restarts, the billing proxy registers the session with the Broker
(`POST /api/session`). The Broker tracks each session's CPU/memory and computes the user's
total burn rate. When on-chain balance drops below the threshold, it calls the Payment
Layer to top up automatically.

#### Parameter tuning

Derived from **T_react** — time from alert to funds landing on-chain:

| Parameter | Formula | Automatic (T_react ≈ 60s) | Manual (T_react = 10 min) |
|-----------|---------|--------------------------|--------------------------|
| `BROKER_MONITOR_INTERVAL_SEC` | `VOUCHER_INTERVAL_SEC / 2` | **30s** | 30s |
| `BROKER_THRESHOLD_INTERVALS` | `T_react / T_monitor` | **3** | 20 |
| `BROKER_TOPUP_INTERVALS` | `THRESHOLD_INTERVALS × 2` | **6** | 40 |

- **Threshold** = burn_rate × interval × `THRESHOLD_INTERVALS` — triggers top-up request
- **Topup amount** = burn_rate × interval × `TOPUP_INTERVALS` — target balance after refill
- On-chain latency for automatic top-up ≈ 30–60s (Payment Layer queue + ~2 block confirmations at 6s/block)

> **Log buffer note**: tapp-cli has a fixed log buffer (~50 lines). At 6s intervals, the
> buffer fills in ~2 min during alert state and `get-app-logs` returns stale data. Keep
> `BROKER_MONITOR_INTERVAL_SEC ≥ 30` to avoid this.

### Environment variables

| Variable | Default | Description |
|----------|---------|-------------|
| `SETTLEMENT_CONTRACT` | (required) | BeaconProxy address (same as billing proxy) |
| `TAPP_REGISTRY` | (required) | TappRegistry contract address — broker verifies session signatures against active TEE nodes per provider's `appId` |
| `BACKEND_APP_NAME` | (required) | Broker's own tapp app-id — used to fetch its TEE key and surfaced via `GET /api/info` so the user frontend can ack the broker in TappRegistry |
| `RPC_URL` | `https://evmrpc-testnet.0g.ai` | EVM RPC endpoint |
| `CHAIN_ID` | `16602` | Chain ID |
| `BROKER_PORT` | `8082` | HTTP port |
| `BROKER_MONITOR_INTERVAL_SEC` | `300` | Balance poll interval (seconds) |
| `BROKER_THRESHOLD_INTERVALS` | `2` | Alert when balance < burn × interval × N |
| `BROKER_TOPUP_INTERVALS` | `3` | Top-up to burn × interval × N neuron |
| `PAYMENT_LAYER_URL` | — | Payment Layer HTTP endpoint; empty = log-only (noop) |
| `BROKER_DEBUG` | `false` | Expose `GET /api/monitor` to inspect live sessions |

### Tapp Deployment

The Broker runs as a separate tapp app (`0g-broker`) alongside `0g-sandbox`:

```bash
# Build (broker stage of the multi-stage Dockerfile)
docker build --target broker -t 0g-broker:latest .
docker push <registry>/0g-broker:latest

# Deploy
tapp-cli -s http://<tapp-server>:50051 stop-app  --app-id 0g-broker
tapp-cli -s http://<tapp-server>:50051 start-app --app-id 0g-broker \
  -f docker/broker/docker-compose.yml

# Check logs
tapp-cli -s http://<tapp-server>:50051 get-app-logs --app-id 0g-broker --service broker -n 50
```

The billing proxy connects to the Broker via `BROKER_URL=http://<broker-host>:8082`.

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

See [`TESTING.md`](docs/TESTING.md) for unit, integration, and E2E test details.
