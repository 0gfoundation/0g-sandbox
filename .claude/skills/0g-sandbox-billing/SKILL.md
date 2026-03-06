---
name: 0g-sandbox-billing
description: Use this skill when working on the 0G Sandbox Billing project — deploying/redeploying the service, checking logs, running tests, operating the settlement contract, or using the provider/user CLIs. Covers the full dev and ops workflow for this repo.
version: 1.0.0
author: 0G Labs
tags: [0g, billing, sandbox, daytona, tee, settlement, go, solidity]
repository: https://github.com/0gfoundation/0g-sandbox-billing
---

# 0G Sandbox Billing Skill

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

### D. Create sandbox

```bash
USER_KEY=0x<key> go run ./cmd/user/ create --api http://47.236.111.154:8080
```
Note the sandbox ID from the response. Billing starts immediately.

### E. Set up vibe coding

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

### F. Common issues during onboarding

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

## Operator: Deploy / Redeploy

```bash
# 1. Build image
docker build -t billing:latest .

# 2. Stop + redeploy
tapp-cli stop-app  -s http://47.236.111.154:50051 --app-id billing
tapp-cli start-app -s http://47.236.111.154:50051 --app-id billing -f docker-compose.yml

# 3. Verify
tapp-cli get-app-container-status -s http://47.236.111.154:50051 --app-id billing
curl -s http://47.236.111.154:8080/healthz
```

## Operator: Logs

```bash
tapp-cli get-app-logs -s http://47.236.111.154:50051 --app-id billing -n 100
tapp-cli get-app-logs -s http://47.236.111.154:50051 --app-id billing --service billing -n 50
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
| POST | `/api/sandbox` | EIP-191 | Create sandbox |
| GET | `/api/sandbox` | EIP-191 | List own sandboxes |
| GET | `/api/sandbox/:id` | EIP-191 | Get sandbox |
| POST | `/api/sandbox/:id/start` | EIP-191 | Start sandbox |
| POST | `/api/sandbox/:id/stop` | EIP-191 | Stop sandbox |
| DELETE | `/api/sandbox/:id` | EIP-191 | Delete sandbox |
| POST | `/api/sandbox/:id/ssh-access` | EIP-191 | Get SSH token |
| ANY | `/api/toolbox/:id/toolbox/*` | EIP-191 | Toolbox (owner-checked) |

---

## Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| `insufficient balance` on create | Balance < 0.12 0G | `deposit` more |
| `PROVIDER_MISMATCH` on settlement | `msg.sender` ≠ provider | Check `PROVIDER_PRIVATE_KEY` in `.env` |
| `INVALID_NONCE` on settlement | Redis nonce stale | Restart billing service |
| Sandbox create → 403 | `maxCpuPerSandbox=0` | Check `ADMIN_MAX_CPU_PER_SANDBOX` in compose |
| TEE key fetch fails | tapp-daemon unreachable | Use `MOCK_TEE=true` for dev |
| SSH token expired | 60-min TTL | Re-run `ssh-access` |
