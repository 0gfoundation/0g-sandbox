---
name: 0g-sandbox
description: Use this skill when working on the 0G Sandbox project — deploying/redeploying the service, checking logs, running tests, operating the settlement contract, or using the provider/user CLIs. Covers the full dev and ops workflow for this repo.
version: 1.0.0
author: 0G Labs
tags: [0g, billing, sandbox, daytona, tee, settlement, go, solidity]
repository: https://github.com/0gfoundation/0g-sandbox
---

# 0G Sandbox Skill

---

## Quick Reference

| | Value |
|---|---|
| **Billing proxy** | `http://47.236.111.154:8080` |
| **Dashboard** | `http://47.236.111.154:8080/dashboard` |
| **Contract (SETTLEMENT_CONTRACT)** | `0x24cD979DBd0Ae924a3f0c832a724CF4C58E5C210` |
| **Provider** | `0xB831371eb2703305f1d9F8542163633D0675CEd7` |
| **TEE Signer** | `0x61BEb835D1935Eec8cC04efa2f4e2B3cC8B8B6E3` |
| **Go binary** | `/usr/local/go/bin/go` |

```bash
export PATH=$PATH:/usr/local/go/bin
export API=http://47.236.111.154:8080
export PROVIDER=0xB831371eb2703305f1d9F8542163633D0675CEd7
export USER_KEY=0x<your-private-key>
```

---

## Agent: Guiding a New User (Vibe Coding Onboarding)

When a user wants to use private sandbox for vibe coding, follow this flow:

### A. List available providers

Always start by listing providers so the user can choose:
```bash
go run ./cmd/user/ providers --api http://47.236.111.154:8080
```
Shows each provider's address, URL, create fee, and compute price. If only one provider exists, use it automatically and inform the user. If multiple exist, ask the user to pick one.

Note the chosen provider's **address** — it's needed for `balance`, `acknowledge`, and `deposit`.

### B. Check if they have a wallet

**If no wallet** — generate one:
```bash
python3 -c "
import secrets
from eth_keys import keys
pk = keys.PrivateKey(bytes.fromhex(secrets.token_hex(32)))
print('Private Key:', '0x' + pk.to_hex())
print('Address:    ', pk.public_key.to_checksum_address())
"
```
Tell the user to save their private key securely. It will not be shown again.

**If they have a wallet** — ask for their address and check balance (no private key needed):
```bash
go run ./cmd/user/ balance \
  --address 0x<wallet-address> \
  --provider 0xB831371eb2703305f1d9F8542163633D0675CEd7
```
Output shows two balances:
- **Wallet balance** — native 0G in the wallet (used for gas)
- **Contract balance** — 0G deposited into sandbox contract (used for sandbox fees)

### B. Ensure sufficient balance

Minimum required: **0.12 0G** (createFee 0.06 + 1 interval 0.06).
Recommended deposit: **1–10 0G** for extended use.

If balance is 0 or insufficient:
1. User must transfer 0G to their wallet address first (keep ~0.1 0G for gas)
2. Then deposit into the contract:
```bash
USER_KEY=0x<key> go run ./cmd/user/ deposit --provider 0xB831371eb2703305f1d9F8542163633D0675CEd7 --amount <amount>
```
3. Check balance again to confirm.

### C. Acknowledge TEE signer (first time only)

```bash
USER_KEY=0x<key> go run ./cmd/user/ acknowledge \
  --provider 0xB831371eb2703305f1d9F8542163633D0675CEd7
```
This is a one-time on-chain transaction. Must be done before creating any sandbox.

### D. Choose snapshot (optional)

First list available snapshots:
```bash
go run ./cmd/user/ snapshots --api http://47.236.111.154:8080
```
Ask the user: **"是否需要预装特定环境（如 Rust、Python 等）？如果有合适的快照，建议使用。"**

- If a suitable snapshot exists (state: active), use `--snapshot <name>` when creating.
- If no snapshot fits, create without `--snapshot` (default Ubuntu-based image).

### E. Create sandbox

```bash
# Without snapshot (default image)
USER_KEY=0x<key> go run ./cmd/user/ create --api http://47.236.111.154:8080

# With snapshot
USER_KEY=0x<key> go run ./cmd/user/ create --api http://47.236.111.154:8080 --snapshot <name>
```
Note the sandbox ID from the response. Billing starts immediately.

### F. Set up vibe coding

Ask the user: **"Do you want to sync local code, or work purely in the remote sandbox?"**

**Local AI + Remote Execution (Mode A):**

Step 1 — install rsync in sandbox (one-time):
```bash
USER_KEY=0x<key> go run ./cmd/user/ exec --api http://47.236.111.154:8080 --id <id> \
  --cmd "/bin/sh -c 'sudo apt-get update -qq && sudo apt-get install -y rsync'"
```

Step 2 — agent asks user for local project directory, then **starts the watch process itself**:

Ask: "你的本地项目目录是什么路径？同步到 sandbox 的哪个目录？（默认 /home/daytona/project）"

Then run (agent executes this, not the user):
```bash
SSH_CMD=$(USER_KEY=0x<key> go run ./cmd/user/ ssh-access \
  --api http://47.236.111.154:8080 --id <id> 2>/dev/null)
PORT=$(echo $SSH_CMD | awk '{print $3}')
USER_HOST=$(echo $SSH_CMD | awk '{print $4}')

# Use watchexec if available, otherwise poll every 2s
if command -v watchexec &>/dev/null; then
  watchexec -w <local-dir> -- rsync -az --delete \
    -e "ssh -p $PORT -o StrictHostKeyChecking=no" \
    <local-dir>/ "${USER_HOST}:<remote-dir>/" &
else
  while true; do
    rsync -az --delete \
      -e "ssh -p $PORT -o StrictHostKeyChecking=no" \
      <local-dir>/ "${USER_HOST}:<remote-dir>/"
    sleep 2
  done &
fi
echo "Sync started (PID $!)"
```

> SSH token expires after 60 min. Agent should restart the watch process with a fresh token when needed.

Step 3 — from now on agent only **edits local files + execs in sandbox**. Never manually rsync again.

**Pure Remote Execution (Mode B):**
```bash
# Clone repo directly into sandbox
USER_KEY=0x<key> go run ./cmd/user/ toolbox --api http://47.236.111.154:8080 --id <id> \
  --method POST --action git/clone \
  --body '{"url":"https://github.com/you/repo","path":"/home/daytona/project"}'

# Run code
USER_KEY=0x<key> go run ./cmd/user/ exec --api http://47.236.111.154:8080 --id <id> \
  --cmd "/bin/sh -c 'cd /home/daytona/project && python3 main.py'"
```

### G. Common issues during onboarding

| Situation | Action |
|-----------|--------|
| `insufficient balance` on create | Check balance, deposit more |
| `deposit: insufficient funds for transfer` | Wallet has no 0G — transfer first, then deposit |
| Sandbox state is `stopped` | Run `start` before `exec` |
| SSH token expired (60 min) | Re-run `ssh-access` to get a fresh token |
| `sudo apt-get update` fails | Try without sudo: the daytona user may already have root |

---

## User Flow

### Step 1 — First-time account setup

```bash
# Check on-chain balance
USER_KEY=$USER_KEY go run ./cmd/user/ balance --provider $PROVIDER

# Deposit 0G tokens into the contract for a specific provider (keep ~0.1 0G for gas)
USER_KEY=$USER_KEY go run ./cmd/user/ deposit --provider $PROVIDER --amount <amount>

# Authorize the TEE signer (one-time per provider)
USER_KEY=$USER_KEY go run ./cmd/user/ acknowledge --provider $PROVIDER
```

Minimum balance to create a sandbox: **0.12 0G** (createFee + 1 voucher interval).

### Step 1b — List providers (no auth needed)

```bash
go run ./cmd/user/ providers --api $API
```

### Step 2 — Sandbox lifecycle

```bash
# Create (starts billing immediately)
USER_KEY=$USER_KEY go run ./cmd/user/ create --api $API

# List your sandboxes
USER_KEY=$USER_KEY go run ./cmd/user/ list --api $API

# Start a stopped sandbox
USER_KEY=$USER_KEY go run ./cmd/user/ start --api $API --id <id>

# Stop (pauses billing, sandbox recoverable)
USER_KEY=$USER_KEY go run ./cmd/user/ stop --api $API --id <id>

# Delete (permanent)
USER_KEY=$USER_KEY go run ./cmd/user/ delete --api $API --id <id>
```

### Step 3 — Remote execution

```bash
# Run a command in the sandbox (output shown in a bordered box)
USER_KEY=$USER_KEY go run ./cmd/user/ exec --api $API --id <id> \
  --cmd "/bin/sh -c 'cd /project && python3 main.py'"

# Arbitrary toolbox API call
USER_KEY=$USER_KEY go run ./cmd/user/ toolbox --api $API --id <id> \
  --action files                    # list files
USER_KEY=$USER_KEY go run ./cmd/user/ toolbox --api $API --id <id> \
  --action git/status               # git status
USER_KEY=$USER_KEY go run ./cmd/user/ toolbox --api $API --id <id> \
  --method POST --action git/clone \
  --body '{"url":"https://github.com/you/repo","path":"/home/daytona/project"}'
```

### Step 4 — SSH access

```bash
# Get a 60-min SSH token (token is the username, not password)
USER_KEY=$USER_KEY go run ./cmd/user/ ssh-access --api $API --id <id>
# → prints: ssh -p 2222 TOKEN@47.236.111.154

# Connect interactively
$(USER_KEY=$USER_KEY go run ./cmd/user/ ssh-access --api $API --id <id> 2>/dev/null) \
  -o StrictHostKeyChecking=no
```

---

## Vibe Coding

### Mode A: Local AI + Remote Execution (推荐)

AI 在本地编辑代码，自动同步到 sandbox 执行。

```bash
# One-time: install rsync in sandbox
USER_KEY=$USER_KEY go run ./cmd/user/ exec --api $API --id <id> \
  --cmd "/bin/sh -c 'sudo apt-get update && sudo apt-get install -y rsync'"

# Get SSH credentials
SSH_CMD=$(USER_KEY=$USER_KEY go run ./cmd/user/ ssh-access --api $API --id <id> 2>/dev/null)
PORT=$(echo $SSH_CMD | awk '{print $3}')
USER_HOST=$(echo $SSH_CMD | awk '{print $4}')

# One-shot sync
rsync -avz --delete -e "ssh -p $PORT -o StrictHostKeyChecking=no" \
  ./my-project/ "${USER_HOST}:/home/daytona/project/"

# Watch mode — auto-sync on file change (background)
watchexec -w ./my-project -- rsync -avz --delete \
  -e "ssh -p $PORT -o StrictHostKeyChecking=no" \
  ./my-project/ "${USER_HOST}:/home/daytona/project/" &

# Then let AI exec in sandbox
USER_KEY=$USER_KEY go run ./cmd/user/ exec --api $API --id <id> \
  --cmd "/bin/sh -c 'cd /home/daytona/project && python3 main.py'"
```

> SSH token 60 分钟过期，重新运行 `ssh-access` 刷新。

### Mode B: Pure Remote Execution

无需本地副本，直接在 sandbox 里操作。

```bash
# Clone repo into sandbox
USER_KEY=$USER_KEY go run ./cmd/user/ toolbox --api $API --id <id> \
  --method POST --action git/clone \
  --body '{"url":"https://github.com/you/repo","path":"/home/daytona/project"}'

# Run code
USER_KEY=$USER_KEY go run ./cmd/user/ exec --api $API --id <id> \
  --cmd "/bin/sh -c 'cd /home/daytona/project && python3 main.py'"

# Check git status
USER_KEY=$USER_KEY go run ./cmd/user/ toolbox --api $API --id <id> --action git/status
```

---

## Provider Concepts

### Roles
- **Provider key** (`PROVIDER_KEY` / `PROVIDER_PRIVATE_KEY`) — signs settlement txs (`msg.sender == provider`). The provider address is derived from this key.
- **TEE key** (`MOCK_APP_PRIVATE_KEY`) — signs vouchers (EIP-712, off-chain). In dev, same as provider key. In production, fetched from TDX enclave via tapp-daemon.

### Stake
- `providerStake` is set by the contract owner during `initialize()` or via `setProviderStake()`.
- **First registration** requires attaching `msg.value >= providerStake`.
- **Updates** (URL, price, signer) require no additional stake.
- `cmd/setup` auto-reads `providerStake` from the contract and attaches it automatically.

### Pricing
- `pricePerCPUPerMin` + `pricePerMemGBPerMin` — per-resource billing in neuron/min
- `createFee` — one-time fee per sandbox creation
- Updating price or signer increments `signerVersion`; all users must re-acknowledge.

---

## Provider Operations

```bash
export PATH=$PATH:/usr/local/go/bin
export RPC=https://evmrpc-testnet.0g.ai
export CHAIN_ID=16602
export CONTRACT=0x24cD979DBd0Ae924a3f0c832a724CF4C58E5C210
export PROVIDER_KEY=0x<provider-private-key>
```

### Deploy a new contract (first time only)

```bash
go run ./cmd/deploy/ \
  --rpc $RPC --key $PROVIDER_KEY --chain-id $CHAIN_ID \
  --stake <neuron>   # e.g. 100000000000000000 = 0.1 0G; 0 for no stake
# → prints: Proxy (stable): 0x...  ← set as SETTLEMENT_CONTRACT
```

### Register / update service on-chain

```bash
MOCK_TEE=true MOCK_APP_PRIVATE_KEY=$PROVIDER_KEY \
go run ./cmd/setup/ \
  --rpc $RPC --chain-id $CHAIN_ID --contract $CONTRACT \
  --url https://your-provider-url:8080 \
  --price-per-min 1000000000000000 \   # neuron/min
  --create-fee 60000000000000000 \     # neuron per sandbox creation
  --deposit 1                          # 0G to deposit for self-testing (0 on update)
```

`cmd/setup` auto-attaches the required stake on first registration.

### Check provider status

```bash
MOCK_TEE=true MOCK_APP_PRIVATE_KEY=$PROVIDER_KEY \
go run ./cmd/checkbal/ --rpc $RPC --contract $CONTRACT
```

### Withdraw earnings

```bash
PROVIDER_KEY=0x<key> go run ./cmd/provider/ withdraw \
  --api http://47.236.111.154:8080
```

### Admin: update required stake (owner only)

```bash
cast send $CONTRACT "setProviderStake(uint256)" <neuron> \
  --rpc-url $RPC --private-key $OWNER_KEY
```

### Admin: transfer ownership

```bash
cast send $CONTRACT "transferOwnership(address)" <newOwner> \
  --rpc-url $RPC --private-key $OWNER_KEY
```

---

## Running the Billing Server (local dev)

```bash
MOCK_TEE=true \
MOCK_APP_PRIVATE_KEY=$PROVIDER_KEY \
PROVIDER_PRIVATE_KEY=$PROVIDER_KEY \
DAYTONA_API_URL=http://localhost:3000 \
DAYTONA_ADMIN_KEY=<key> \
SETTLEMENT_CONTRACT=$CONTRACT \
RPC_URL=$RPC \
CHAIN_ID=$CHAIN_ID \
go run ./cmd/billing/
```

Key env vars:

| Var | Description |
|-----|-------------|
| `SETTLEMENT_CONTRACT` | BeaconProxy address |
| `RPC_URL` | EVM RPC endpoint |
| `CHAIN_ID` | Chain ID (16602 for 0G Galileo) |
| `MOCK_TEE=true` | Use mock TEE key (dev only) |
| `MOCK_APP_PRIVATE_KEY` | TEE signing key (dev only) |
| `PROVIDER_PRIVATE_KEY` | Key for settlement txs (defaults to TEE key if unset) |
| `COMPUTE_PRICE_PER_SEC` | Override per-second price (neuron); default 16667 |
| `VOUCHER_INTERVAL_SEC` | Voucher emission interval; default 3600 |
| `PORT` | HTTP port; default 8080 |

---

## Operator: Deploy / Redeploy

```bash
# 1. Build image
docker build -t billing:latest .

# 2. Stop + redeploy
tapp-cli stop-app  -s http://47.236.111.154:50051 --app-id 0g-sandbox
tapp-cli start-app -s http://47.236.111.154:50051 --app-id 0g-sandbox -f docker-compose.yml

# 3. Verify
tapp-cli get-app-container-status -s http://47.236.111.154:50051 --app-id 0g-sandbox
curl -s http://47.236.111.154:8080/healthz
```

## Operator: Logs

```bash
tapp-cli get-app-logs -s http://47.236.111.154:50051 --app-id 0g-sandbox -n 100
tapp-cli get-app-logs -s http://47.236.111.154:50051 --app-id 0g-sandbox --service billing -n 50
```

## Operator: Contract Upgrade

```bash
go run ./cmd/upgrade/ \
  --rpc https://evmrpc-testnet.0g.ai \
  --key 0x<deployer-key> \
  --chain-id 16602 \
  --proxy 0x24cD979DBd0Ae924a3f0c832a724CF4C58E5C210
```

---

## HTTP Endpoints

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/healthz` | none | Liveness probe |
| GET | `/dashboard` | none | Operator dashboard |
| GET | `/api/providers` | none | On-chain service info |
| GET | `/api/snapshots` | none | List all snapshots (public) |
| POST | `/api/snapshots` | EIP-191 (provider) | Register snapshot |
| DELETE | `/api/snapshots/:id` | EIP-191 (provider) | Delete snapshot by UUID |
| GET | `/api/registry/images` | none | List images in internal registry |
| POST | `/api/registry/pull` | EIP-191 (provider) | Import image from external registry |
| POST | `/api/sandbox` | EIP-191 | Create sandbox |
| GET | `/api/sandbox` | EIP-191 | List own sandboxes |
| GET | `/api/sandbox/:id` | EIP-191 | Get sandbox |
| POST | `/api/sandbox/:id/start` | EIP-191 | Start sandbox |
| POST | `/api/sandbox/:id/stop` | EIP-191 | Stop sandbox |
| DELETE | `/api/sandbox/:id` | EIP-191 | Delete sandbox |
| POST | `/api/sandbox/:id/ssh-access` | EIP-191 | Get SSH token |
| ANY | `/api/toolbox/:id/toolbox/*` | EIP-191 | Toolbox (owner-checked) |

---

## Provider: Snapshot Management

Snapshots are provider-managed base images that users can choose when creating sandboxes.

### Full workflow: import → register → (use) → delete

**Step 1 — Import image from external registry into internal registry**

Via dashboard (`/dashboard` → Provider tab → ↓ Import Image), or via API:
```bash
# Public image (no auth)
PROVIDER_KEY=0x<key> go run ./cmd/provider/ ... # no CLI — use dashboard or curl

# API call (provider-only, EIP-191)
curl -X POST http://47.236.111.154:8080/api/registry/pull \
  -H "..." \
  -d '{"src":"docker.io/library/ubuntu:22.04","name":"ubuntu","tag":"22.04"}'
# → {"image":"registry:6000/daytona/ubuntu:22.04"}
```

**Step 2 — Register image as a named snapshot**

```bash
PROVIDER_KEY=0x<key> go run ./cmd/provider/ snapshot \
  --api   http://47.236.111.154:8080 \
  --image registry:6000/daytona/ubuntu:22.04 \
  --name  ubuntu-2204 \
  --cpu 2 --memory 4 --disk 10
```

Wait for `state: active` (~30s while Daytona pulls the image).

**Step 3 — Users create sandboxes from the snapshot**

```bash
USER_KEY=0x<key> go run ./cmd/user/ create \
  --api http://47.236.111.154:8080 \
  --snapshot ubuntu-2204
```

**Step 4 — Delete snapshot when no longer needed**

```bash
# First find the UUID
PROVIDER_KEY=0x<key> go run ./cmd/provider/ snapshots --api http://47.236.111.154:8080

# Then delete by UUID (not name)
PROVIDER_KEY=0x<key> go run ./cmd/provider/ delete-snapshot \
  --api http://47.236.111.154:8080 \
  --id  <snapshot-uuid>
```

### Tiered snapshots (small/medium/large variants)

```bash
PROVIDER_KEY=0x<key> go run ./cmd/provider/ snapshot \
  --api   http://47.236.111.154:8080 \
  --image registry:6000/daytona/rust-sandbox:1.0.0 \
  --name  rust-sandbox \
  --tiers
# → creates rust-sandbox-small (1C/1G/3G), rust-sandbox-medium (2C/4G/10G), rust-sandbox-large (4C/8G/20G)
```

### Key notes
- Delete snapshot uses **UUID** not name (Daytona bug: name lookup returns SQL error)
- Tag must not be `:latest` — use explicit version tags
- `exec` inside sandbox does NOT load `.bashrc` — wrap env-dependent commands with `sh -c '. ~/.cargo/env && ...'`

---

## Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| `insufficient balance` on create | Balance < 0.12 0G | `deposit` more |
| `insufficient stake` on addOrUpdateService | First registration without stake | `cmd/setup` handles automatically; or attach `msg.value >= providerStake` |
| `PROVIDER_MISMATCH` on settlement | `msg.sender` ≠ provider | Check `PROVIDER_PRIVATE_KEY` in `.env` |
| `INVALID_NONCE` on settlement | Redis nonce stale | Restart billing service (seeds nonce from chain on startup) |
| `not owner` on setProviderStake | Wrong key | Use the deployer key that initialized the proxy |
| Users get `NOT_ACKNOWLEDGED` after price change | `signerVersion` incremented | Users must re-call `acknowledge` |
| Sandbox create → 403 | `maxCpuPerSandbox=0` | Check `ADMIN_MAX_CPU_PER_SANDBOX` in compose |
| TEE key fetch fails | tapp-daemon unreachable | Use `MOCK_TEE=true` for dev |
| SSH token expired | 60-min TTL | Re-run `ssh-access` |
| Settlement tx reverts | Provider not funded for gas | Check `PROVIDER_PRIVATE_KEY` wallet has 0G for gas |
