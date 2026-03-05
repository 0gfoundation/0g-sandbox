---
name: 0g-sandbox-billing
description: Use this skill when working on the 0G Sandbox Billing project — deploying/redeploying the service, checking logs, running tests, operating the settlement contract, or using the provider/user CLIs. Covers the full dev and ops workflow for this repo.
version: 1.0.0
author: 0G Labs
tags: [0g, billing, sandbox, daytona, tee, settlement, go, solidity]
repository: https://github.com/0gfoundation/0g-sandbox-billing
---

# 0G Sandbox Billing Skill

Full dev and ops workflow for the `0g-sandbox-billing` project.

## Environment

| Variable | Value |
|----------|-------|
| **Working dir** | `/root/0g-sandbox-billing` |
| **TEE server** | `http://47.236.111.154:50051` |
| **App ID** | `billing` |
| **TAPP_PRIVATE_KEY** | set via `export TAPP_PRIVATE_KEY="0x..."` |
| **Billing proxy** | `http://47.236.111.154:8080` |
| **Dashboard** | `http://47.236.111.154:8080/dashboard` |
| **Go binary** | `/usr/local/go/bin/go` (add to PATH) |

```bash
export PATH=$PATH:/usr/local/go/bin
export TAPP_PRIVATE_KEY="0xdb56d646e7de8081b9e4242fd41fec80976cbed8e495ed8598b9a3a8542fb8a3"
```

---

## Deploy / Redeploy

Full redeploy cycle (always rebuild image first — `billing:latest` is a local image):

```bash
# 1. Build image
docker build -t billing:latest .

# 2. Stop running app
tapp-cli stop-app -s http://47.236.111.154:50051 --app-id billing

# 3. Deploy
tapp-cli start-app -s http://47.236.111.154:50051 --app-id billing -f docker-compose.yml

# 4. Check task result
tapp-cli --server http://47.236.111.154:50051 get-task-status --task-id <task-id>

# 5. Verify all containers running
tapp-cli get-app-container-status -s http://47.236.111.154:50051 --app-id billing

# 6. Smoke test
curl -s http://47.236.111.154:8080/healthz
```

### What tapp-cli auto-uploads
Scans `volumes:` for `./`-prefixed paths — uploads `.env` and `config/dex/config.yaml`.

---

## Logs

```bash
# All services (last 100 lines)
tapp-cli get-app-logs -s http://47.236.111.154:50051 --app-id billing -n 100

# Specific service
tapp-cli get-app-logs -s http://47.236.111.154:50051 --app-id billing --service billing -n 50
tapp-cli get-app-logs -s http://47.236.111.154:50051 --app-id billing --service api -n 50
```

---

## Build & Test

```bash
export PATH=$PATH:/usr/local/go/bin

go build ./...           # compile
go test ./...            # unit + integration tests
go test ./internal/chain/... -v                    # chain integration tests
go test ./cmd/billing/ -v -run TestComponent       # component tests

make build-contracts     # compile Solidity via Docker Foundry
make abigen              # regenerate Go bindings from ABI
```

---

## Contract Operations

### Deploy (first time)
```bash
go run ./cmd/deploy/ \
  --rpc https://evmrpc-testnet.0g.ai \
  --key 0x<deployer-key> \
  --chain-id 16602 \
  --stake 0
# → set SETTLEMENT_CONTRACT=<proxy address> in .env
```

### Upgrade implementation
```bash
go run ./cmd/upgrade/ \
  --rpc https://evmrpc-testnet.0g.ai \
  --key 0x<deployer-key> \
  --chain-id 16602 \
  --proxy 0x24cD979DBd0Ae924a3f0c832a724CF4C58E5C210
# Proxy address unchanged — no .env update needed
```

Current addresses (0G Galileo, chainID 16602):
- **Proxy (SETTLEMENT_CONTRACT)**: `0x24cD979DBd0Ae924a3f0c832a724CF4C58E5C210`
- **Provider**: `0xB831371eb2703305f1d9F8542163633D0675CEd7`
- **TEE Signer**: `0x61BEb835D1935Eec8cC04efa2f4e2B3cC8B8B6E3`

---

## Provider CLI

```bash
PROVIDER_KEY=0x<key> go run ./cmd/provider/ init-service \
  --tee-signer 0x61BEb835D1935Eec8cC04efa2f4e2B3cC8B8B6E3 \
  --url http://47.236.111.154:8080
```

---

## User CLI

```bash
USER_KEY=0x<key> go run ./cmd/user/ balance \
  --provider 0xB831371eb2703305f1d9F8542163633D0675CEd7

USER_KEY=0x<key> go run ./cmd/user/ deposit --amount 0.01

USER_KEY=0x<key> go run ./cmd/user/ acknowledge \
  --provider 0xB831371eb2703305f1d9F8542163633D0675CEd7

USER_KEY=0x<key> go run ./cmd/user/ create --api http://47.236.111.154:8080
USER_KEY=0x<key> go run ./cmd/user/ list   --api http://47.236.111.154:8080
USER_KEY=0x<key> go run ./cmd/user/ stop   --api http://47.236.111.154:8080 --id <id>

# Remote execution inside a sandbox
USER_KEY=0x<key> go run ./cmd/user/ exec \
  --api http://47.236.111.154:8080 \
  --id <sandbox-id> \
  --cmd "python3 -c \"print('hello')\""

# Arbitrary toolbox API call
USER_KEY=0x<key> go run ./cmd/user/ toolbox \
  --api http://47.236.111.154:8080 \
  --id <sandbox-id> \
  --action files                          # GET /toolbox/files
USER_KEY=0x<key> go run ./cmd/user/ toolbox \
  --api http://47.236.111.154:8080 \
  --id <sandbox-id> \
  --action git/status                     # GET /toolbox/git/status
USER_KEY=0x<key> go run ./cmd/user/ toolbox \
  --api http://47.236.111.154:8080 \
  --id <sandbox-id> \
  --method POST \
  --action process/execute \
  --body '{"command":"echo hi","timeout":10}'
```

---

## Key Architecture Notes

- **Dual-key**: TEE key signs EIP-712 vouchers; `PROVIDER_PRIVATE_KEY` sends settlement txs (`msg.sender == provider`)
- **Beacon Proxy**: stable address `0x24cD...C210`; upgrade = deploy new impl + `beacon.upgradeTo()`
- **Auto-stop**: settler writes `stop:sandbox:<id>` on `INSUFFICIENT_BALANCE`; `runStopHandler` calls Daytona stop
- **Nonce**: per `(user, provider)` pair; seeded from chain on startup
- **Units**: all amounts in **neuron** (`1 0G = 10^18 neuron`)

### Redis Keys
| Key | Purpose |
|-----|---------|
| `billing:compute:<sandboxID>` | Open compute session |
| `billing:nonce:<user>:<provider>` | Voucher nonce counter |
| `voucher:<providerAddr>` | Pending voucher queue |
| `stop:sandbox:<sandboxID>` | Pending stop signal |

---

## HTTP Endpoints

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/healthz` | none | Liveness probe |
| GET | `/dashboard` | none | Operator dashboard |
| GET | `/api/providers` | none | On-chain service info for known providers |
| POST | `/api/sandbox` | EIP-191 | Create sandbox |
| GET | `/api/sandbox` | EIP-191 | List own sandboxes |
| GET | `/api/sandbox/:id` | EIP-191 | Get sandbox (403 if not owner) |
| POST | `/api/sandbox/:id/stop` | EIP-191 | Stop sandbox |
| DELETE | `/api/sandbox/:id` | EIP-191 | Delete sandbox |
| ANY | `/api/toolbox/:id/toolbox/*` | EIP-191 | Daytona toolbox — owner-checked transparent forward |

### Toolbox API (remote execution)
Path format: `/api/toolbox/{sandboxId}/toolbox/{action}` (double `/toolbox/`).

Available actions (from Daytona): `files`, `files/download`, `files/upload`, `files/find`, `git/status`, `git/clone`, `git/commit`, `git/push`, `git/pull`, `process/execute`, `process/pty`, `lsp/start`, `lsp/completions`, `project-dir`, `user-home-dir`, `computeruse/*`

Use the CLI for easy access (handles EIP-191 signing automatically):
```bash
# Run a command
USER_KEY=0x<key> go run ./cmd/user/ exec --api http://47.236.111.154:8080 --id <id> --cmd "echo hi"

# Arbitrary toolbox call
USER_KEY=0x<key> go run ./cmd/user/ toolbox --api http://47.236.111.154:8080 --id <id> --action files
USER_KEY=0x<key> go run ./cmd/user/ toolbox --api http://47.236.111.154:8080 --id <id> --action git/status
```

---

## Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| `PROVIDER_MISMATCH` on settlement | `msg.sender` ≠ provider address | Check `PROVIDER_PRIVATE_KEY` in `.env` |
| `INVALID_NONCE` on settlement | Redis nonce < chain nonce | Restart billing (reseeds from chain on startup) |
| Sandbox create → 403 | `maxCpuPerSandbox=0` on admin org | Check `ADMIN_MAX_CPU_PER_SANDBOX` in compose |
| TEE key fetch fails | tapp-daemon not reachable | Check `BACKEND_TAPP_IP`/`PORT`; use `MOCK_TEE=true` for dev |
| Settlement tx: insufficient funds | TEE signer has no gas | Fund `0x61BEb835...` with 0G on Galileo |
