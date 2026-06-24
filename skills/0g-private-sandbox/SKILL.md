---
name: 0g-private-sandbox
description: Use this skill when a user wants to create, use, or manage sandboxes on 0G Private Sandbox — including depositing balance, vibe coding (rsync/remote), and using the OpenClaw AI gateway.
version: 2.0.0
author: 0G Labs
tags: [0g, sandbox, daytona, vibe-coding, user]
repository: https://github.com/0gfoundation/0g-sandbox
---

# 0G Private Sandbox — User Skill

---

## MANDATORY: Session setup

Detect the language of the user's message and respond in that language throughout the entire session.

Output the following verbatim and wait for all answers before doing anything else:

---

Before we begin, I need a few details.

**Prerequisite** — commands require Go. Check it's available:

```bash
export PATH=$PATH:/usr/local/go/bin
go version
```

If Go is missing:
```bash
curl -L https://go.dev/dl/go1.23.4.linux-amd64.tar.gz | sudo tar -C /usr/local -xz
export PATH=$PATH:/usr/local/go/bin
```

---

**0. Do you have a Broker URL?**

A broker aggregates provider discovery and exposes chain config — you don't need to know the network details manually.

- `yes` — provide the broker URL (e.g. `https://broker.example.com`)
- `no` — I'll configure network and contract manually

**If yes → broker path:**

```bash
export API=<broker-url>
```

Fetch chain config from the broker (no manual input needed):
```bash
curl $API/api/info
# Returns: contract_address, chain_id, rpc_url
```

Set variables from the response:
```bash
export RPC_URL=<rpc_url from response>
export CHAIN_ID=<chain_id from response>
export SETTLEMENT_CONTRACT=<contract_address from response>
```

Skip Q1 and Q2. Proceed to **User Onboarding**.

---

**If no → direct path, answer both questions:**

**1. Which network?**
- `1` — 0G Galileo Testnet
- `2` — 0G Mainnet
- `3` — Custom (provide RPC URL and Chain ID)

**2. Contract address (SETTLEMENT_CONTRACT)**
Please provide the settlement contract address. Type `default` if you are unsure and I will suggest the standard address for your network.

Please answer →

---

After receiving answers for Q1/Q2, set variables:

```bash
export PATH=$PATH:/usr/local/go/bin
# Network 1 (Testnet):
export RPC_URL=https://evmrpc-testnet.0g.ai
export CHAIN_ID=16602

# Network 2 (Mainnet):
export RPC_URL=https://evmrpc.0g.ai
export CHAIN_ID=16661

export SETTLEMENT_CONTRACT=<confirmed-address>
```

If user typed `default` for Q2, present the standard address for their chosen network and ask them to confirm before proceeding.

Then proceed to **User Onboarding**.

---

## User Onboarding

### Step 0 — Wallet

**No wallet yet** — generate one:

```bash
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

Tell the user to save their private key securely — it will not be shown again.

**Have a wallet** — ask: "Please tell me your wallet **address** (not the private key) so I can check your balance."

### Step 1 — Discover providers

`providers` reads on-chain service registrations directly — there is **no `--api` flag**. It uses the `RPC_URL` and `SETTLEMENT_CONTRACT` you set during session setup (from the broker's `/api/info` on the broker path, or from your answers on the direct path), so the same command works for both paths:

```bash
go run ./cmd/user/ providers
```

To target a different network or contract without changing those env vars, pass the chain flags explicitly:

```bash
go run ./cmd/user/ providers \
  --rpc "$RPC_URL" --contract "$SETTLEMENT_CONTRACT"
```

After picking a provider, update `API` to the selected provider URL (from the export commands printed by the command). From this point on, `API` always points to the provider, not the broker.

The command scans the chain and prints available providers with their URL, pricing, and TEE signer. Example output:

```
[1] 0xB831371eb2703305f1d9F8542163633D0675CEd7
    URL:        http://<provider-host>:8080
    Create fee: 0.0600 0G
    CPU price:  0.001000 0G/CPU/sec
    Mem price:  0.000500 0G/GB/sec
    TEE signer: 0x61BEb835... (v4)
```

It also outputs ready-to-use export commands — have the user run them:

```bash
export PROVIDER=<provider-address>
export API=<provider-url>
```

- Single provider → use it automatically
- Multiple providers → show the list and ask user to pick

Note the create fee and per-resource pricing for the chosen provider — you will need them in Step 2.

### Step 2 — Balance check & deposit

Use the wallet address (not the private key) to check balance:

```bash
go run ./cmd/user/ balance --address <wallet-address> --provider $PROVIDER
```

Output:
```
Wallet balance:  X neuron  (X.XX 0G)  ← for gas
Contract balance:Y neuron  (Y.YY 0G)  ← for sandbox billing
```

Show the provider's pricing from Step 1 and ask: "Would you like to deposit? If so, how much 0G?"

At minimum the balance must cover the **create fee** (shown in Step 1 output) plus compute for the intended usage time. Recommended: **1–10 0G** for comfortable usage.

If depositing:
```bash
go run ./cmd/user/ deposit --provider $PROVIDER --amount <amount>
```

Confirm balance again after deposit.

### Step 3 — Acknowledge TEE signer (first-time only)

Only required when contract balance was 0 before depositing:

```bash
go run ./cmd/user/ acknowledge --provider $PROVIDER
```

### Step 4 — Check for existing sandboxes

```bash
go run ./cmd/user/ list --api $API
```

- **Sandboxes found** → show list, ask: reuse one or create new?
  - Reuse: `go run ./cmd/user/ start --api $API --id <id>`
  - Create new → Step 5
- **No sandboxes** → Step 5 directly

### Step 5 — Understand goal, recommend snapshot

First ask:
> "What do you want to use this sandbox for?"

Then list available snapshots:
```bash
go run ./cmd/user/ snapshots --api $API
```

Based on goal, recommend:

| User goal | Recommendation |
|-----------|----------------|
| General vibe coding / running code | Default image (no snapshot) |
| AI coding assistant in secure sandbox | **openclaw** snapshot |
| Specific environment (Rust, Python…) | Match from snapshot list |

**STOP and present recommendation:**
> "Based on your goal, I recommend **[snapshot]**: [one-line description].
> Shall I use this? Or do you have another preference?"

Wait for confirmation before proceeding.

### Step 6 — Create sandbox

```bash
# Without snapshot
go run ./cmd/user/ create --api $API --name <friendly-name>

# With snapshot
go run ./cmd/user/ create --api $API --name <friendly-name> --snapshot <name>

# Sealed sandbox — TEE attestation injected, SSH and toolbox permanently blocked
go run ./cmd/user/ create --api $API --name <friendly-name> --sealed
```

Copy the returned sandbox ID:
```bash
export SANDBOX_ID=<returned-id>
```

Billing starts immediately. **Do NOT wait for the sandbox to be ready** — start the mode discussion while it starts up. If user chose **openclaw** → skip to **OpenClaw Mode**.

### Step 6b — Accessing sandbox service ports

If the provider has `PROXY_DOMAIN` configured, user-defined service ports are reachable at:
```
http://<port>-<sandboxId>.<PROXY_DOMAIN>/<path>
```
For example, if a process listens on port 8080 inside the sandbox:
```
http://8080-<sandboxId>.sandbox.example.com/
```
The provider's `/info` endpoint lists `proxy_domain` when configured.

> **Note:** Sealed sandboxes (`--sealed`) block SSH and toolbox access — use the proxy URL to reach services running inside them.

### Step 7 — Recommend vibe coding mode

| User's goal | Recommendation |
|-------------|----------------|
| Starting a new project from scratch | **Mode A** — Claude edits locally, rsync syncs to sandbox |
| Modifying an existing local project | **Mode A** — local code + remote execution |
| Running an existing GitHub project | **Mode B** — git clone into sandbox and run directly |
| Quick one-off command | **Mode B** — remote exec directly |
| Sealed execution environment | **Mode B** + proxy URL — no SSH/toolbox; services via proxy URL |

Present the recommendation and wait for confirmation, then proceed.

---

## Mode A — Local AI + Remote Execution (rsync)

Known issues:
- `exec` does not invoke a shell — `&&`, `||` not interpreted. Always wrap with `sh -c '...'`.
- `apt-get` inside sandbox may require `sudo`.

### Step 1 — Verify rsync is available (MANDATORY before syncing)

**Local machine:**
```bash
which rsync sshpass
# If missing:
sudo apt-get install -y rsync sshpass   # Ubuntu/Debian
brew install rsync sshpass               # macOS
```

**Sandbox — check and install if missing:**
```bash
go run ./cmd/user/ exec --api $API --id $SANDBOX_ID --cmd "which rsync"
```

If the output is empty (not found), install it:
```bash
go run ./cmd/user/ exec --api $API --id $SANDBOX_ID \
  --cmd "sh -c 'apt-get update -qq && apt-get install -y rsync'"
```

Confirm installed before proceeding:
```bash
go run ./cmd/user/ exec --api $API --id $SANDBOX_ID --cmd "rsync --version"
```

### Step 2 — Test rsync protocol support (MANDATORY before syncing)

The sandbox SSH shell may be restricted and block the rsync protocol even when rsync binary is present. Always test first:

```bash
export LOCAL_DIR=/path/to/your/project
export REMOTE_DIR=~/workspace

# Get SSH credentials
# NOTE: use 2>&1 — "Password:" line comes from stderr
SSH_OUTPUT=$(go run ./cmd/user/ ssh-access --api $API --id $SANDBOX_ID 2>&1)
SSH_LINE=$(echo "$SSH_OUTPUT" | grep '^ssh ')
PORT=$(echo "$SSH_LINE" | grep -o '\-p [0-9]*' | awk '{print $2}')
USER_HOST=$(echo "$SSH_LINE" | awk '{print $NF}')
TOKEN=$(echo "$SSH_OUTPUT" | grep '^Password:' | awk '{print $2}')

# ⚠️ Known issue: SSH via domain may hang. Replace domain with the provider's direct IP if needed:
# USER_HOST=$(echo "$USER_HOST" | sed 's/<provider-domain>/<provider-ip>/')

# Test rsync protocol — create a tiny test file and try to sync it
echo "test" > /tmp/_rsync_test.txt
RSYNC_TEST=$(sshpass -p $TOKEN rsync -q \
  -e "ssh -p $PORT -o StrictHostKeyChecking=no -o BatchMode=no" \
  /tmp/_rsync_test.txt $USER_HOST:/tmp/_rsync_test.txt 2>&1)

if echo "$RSYNC_TEST" | grep -q "connection unexpectedly closed\|rsync error"; then
  echo "⚠️  rsync protocol not supported by this sandbox SSH — use toolbox upload instead (see below)"
  export RSYNC_OK=0
else
  echo "✅ rsync works — proceeding with sync"
  export RSYNC_OK=1
fi
rm -f /tmp/_rsync_test.txt
```

**If `RSYNC_OK=1` → proceed with rsync sync:**

```bash
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

**If `RSYNC_OK=0` → use toolbox upload instead:**

```bash
# Upload a single file
FILE=main.py
go run ./cmd/user/ toolbox \
  --api $API --id $SANDBOX_ID \
  --method POST --action files/upload \
  --body "{\"path\":\"$REMOTE_DIR/$FILE\",\"content\":\"$(base64 -w0 $LOCAL_DIR/$FILE)\"}"

# For multiple files — loop over them
for FILE in $(find $LOCAL_DIR -type f -not -path '*/.git/*' -not -name '*.pyc'); do
  REL=${FILE#$LOCAL_DIR/}
  go run ./cmd/user/ toolbox \
    --api $API --id $SANDBOX_ID \
    --method POST --action files/upload \
    --body "{\"path\":\"$REMOTE_DIR/$REL\",\"content\":\"$(base64 -w0 $FILE)\"}"
done
```

### Run code after sync

```bash
go run ./cmd/user/ exec --api $API --id $SANDBOX_ID \
  --cmd "sh -c 'cd $REMOTE_DIR && python3 main.py'"
```

From now on: agent only **edits local files + execs in sandbox**. Never manually rsync again. For watch mode with toolbox: upload only the changed file after each edit.

---

## Mode B — Direct Remote Execution

```bash
# Clone repo into sandbox
go run ./cmd/user/ toolbox \
  --api $API --id $SANDBOX_ID \
  --method POST --action git/clone \
  --body '{"url":"https://github.com/org/repo","path":"/home/daytona/project"}'

# Install deps and run
go run ./cmd/user/ exec --api $API --id $SANDBOX_ID \
  --cmd "sh -c 'cd /home/daytona/project && pip install -r requirements.txt && python main.py'"
```

---

## OpenClaw Mode — AI Coding in Secure Sandbox

For users who chose the **openclaw** snapshot. OpenClaw is an AI coding gateway (powered by Claude) running privately inside the sandbox — code and API key never leave it.

Present the setup plan and ask how the user wants to proceed:

> Here's how we'll set up OpenClaw:
>
> 1. **Start Gateway** — start openclaw gateway inside the sandbox (port 3284)
> 2. **Get Token** — read the auth token auto-generated by the gateway
> 3. **SSH Tunnel** — forward local port to sandbox — you run this on your **local machine**
> 4. **Open Browser** — visit `http://localhost:13284/#token=<token>`
>
> **You'll need an Anthropic API Key** (`ANTHROPIC_API_KEY`) set in your terminal:
> - **Claude Code users**: run `claude setup-token` to get your key
> - **Others**: visit https://console.anthropic.com/settings/keys
>
> **Choose setup method:**
> - `A` — **Agent-assisted**: I'll run all steps and give you the SSH tunnel command
> - `B` — **Self-configure via SSH**: I'll give you a token and a step-by-step tutorial
>
> Choice (A or B)?

Proceed with Step 1.

**Step 1 — Set gateway mode + start**

Before running, verify `ANTHROPIC_API_KEY` is set in the terminal:
```bash
echo $ANTHROPIC_API_KEY
```

If the output is empty, **stop and ask the user to set it** before continuing:
> "`ANTHROPIC_API_KEY` is not set. Please set it in your terminal (`export ANTHROPIC_API_KEY=sk-ant-...`) and let me know when done."

Only proceed once confirmed non-empty.

```bash
go run ./cmd/user/ exec --api $API --id $SANDBOX_ID \
  --cmd "/bin/bash -c 'openclaw config set gateway.mode local'"

go run ./cmd/user/ exec --api $API --id $SANDBOX_ID \
  --cmd "/bin/bash -c 'export ANTHROPIC_API_KEY=$ANTHROPIC_API_KEY; nohup bash -c \"openclaw gateway run --bind lan --port 3284 > /tmp/openclaw.log 2>&1\" &'"
```

Wait 3s, confirm running:
```bash
go run ./cmd/user/ exec --api $API --id $SANDBOX_ID \
  --cmd "/bin/bash -c 'sleep 3 && grep \"listening on\" /tmp/openclaw.log'"
```

**Step 2 — Get gateway auth token**

```bash
go run ./cmd/user/ exec --api $API --id $SANDBOX_ID \
  --cmd "/bin/bash -c 'node -e \"console.log(require(\\\"/root/.openclaw/openclaw.json\\\").gateway.auth.token)\"'"
```

Show the token clearly, then say:
> Token retrieved: `<token>`
> Next, run the following command on your **local machine**, then let me know when it's running.

**Step 3 — SSH tunnel (user runs on local machine)**

```bash
ssh -N -L 13284:localhost:3284 -p 2222 -o StrictHostKeyChecking=no '<SSH_TOKEN>@HOST' &
```

> ⚠️ Use port **13284** (not 3284) — local 3284 may already be in use.
> ⚠️ zsh users: wrap SSH token in **single quotes** — `!` triggers history expansion.
> ⚠️ Known issue: SSH via domain hangs on port 2222. Replace HOST with the provider's direct IP if it hangs.

**Step 4 — Open in browser**

> **http://localhost:13284/#token=`<openclaw-token>`**
> Must use `#token=...` (hash fragment), NOT `?token=...`.

**Option B — Self-configure via SSH**

```bash
go run ./cmd/user/ ssh-access --api $API --id $SANDBOX_ID
```

Present the SSH token and the following exact steps to the user:

```bash
# 1. SSH into the sandbox (use the token from ssh-access output as password)
ssh -p <PORT> -o StrictHostKeyChecking=no '<TOKEN>@<HOST>'

# 2. Inside the sandbox — set gateway mode and start with API key as env var
openclaw config set gateway.mode local
export ANTHROPIC_API_KEY=sk-ant-<your-key>
nohup bash -c "openclaw gateway run --bind lan --port 3284 > /tmp/openclaw.log 2>&1" &
# Shell prints "[1] <pid>" — press Enter once to return to prompt, then:
sleep 3 && grep "listening on" /tmp/openclaw.log

# 3. Get auth token
node -e "console.log(require('/root/.openclaw/openclaw.json').gateway.auth.token)"

# 4. Exit sandbox, then in a new terminal — SSH tunnel (port forward)
ssh -N -L 13284:localhost:3284 -p 2222 -o StrictHostKeyChecking=no '<TOKEN>@<HOST>' &

# 5. Open browser
# http://localhost:13284/#token=<token-from-step-3>
```

**Important:** `ANTHROPIC_API_KEY` must be exported as an env var **before** starting the gateway — there is no `openclaw config set` for it. Do NOT suggest `openclaw config set anthropic.*`.

**Gotchas:**
- Must run `openclaw config set gateway.mode local` before starting
- `nohup openclaw ... &` does NOT work — wrap with `nohup bash -c '...' &`
- `ANTHROPIC_API_KEY` must be passed as env var when starting gateway — do NOT use `openclaw config set anthropic.*` (invalid key, will error)
- Token is at `gateway.auth.token` in config JSON
- Browser URL must use `#token=` hash fragment
- SSH token expires 60 min → re-run `ssh-access` to refresh tunnel
- Stop gateway: `exec --cmd "/bin/bash -c 'pkill -f openclaw'"`

---

## Sandbox Management

```bash
# List
go run ./cmd/user/ list --api $API

# Start stopped sandbox
go run ./cmd/user/ start --api $API --id $SANDBOX_ID

# Stop (pauses billing)
go run ./cmd/user/ stop --api $API --id $SANDBOX_ID

# Delete (permanent)
go run ./cmd/user/ delete --api $API --id $SANDBOX_ID

# SSH access (60-min token)
go run ./cmd/user/ ssh-access --api $API --id $SANDBOX_ID

# Check balance (use address, not key)
go run ./cmd/user/ balance --address <wallet-address> --provider $PROVIDER
```

---

## Toolbox Quick Reference

```bash
go run ./cmd/user/ toolbox \
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

## Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| `insufficient balance` on create | Balance < 0.12 0G | `deposit` more |
| `deposit: insufficient funds` | Wallet has no 0G | Transfer 0G to wallet first |
| `GetBalance` revert | Wrong contract address | Check `SETTLEMENT_CONTRACT` |
| `NOT_ACKNOWLEDGED` after price change | `signerVersion` incremented | Re-run `acknowledge` |
| Sandbox state is `stopped` | Auto-stopped or billing stopped it | Run `start` before `exec` |
| SSH token expired | 60-min TTL | Re-run `ssh-access` |
| `sudo apt-get` fails in sandbox | User may already be root | Try without sudo |
| rsync exit 255 | SSH closes after transfer | Normal — verify files instead |
| rsync `connection unexpectedly closed (code 12)` | Sandbox SSH is restricted shell, blocks rsync protocol | Use toolbox upload fallback |
| Toolbox 403 | Wrong owner | Confirm `SANDBOX_ID` belongs to this `USER_KEY` |
| `exec` output missing env vars | No login shell | Wrap: `sh -c '. ~/.cargo/env && ...'` |
