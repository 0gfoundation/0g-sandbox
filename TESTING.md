# Testing Guide

> 中文版：[TESTING.zh.md](TESTING.zh.md)

---

## Test Levels Overview

| Level | Command | External deps | Duration |
|---|---|---|---|
| Unit tests | `go test ./...` | None | < 5 s |
| Chain integration | `go test ./internal/chain/...` | `make build-contracts` first | < 5 s |
| Component tests | `go test ./cmd/billing/` | `make build-contracts` first | < 30 s |
| E2E tests | `go test -tags e2e ./cmd/billing/` | Live chain + Redis + Daytona | Minutes |

---

## 1. Unit Tests

No external dependencies. Uses httptest, miniredis, and go-ethereum simulated backend entirely in-process.

```bash
go test ./...
```

| Package | What is tested |
|---|---|
| `internal/auth` | EIP-191 signature verification, nonce replay protection, expiry |
| `internal/billing` | OnCreate/OnStart/OnStop/OnDelete handlers, voucher generation, nonce increment |
| `internal/daytona` | HTTP client, auth headers, error handling |
| `internal/proxy` | Request interception, owner label injection/filtering, balance check |
| `internal/settler` | Settlement status handling, persistStop, DLQ |
| `internal/voucher` | EIP-712 signing, usageHash construction |

---

## 2. Chain Integration Tests

Uses the go-ethereum simulated backend (no network). Requires compiled contract artifacts.

**Prerequisite**

```bash
make build-contracts   # Compiles Solidity via Docker, outputs to contracts/out/
```

**Run**

```bash
go test ./internal/chain/... -v
```

Deploys the full impl + beacon + proxy stack on a simulated chain and verifies
`GetLastNonce`, `SettleFeesWithTEE`, `GetAccount`, and related on-chain operations.

---

## 3. Component Tests

Runs the complete billing pipeline (auth → proxy → billing → settler) in-process
using a simulated chain + miniredis + mock Daytona httptest server. No real external services.

**Prerequisite**

```bash
make build-contracts   # Component tests need contract bytecode for deployment
```

**Run**

```bash
go test ./cmd/billing/ -v -run TestComponent
```

| Test | Scenario |
|---|---|
| `TestComponent_HappyPath` | Create sandbox → create-fee settled → on-chain lastNonce == 1 |
| `TestComponent_InsufficientBalance` | Zero balance → InsufficientBalance → Daytona auto-stop |
| `TestComponent_OwnershipFiltering` | Owner label injection, list filtering, cross-user 403 |

---

## 4. End-to-End (E2E) Tests

Connects to the real 0G Galileo testnet, real Redis, and real Daytona instance.
Requires the `-tags e2e` build tag.

### Prerequisites

**1. Account setup**

Before running the E2E test the provider's `appId` must be registered in TappRegistry
and the SandboxServing service must be bound to that `appId`, and the test wallet must
have deposited funds and acknowledged the `appId`:

```
tapp.registerApp(appId, ...)        # register the TEE-app trust root
sandbox.addOrUpdateService(...)     # bind URL/prices to appId
sandbox.deposit(user, provider, ...)# fund the user account (≥ 0.1 0G recommended)
tapp.acknowledgeApp(appId)          # acknowledge under the active TEE node set
```

Run the ceremony from the CLI:

```bash
# Provider side
PROVIDER_KEY=0x<provider-key> go run ./cmd/provider/ register \
  --api      http://<billing-host>:8080 \
  --app-id   <appId> \
  --price-per-cpu <neuron/cpu/min> \
  --price-per-mem <neuron/memGB/min> \
  --create-fee    <neuron>

# User side
USER_KEY=0x<user-key> go run ./cmd/user/ deposit \
  --provider 0x<provider-address> \
  --amount   0.1
USER_KEY=0x<user-key> go run ./cmd/user/ acknowledge \
  --provider 0x<provider-address> \
  --tapp     0x<tapp-registry-address>
```

**2. Local services**
- Redis at `localhost:6379` (override with `REDIS_ADDR`)
- Daytona at `localhost:3000` (override with `INTEGRATION_DAYTONA_URL`)

### Environment Variables

| Variable | Default | Description |
|---|---|---|
| `MOCK_TEE` | — | **Required.** Set to `true` to use mock TEE mode |
| `MOCK_APP_PRIVATE_KEY` | — | **Required.** TEE private key (`0x` prefix optional) |
| `INTEGRATION_RPC_URL` | `https://evmrpc-testnet.0g.ai` | Chain RPC endpoint |
| `INTEGRATION_CONTRACT` | `0x24cD979DBd0Ae924a3f0c832a724CF4C58E5C210` | Settlement contract address |
| `INTEGRATION_DAYTONA_URL` | `http://localhost:3000` | Daytona API endpoint |
| `INTEGRATION_DAYTONA_KEY` | `daytona_admin_key` | Daytona admin key |
| `REDIS_ADDR` | `localhost:6379` | Redis address |
| `REDIS_PASSWORD` | — | Redis password (optional) |
| `INTEGRATION_USER_KEY` | same as `MOCK_APP_PRIVATE_KEY` | User wallet key (defaults to TEE key) |
| `INTEGRATION_VOUCHER_INTERVAL_SEC` | `5` | Voucher generation interval (seconds) |
| `INTEGRATION_CREATE_FEE` | `1` | Create fee (neuron) |
| `INTEGRATION_COMPUTE_PRICE` | `1` | Compute price (neuron/sec) |

### Run

```bash
MOCK_TEE=true \
MOCK_APP_PRIVATE_KEY=0xYOUR_PRIVATE_KEY_HERE \
go test -v -tags e2e ./cmd/billing/ -run TestE2E -timeout 10m
```

Run a single test:

```bash
MOCK_TEE=true \
MOCK_APP_PRIVATE_KEY=0xYOUR_PRIVATE_KEY_HERE \
go test -v -tags e2e ./cmd/billing/ -run TestE2E_AutoStopInsufficientBalance -timeout 10m
```

Custom billing parameters:

```bash
MOCK_TEE=true \
MOCK_APP_PRIVATE_KEY=0xYOUR_PRIVATE_KEY_HERE \
INTEGRATION_VOUCHER_INTERVAL_SEC=10 \
INTEGRATION_CREATE_FEE=100 \
INTEGRATION_COMPUTE_PRICE=50 \
go test -v -tags e2e ./cmd/billing/ -run TestE2E -timeout 10m
```

### E2E Test Cases

| Test | Scenario | Expected |
|---|---|---|
| `TestE2E_CreateFeeSettled` | Create a sandbox | On-chain nonce +1 (create-fee voucher) |
| `TestE2E_ComputeFeeSettled` | Run sandbox for 6 voucher intervals then stop | Nonce advances by ≥ 8 (create-fee + periodic compute × 6 + OnStop); exact delta depends on generator timing |
| `TestE2E_InsufficientBalance` | Wallet with no deposit tries to create sandbox | Proxy returns HTTP 402 before Daytona is called |
| `TestE2E_AutoStopInsufficientBalance` | Ephemeral wallet funded for exactly one compute period | `stop:sandbox:<id>` key appears then is cleaned up; sandbox stopped via Daytona |

> `TestMain` starts the shared environment (settler + generator + proxy) once.
> All `TestE2E_*` functions share it and run sequentially — no restart between tests.
>
> **Nonce delta note:** `TestE2E_ComputeFeeSettled` uses `t.Cleanup` to stop the
> sandbox after the test body returns. The resulting OnStop compute voucher settles
> asynchronously and may appear within the next test's observation window. This is
> expected — on-chain settlement is async and nonces accumulate correctly across tests.

---

## Appendix: Daytona Client Integration Tests

`internal/daytona` includes 3 real-Daytona tests that run as part of `go test ./...`.
If Daytona is unreachable (`GET /api/health` fails), these skip automatically — safe for CI.

```bash
# Override Daytona address (default localhost:3000)
DAYTONA_API_URL=http://localhost:3000 \
DAYTONA_ADMIN_KEY=daytona_admin_key \
go test ./internal/daytona/... -v -run TestIntegration
```
