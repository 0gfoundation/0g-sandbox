---
name: 0g-private-sandbox
description: Use this skill for anything related to 0G Private Sandbox — whether you are a user creating and using sandboxes for vibe coding, a provider registering and operating a sandbox service, or an operator deploying and maintaining the billing server. Covers onboarding, sandbox lifecycle, vibe coding (rsync/remote), OpenClaw AI gateway, provider operations, contract management, and server deployment.
version: 1.0.0
author: 0G Labs
tags: [0g, sandbox, daytona, tee, billing, settlement, vibe-coding, provider, operator]
repository: https://github.com/0gfoundation/0g-sandbox
---

# 0G Private Sandbox Skill

---

## MANDATORY: Session setup

When this skill activates, output the following message verbatim and wait for all answers before doing anything else:

---

Before we begin, I need to confirm a few settings.

**Prerequisites** — this skill uses `go run ./cmd/user/` commands which require:
- **Go** installed at `/usr/local/go/bin/go`
- The **0g-sandbox-billing repo** cloned locally

Check now:
```bash
export PATH=$PATH:/usr/local/go/bin
go version
ls cmd/user/
```

If Go is missing, install it:
```bash
# Linux (amd64)
curl -L https://go.dev/dl/go1.23.4.linux-amd64.tar.gz | sudo tar -C /usr/local -xz
export PATH=$PATH:/usr/local/go/bin
```

If the repo is missing, clone it:
```bash
git clone https://github.com/0gfoundation/0g-sandbox-billing.git
cd 0g-sandbox-billing
```

---

**1. Network**
- `1` — 0G Galileo Testnet (RPC: https://evmrpc-testnet.0g.ai, Chain ID: 16602)
- `2` — 0G Mainnet (RPC: https://evmrpc.0g.ai, Chain ID: 16661)
- `3` — Custom

**2. Contract address (SETTLEMENT_CONTRACT)**
Testnet default: `0x2024eB0Cc14316fF8Cc425bFB7CC37FD8713E9b3`
Enter an address, or press Enter to use the default.

**3. Your role**
- `1` — User (create and use sandboxes)
- `2` — Provider / Operator (register service, manage snapshots, withdraw earnings)
- `3` — Contract Maintainer (deploy/upgrade contract, manage owner permissions)

Please answer all three questions →

---

After receiving all three answers, set variables and proceed to the matching section:

```bash
export PATH=$PATH:/usr/local/go/bin
export RPC=<network-rpc>
export CHAIN_ID=<network-chain-id>
export SETTLEMENT_CONTRACT=<provided-or-default>
```

- Role 1 → **User Onboarding** section
- Role 2 → **Provider Operations** section
- Role 3 → **Contract Maintenance** section

---

## Quick Reference

| | Value |
|---|---|
| **Billing proxy** | `http://47.236.111.154:8080` |
| **Dashboard** | `http://47.236.111.154:8080/dashboard` |
| **Contract (SETTLEMENT_CONTRACT)** | `0x2024eB0Cc14316fF8Cc425bFB7CC37FD8713E9b3` |
| **Provider** | `0xB831371eb2703305f1d9F8542163633D0675CEd7` |
| **TEE Signer** | `0x61BEb835D1935Eec8cC04efa2f4e2B3cC8B8B6E3` |
| **Go binary** | `/usr/local/go/bin/go` |

---

## User Onboarding

### Step 0 — Wallet

**If no wallet** — generate one:
```bash
export PATH=$PATH:/usr/local/go/bin
go run - <<'EOF'
package main
import ("fmt"; "github.com/ethereum/go-ethereum/crypto")
func main() {
    k, _ := crypto.GenerateKey()
    fmt.Printf("Private Key: 0x%x\nAddress:     %s\n",
        crypto.FromECDSA(k), crypto.PubkeyToAddress(k.PublicKey).Hex())
}
EOF
```
Tell the user to save their private key securely. It will not be shown again.

**If they have a wallet** — collect the private key:
```bash
export USER_KEY=0x<private-key>
```

### Step 1 — Discover providers

```bash
go run ./cmd/user/ providers
```

Example output:
```
Found 1 provider(s) on-chain:

[1] 0xB831371eb2703305f1d9F8542163633D0675CEd7
    URL:         http://47.236.111.154:8080
    Create fee:  0.0600 0G
    Compute:     0.001000 0G/sec  (0.0600 0G/min)
    TEE signer:  0x61BEb835... (v4)
```

- Single provider → use it automatically, export vars
- Multiple providers → show list and ask user to pick

```bash
export PROVIDER=<chosen-provider-address>
export API=<provider-url>
```

### Step 2 — Balance check & deposit

```bash
USER_KEY=$USER_KEY go run ./cmd/user/ balance --provider $PROVIDER
```

Output:
```
Wallet balance:  X neuron  (X.XX 0G)  ← for gas
Contract balance:Y neuron  (Y.YY 0G)  ← for sandbox billing
```

Calculate estimated usage time and show:
```
Estimated time = (contract_balance - create_fee) / compute_price_per_sec / 60  minutes
```

Ask: "Would you like to deposit? If so, how much 0G?"

Minimum required: **0.12 0G** (createFee 0.06 + 1 interval 0.06). Recommended: **1–10 0G**.

If depositing:
1. Ensure wallet has enough 0G (keep ~0.1 for gas)
2. Deposit:
```bash
USER_KEY=$USER_KEY go run ./cmd/user/ deposit --provider $PROVIDER --amount <amount>
```
3. Confirm balance again.

### Step 3 — Acknowledge TEE signer (one-time per wallet × provider)

Only required when contract balance was 0 (first-time use):
```bash
USER_KEY=$USER_KEY go run ./cmd/user/ acknowledge --provider $PROVIDER
```

### Step 4 — Check for existing sandboxes

```bash
USER_KEY=$USER_KEY go run ./cmd/user/ list --api $API
```

- **Existing sandboxes found** → show list, ask: reuse one or create new?
  - Reuse: `USER_KEY=$USER_KEY go run ./cmd/user/ start --api $API --id <id>`
  - Create new → Step 5
- **No sandboxes** → Step 5 directly

### Step 5 — Understand goal, recommend snapshot (REQUIRED — wait for user)

First ask:
> "What do you want to use this sandbox for?"

Then list available snapshots:
```bash
go run ./cmd/user/ snapshots --api $API
```

Based on goal, proactively recommend:

| User goal | Recommendation |
|-----------|----------------|
| General vibe coding / running code / testing | Default image (no snapshot) or `0g-sandbox-base` |
| AI coding assistant in a secure private sandbox | **openclaw** snapshot — pre-installed OpenClaw AI Gateway |
| Specific environment (Rust, Python, etc.) | List matching snapshots and let user choose |

**STOP and present recommendation:**
> "Based on your goal, I recommend **[snapshot]**: [one-line description].
> Available snapshots: [list name / CPU / memory]
> Shall I use this? Or do you have another preference?"

Wait for confirmation before proceeding.

### Step 6 — Create sandbox

```bash
# Without snapshot
USER_KEY=$USER_KEY go run ./cmd/user/ create --api $API --name <friendly-name>

# With snapshot
USER_KEY=$USER_KEY go run ./cmd/user/ create --api $API --name <friendly-name> --snapshot <name>
export SANDBOX_ID=<returned-id>
```

Billing starts immediately. If user chose **openclaw** → skip to **OpenClaw Mode** below.

### Step 7 — Recommend vibe coding mode

The sandbox takes a few seconds to start. Use that time to recommend a mode:

| User's goal | Recommendation |
|-------------|----------------|
| Starting a new project from scratch | **Mode A** — Claude edits locally, rsync syncs to sandbox |
| Modifying an existing local project | **Mode A** — local code + remote execution |
| Running an existing GitHub project | **Mode B** — git clone into sandbox and run directly |
| Quick one-off command | **Mode B** — remote exec directly |

Template (Mode A):
> "Sandbox is starting. Based on your goal (starting xxx from scratch), I recommend **Mode A**: Claude edits local files, rsync auto-syncs to sandbox, sandbox executes. Confirm?"

Template (Mode B):
> "Sandbox is starting. Based on your goal (running existing project xxx), I recommend **Mode B**: git clone directly into sandbox. Confirm?"

Wait for confirmation, then proceed with the relevant mode.

---

## Mode A — Local AI + Remote Execution (rsync)

### Setup (BEFORE syncing)

**IMPORTANT: if rsync or sshpass is missing, install it. Do NOT fall back to toolbox upload just because a tool is absent — only fall back if installation itself fails.**

Known issues:
- `exec` does not invoke a shell — `&&`, `||` not interpreted. Always wrap with `sh -c '...'`.
- `apt-get` inside sandbox may require `sudo`.

**Local machine:**
```bash
which rsync sshpass
# If missing:
sudo apt-get install -y rsync sshpass   # Ubuntu/Debian
brew install rsync sshpass               # macOS
```

**Sandbox (always check, don't assume):**
```bash
USER_KEY=$USER_KEY go run ./cmd/user/ exec \
  --api $API --id $SANDBOX_ID --cmd "which rsync"
# If not found:
USER_KEY=$USER_KEY go run ./cmd/user/ exec \
  --api $API --id $SANDBOX_ID \
  --cmd "sh -c 'sudo apt-get update -qq && sudo apt-get install -y rsync'"
```

### Sync via rsync

```bash
export LOCAL_DIR=/path/to/your/project
export REMOTE_DIR=/home/daytona/project

# Get SSH credentials
# NOTE: use 2>&1, NOT 2>/dev/null — "Password:" line comes from stderr
SSH_OUTPUT=$(USER_KEY=$USER_KEY go run ./cmd/user/ ssh-access \
  --api $API --id $SANDBOX_ID 2>&1)
SSH_LINE=$(echo "$SSH_OUTPUT" | grep '^ssh ')
PORT=$(echo "$SSH_LINE" | grep -o '\-p [0-9]*' | awk '{print $2}')
USER_HOST=$(echo "$SSH_LINE" | awk '{print $NF}')
TOKEN=$(echo "$SSH_OUTPUT" | grep '^Password:' | awk '{print $2}')

# ⚠️ Known issue: SSH via domain hangs (CLB TCP listener not configured).
# If SSH/rsync fails, replace the domain in USER_HOST with the direct IP:
# USER_HOST=$(echo "$USER_HOST" | sed 's/private-sandbox-testnet.0g.ai/43.106.147.28/')

# Initial full sync
# NOTE: rsync may exit 255 even on success — verify files instead of exit code
sshpass -p $TOKEN rsync -avz \
  -e "ssh -p $PORT -o StrictHostKeyChecking=no" \
  --exclude='.git' --exclude='__pycache__' --exclude='*.pyc' \
  $LOCAL_DIR/ $USER_HOST:$REMOTE_DIR/ || true

# Watch mode (Linux — requires inotifywait)
while inotifywait -r -e modify,create,delete --exclude='(__pycache__|\.git)' $LOCAL_DIR; do
  sshpass -p $TOKEN rsync -avz \
    -e "ssh -p $PORT -o StrictHostKeyChecking=no" \
    --exclude='.git' --exclude='__pycache__' --exclude='*.pyc' \
    $LOCAL_DIR/ $USER_HOST:$REMOTE_DIR/ || true
done &
```

> SSH token expires ~1h. Re-run `ssh-access` to refresh.

### Fallback: toolbox upload (if rsync/sshpass unavailable)

```bash
# Upload a single file (base64-encoded)
FILE=main.py
USER_KEY=$USER_KEY go run ./cmd/user/ toolbox \
  --api $API --id $SANDBOX_ID \
  --method POST --action files/upload \
  --body "{\"path\":\"$REMOTE_DIR/$FILE\",\"content\":\"$(base64 -w0 $LOCAL_DIR/$FILE)\"}"

# Upload all files in a loop
find $LOCAL_DIR -type f \
  ! -path '*/.git/*' ! -path '*/__pycache__/*' ! -name '*.pyc' \
| while read f; do
    rel="${f#$LOCAL_DIR/}"
    USER_KEY=$USER_KEY go run ./cmd/user/ toolbox \
      --api $API --id $SANDBOX_ID \
      --method POST --action files/upload \
      --body "{\"path\":\"$REMOTE_DIR/$rel\",\"content\":\"$(base64 -w0 $f)\"}"
  done
```

### Run code after sync

```bash
USER_KEY=$USER_KEY go run ./cmd/user/ exec \
  --api $API --id $SANDBOX_ID \
  --cmd "sh -c 'cd $REMOTE_DIR && python3 main.py'"
```

From now on: agent only **edits local files + execs in sandbox**. Never manually rsync again.

---

## Mode B — Direct Remote Execution

```bash
# Clone repo into sandbox
USER_KEY=$USER_KEY go run ./cmd/user/ toolbox \
  --api $API --id $SANDBOX_ID \
  --method POST --action git/clone \
  --body '{"url":"https://github.com/org/repo","path":"/home/daytona/project"}'

# Install deps and run
USER_KEY=$USER_KEY go run ./cmd/user/ exec \
  --api $API --id $SANDBOX_ID \
  --cmd "sh -c 'cd /home/daytona/project && pip install -r requirements.txt && python main.py'"

# Check files / git status
USER_KEY=$USER_KEY go run ./cmd/user/ toolbox --api $API --id $SANDBOX_ID --action files
USER_KEY=$USER_KEY go run ./cmd/user/ toolbox --api $API --id $SANDBOX_ID --action git/status
```

---

## OpenClaw Mode — AI Coding in Secure Sandbox

For users who chose the **openclaw** snapshot. OpenClaw is an AI coding gateway (powered by Claude) that runs privately inside your sandbox — code and API key never leave it.

When entering this mode, first present the full setup plan and ask how the user wants to proceed:

> Here's how we'll set up OpenClaw:
>
> 1. **Start Gateway** — start openclaw gateway in the background inside the sandbox (port 3284)
> 2. **Get Token** — read the auth token auto-generated by the gateway
> 3. **SSH Tunnel** — forward local port to sandbox — you run this on your **local machine**
> 4. **Open Browser** — visit `http://localhost:13284/#token=<token>`
>
> **You'll need an Anthropic API Key.** How to get one:
> - **Claude Code users**: run `claude setup-token` in your terminal
> - **Others**: visit https://console.anthropic.com/settings/keys to create a new key
>
> **Choose setup method:**
> - `A` — **Agent-assisted**: provide your API Key, I'll run all steps and give you the SSH tunnel command to run locally
> - `B` — **Self-configure via SSH**: I'll give you an SSH token and a step-by-step tutorial
>
> Please provide: your API Key and choice (A or B)?

Then proceed based on user's choice. Execute each step and show output, waiting for user confirmation between steps 2→3 (since step 3 runs on local machine):

**Step 1 — Set gateway mode + start (agent executes)**

```bash
# Must set mode first, otherwise gateway refuses to start
USER_KEY=$USER_KEY go run ./cmd/user/ exec --api $API --id $SANDBOX_ID \
  --cmd "/bin/bash -c 'openclaw config set gateway.mode local'"

# Start gateway in background (use bash -c so redirection works under nohup)
USER_KEY=$USER_KEY go run ./cmd/user/ exec --api $API --id $SANDBOX_ID \
  --cmd "/bin/bash -c 'export ANTHROPIC_API_KEY=sk-ant-YOUR-KEY; nohup bash -c \"openclaw gateway run --bind lan --port 3284 > /tmp/openclaw.log 2>&1\" &'"
```

Wait 3s, confirm running:
```bash
USER_KEY=$USER_KEY go run ./cmd/user/ exec --api $API --id $SANDBOX_ID \
  --cmd "/bin/bash -c 'sleep 3 && grep \"listening on\" /tmp/openclaw.log'"
# → [gateway] listening on ws://0.0.0.0:3284 (PID xxx)
```

**Step 2 — Get gateway auth token (agent executes)**

```bash
USER_KEY=$USER_KEY go run ./cmd/user/ exec --api $API --id $SANDBOX_ID \
  --cmd "/bin/bash -c 'node -e \"console.log(require(\\\"/root/.openclaw/openclaw.json\\\").gateway.auth.token)\"'"
# → 56e254285de2f6b7f40c489c0314e45bf83818a6d5735581
```

Show the token to the user clearly. Then say:
> Token retrieved: `<token>`
> Next, run the following command on your **local machine** to establish the SSH tunnel, then let me know when it's running.

**Step 3 — SSH tunnel (user executes on LOCAL machine)**

Give the user this **single-line** command (single quotes to avoid zsh `!` expansion):

```bash
ssh -N -L 13284:localhost:3284 -p 2222 -o StrictHostKeyChecking=no '<SSH_TOKEN>@HOST' &
```

> ⚠️ Use port **13284** (not 3284) — local 3284 may already be in use.
> ⚠️ zsh users: always wrap SSH token in **single quotes** — `!` triggers history expansion in double quotes.
> ℹ️ No password needed — the SSH token serves as both username and authentication credential.
> ⚠️ Known issue: SSH via domain (`private-sandbox-testnet.0g.ai`) hangs on port 2222. Replace HOST with `43.106.147.28` if the tunnel hangs.

Wait for user to confirm tunnel is running, then:

**Step 4 — Open in browser**

> Tunnel established! Open in browser (note the `#token=` hash format):
> **http://localhost:13284/#token=`<openclaw-token>`**
>
> The URL must use `#token=...` (hash fragment), NOT `?token=...` (query string).

**Option B — Self-configure via SSH (agent provides token and tutorial)**

Agent executes ssh-access to get a token and shows the user:

```bash
USER_KEY=$USER_KEY go run ./cmd/user/ ssh-access --api $API --id $SANDBOX_ID
# → ssh -p <PORT> <SSH_TOKEN>@<HOST>
# → Password: <SSH_TOKEN>
```

Then present the user with this tutorial:

> **SSH Setup Tutorial (run on your local machine)**
>
> ```bash
> # 1. Connect to sandbox (no password needed — token is username AND auth credential)
> ssh -p <PORT> -o StrictHostKeyChecking=no '<SSH_TOKEN>@<HOST>'
> ```
>
> ```bash
> # 2. Set gateway mode (required, or gateway refuses to start)
> openclaw config set gateway.mode local
> ```
>
> ```bash
> # 3. Start OpenClaw Gateway (replace with your API Key)
> export ANTHROPIC_API_KEY=sk-ant-YOUR-KEY
> nohup bash -c 'openclaw gateway run --bind lan --port 3284 > /tmp/openclaw.log 2>&1' &
> sleep 3 && grep "listening on" /tmp/openclaw.log
> ```
>
> ```bash
> # 4. Get auth token
> node -e "console.log(require('/root/.openclaw/openclaw.json').gateway.auth.token)"
> ```
>
> ```bash
> # 5. Exit SSH
> exit
> ```
>
> ```bash
> # 6. Establish SSH Tunnel — new terminal window, single-line, single quotes to avoid zsh ! expansion
> ssh -N -L 13284:localhost:3284 -p <PORT> -o StrictHostKeyChecking=no '<SSH_TOKEN>@<HOST>' &
> ```
>
> 7. Open browser (**must use `#token=` hash fragment format**):
>    **http://localhost:13284/#token=`<openclaw-token>`**
>
> **Notes:**
> - No password needed — SSH token is both username and auth credential
> - Always use **single-line** commands to avoid copy-paste issues with line continuations
> - Local port 3284 may be in use → use 13284 or another port
> - zsh: always wrap tokens containing `!` in single quotes
> - SSH Token expires after 60 min → re-run `ssh-access` to refresh tunnel
> - Gateway keeps running inside sandbox; rebuilding tunnel does not require restarting gateway
> - Run `stop` to pause billing when not in use

**Gotchas (learned from real usage):**
- `--allow-unconfigured` flag was removed in newer openclaw versions — do not use it
- Must run `openclaw config set gateway.mode local` before starting the gateway
- `nohup openclaw ... > /tmp/openclaw.log &` does NOT work — wrap with `nohup bash -c '...' &`
- Token is at `gateway.auth.token` in config; extract with `node -e "console.log(require(...).gateway.auth.token)"`
- Browser URL must use `#token=<token>` (hash fragment), not `?token=` or a login form
- SSH token 60 min expiry → re-run Step 3 to refresh tunnel
- Stop gateway: `exec --cmd "/bin/bash -c 'pkill -f openclaw'"`
- Stop sandbox to pause billing when not in use

---

## Sandbox Management

```bash
# List
USER_KEY=$USER_KEY go run ./cmd/user/ list --api $API

# Start stopped sandbox
USER_KEY=$USER_KEY go run ./cmd/user/ start --api $API --id $SANDBOX_ID

# Stop (pauses billing)
USER_KEY=$USER_KEY go run ./cmd/user/ stop --api $API --id $SANDBOX_ID

# Delete (permanent)
USER_KEY=$USER_KEY go run ./cmd/user/ delete --api $API --id $SANDBOX_ID

# SSH access (60-min token)
USER_KEY=$USER_KEY go run ./cmd/user/ ssh-access --api $API --id $SANDBOX_ID

# Check balance
USER_KEY=$USER_KEY go run ./cmd/user/ balance --provider $PROVIDER
```

---

## Toolbox Quick Reference

```bash
USER_KEY=$USER_KEY go run ./cmd/user/ toolbox \
  --api $API --id $SANDBOX_ID \
  [--method POST] --action <action> [--body '<json>']
```

| Action | Method | Description |
|--------|--------|-------------|
| `files` | GET | List files |
| `files/upload` | POST | Upload a file (base64) |
| `files/download` | GET | Download a file |
| `git/status` | GET | Git status |
| `git/clone` | POST | Clone repo |
| `git/commit` | POST | Commit changes |
| `process/execute` | POST | Run command (full output) |
| `project-dir` | GET | Get project directory |

---

## Provider Operations

### Concepts

- **Provider key** (`PROVIDER_KEY`) — stakes and registers service; provider address derived from this key
- **TEE key** — signs vouchers (EIP-712) and settlement txs. Prod: fetched from TDX enclave. Dev: set `MOCK_TEE=true` + `MOCK_APP_PRIVATE_KEY`.
- **Stake**: first registration requires `msg.value >= providerStake`. Updates (URL/price/signer) need no extra stake.
- **Pricing**: `pricePerCPUPerMin` + `pricePerMemGBPerMin` in neuron/min. `createFee` per sandbox creation.
- Updating price or signer increments `signerVersion` — all users must re-acknowledge.

```bash
export PROVIDER_KEY=0x<provider-private-key>
```

### Register / update service on-chain

```bash
PROVIDER_KEY=$PROVIDER_KEY go run ./cmd/provider/ register \
  --rpc $RPC --contract $SETTLEMENT_CONTRACT \
  --url https://your-provider-url \
  --price-per-cpu 1000000000000000 \
  --price-per-mem 500000000000000 \
  --fee 60000000000000000
```

### Check provider status

```bash
PROVIDER_KEY=$PROVIDER_KEY go run ./cmd/provider/ status \
  --rpc $RPC --contract $SETTLEMENT_CONTRACT
```

### Withdraw earnings

```bash
PROVIDER_KEY=$PROVIDER_KEY go run ./cmd/provider/ withdraw --api $API
```

### Snapshot management

**Step 1 — Import image into internal registry** (via dashboard or API):
```bash
# Via dashboard: /dashboard → Provider tab → ↓ Import Image
# Via API (provider-only, EIP-191):
curl -X POST $API/api/registry/pull \
  -H "..." \
  -d '{"src":"docker.io/library/ubuntu:22.04","name":"ubuntu","tag":"22.04"}'
```

**Step 2 — Register as named snapshot:**
```bash
PROVIDER_KEY=$PROVIDER_KEY go run ./cmd/provider/ snapshot \
  --api $API \
  --image registry:6000/daytona/<name>:<tag> \
  --name <snapshot-name> \
  --cpu 2 --memory 4 --disk 10
# Or with tiers (small/medium/large):
PROVIDER_KEY=$PROVIDER_KEY go run ./cmd/provider/ snapshot \
  --api $API --image registry:6000/daytona/<name>:<tag> --name <snapshot-name> --tiers
```

Wait for `state: active` (~30s).

**Step 3 — Delete snapshot:**
```bash
# Find UUID first
PROVIDER_KEY=$PROVIDER_KEY go run ./cmd/provider/ snapshots --api $API
# Delete by UUID (not name)
PROVIDER_KEY=$PROVIDER_KEY go run ./cmd/provider/ delete-snapshot --api $API --id <uuid>
```

**Notes:**
- Tag must NOT be `:latest` — use explicit version tags
- `exec` does not load `.bashrc` — wrap env-dependent commands: `sh -c '. ~/.cargo/env && ...'`

---

## Operator

### Running the billing server (local dev)

```bash
MOCK_TEE=true \
MOCK_APP_PRIVATE_KEY=0x<tee-key> \
DAYTONA_API_URL=http://localhost:3000 \
DAYTONA_ADMIN_KEY=<key> \
SETTLEMENT_CONTRACT=$SETTLEMENT_CONTRACT \
RPC_URL=$RPC \
CHAIN_ID=$CHAIN_ID \
go run ./cmd/billing/
```

| Var | Description |
|-----|-------------|
| `SETTLEMENT_CONTRACT` | BeaconProxy address |
| `RPC_URL` | EVM RPC endpoint |
| `CHAIN_ID` | Chain ID (16602 testnet / 16661 mainnet) |
| `MOCK_TEE=true` | Use mock TEE key (dev only) |
| `MOCK_APP_PRIVATE_KEY` | TEE signing key (dev only); signs vouchers and settlement txs |
| `COMPUTE_PRICE_PER_SEC` | Per-second price (neuron); default 16667 |
| `VOUCHER_INTERVAL_SEC` | Voucher interval; default 3600 |
| `PORT` | HTTP port; default 8080 |

### Deploy / redeploy

```bash
docker build -t billing:latest .
tapp-cli stop-app  -s http://47.236.111.154:50051 --app-id 0g-sandbox
tapp-cli start-app -s http://47.236.111.154:50051 --app-id 0g-sandbox -f docker-compose.yml
tapp-cli get-app-container-status -s http://47.236.111.154:50051 --app-id 0g-sandbox
curl -s http://47.236.111.154:8080/healthz
```

### Logs

```bash
tapp-cli get-app-logs -s http://47.236.111.154:50051 --app-id 0g-sandbox -n 100
tapp-cli get-app-logs -s http://47.236.111.154:50051 --app-id 0g-sandbox --service billing -n 50
```

---

## Contract Maintenance

The contract maintainer holds the owner key from deployment. This may or may not be the same as the provider key.

```bash
export OWNER_KEY=0x<deployer/owner-private-key>
```

### Deploy a new contract (first time only)

```bash
go run ./cmd/deploy/ \
  --rpc $RPC --key $OWNER_KEY --chain-id $CHAIN_ID \
  --stake <neuron>   # e.g. 100000000000000000 = 0.1 0G; 0 for no stake
# → prints: Proxy (stable): 0x...  ← set as SETTLEMENT_CONTRACT
```

### Upgrade contract logic

```bash
go run ./cmd/upgrade/ \
  --rpc $RPC --key $OWNER_KEY --chain-id $CHAIN_ID \
  --proxy $SETTLEMENT_CONTRACT
```

### Update required provider stake

```bash
cast send $SETTLEMENT_CONTRACT "setProviderStake(uint256)" <neuron> \
  --rpc-url $RPC --private-key $OWNER_KEY
```

### Transfer ownership

```bash
cast send $SETTLEMENT_CONTRACT "transferOwnership(address)" <newOwner> \
  --rpc-url $RPC --private-key $OWNER_KEY
```

---

## HTTP Endpoints

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/healthz` | none | Liveness probe |
| GET | `/dashboard` | none | Operator dashboard |
| GET | `/api/providers` | none | On-chain service info |
| GET | `/api/snapshots` | none | List all snapshots |
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

## Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| `insufficient balance` on create | Balance < 0.12 0G | `deposit` more |
| `deposit: insufficient funds` | Wallet has no 0G | Transfer 0G to wallet first, then deposit |
| `GetBalance` revert | Wrong contract address | Check `SETTLEMENT_CONTRACT` is current |
| `NOT_ACKNOWLEDGED` after price change | `signerVersion` incremented | Re-run `acknowledge` |
| `PROVIDER_MISMATCH` on settlement | Wrong provider in voucher | Check TEE key matches registered provider |
| `INVALID_NONCE` on settlement | Redis nonce stale | Restart billing service |
| Sandbox state is `stopped` | Auto-stopped or billing stopped it | Run `start` before `exec` |
| SSH token expired | 60-min TTL | Re-run `ssh-access` |
| `sudo apt-get` fails in sandbox | User may already be root | Try without sudo |
| rsync exit 255 | SSH closes abruptly after transfer | Normal — verify files instead of exit code |
| Toolbox 403 | Wrong owner | Confirm `SANDBOX_ID` belongs to `USER_KEY` |
| Sandbox create → 403 | `maxCpuPerSandbox=0` | Check `ADMIN_MAX_CPU_PER_SANDBOX` env |
| TEE key fetch fails | tapp-daemon unreachable | Use `MOCK_TEE=true` for dev |
| Settlement tx fails: `insufficient funds` | TEE address has no gas | Send 0G to TEE address for gas fees |
