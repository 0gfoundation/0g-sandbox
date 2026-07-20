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

**Prerequisite** — set up the `0g-user` CLI:

```bash
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m); [ "$ARCH" = "x86_64" ] && ARCH=amd64; [ "$ARCH" = "aarch64" ] && ARCH=arm64
if command -v 0g-user &>/dev/null; then
  export USER_CLI=0g-user
elif [ -x "$HOME/.local/bin/0g-user" ]; then
  export USER_CLI="$HOME/.local/bin/0g-user"
else
  mkdir -p ~/.local/bin
  curl -fsSL "https://github.com/0gfoundation/0g-sandbox/releases/latest/download/0g-user_${OS}_${ARCH}" \
    -o ~/.local/bin/0g-user && chmod +x ~/.local/bin/0g-user
  export USER_CLI="$HOME/.local/bin/0g-user"
fi
echo "CLI ready: $USER_CLI"
```

---

**0. Do you have a Broker URL?**

A broker aggregates provider discovery and exposes chain config — you don't need to know the network details manually.

- `yes` — provide the broker URL (hosted testnet broker: `https://private-sandbox-testnet.0g.ai`)
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

**No wallet yet** — generate one (use whichever tool is available):

```bash
# Option 1: Foundry cast (recommended)
cast wallet new

# Option 2: Node.js with ethers
node -e "const {ethers}=require('ethers'); const w=ethers.Wallet.createRandom(); console.log('Private Key:',w.privateKey,'\nAddress:    ',w.address)"

# Option 3: MetaMask / Rabby / any EVM wallet — export the private key after creating
```

Tell the user to save their private key securely — it will not be shown again.

**Have a wallet** — ask: "Please tell me your wallet **address** (not the private key) so I can check your balance."

### Step 1 — Discover providers

`providers` reads on-chain service registrations directly — there is **no `--api` flag**. It uses the `RPC_URL` and `SETTLEMENT_CONTRACT` you set during session setup (from the broker's `/api/info` on the broker path, or from your answers on the direct path), so the same command works for both paths:

```bash
$USER_CLIproviders
```

To target a different network or contract without changing those env vars, pass the chain flags explicitly:

```bash
$USER_CLIproviders --rpc "$RPC_URL" --contract "$SETTLEMENT_CONTRACT"
```

After picking a provider, update `API` to the selected provider URL (from the export commands printed by the command). From this point on, `API` always points to the provider, not the broker.

The command scans the chain and prints available providers with their URL, pricing, and TEE signer. Example output:

```
[1] 0xf982279B872B9a99d64C547a0faC2Dfdfc2AEE5D
    URL:        https://provider-private-sandbox-testnet.0g.ai
    Create fee: 0.0600 0G
    CPU price:  0.001000 0G/CPU/min
    Mem price:  0.000500 0G/GB/min
```

> The provider address IS the node's TEE signer (v2 identity model) — there is
> no separate signer field. Deposits and billing are bound to this address.

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
$USER_CLIbalance --address <wallet-address> --provider $PROVIDER
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
$USER_CLIdeposit --provider $PROVIDER --amount <amount>
```

Confirm balance again after deposit.

### Step 3 — Acknowledge TEE signer (first-time only)

Only required when contract balance was 0 before depositing:

```bash
$USER_CLIacknowledge --provider $PROVIDER
```

### Step 4 — Check for existing sandboxes

```bash
$USER_CLIlist --api $API
```

- **Sandboxes found** → show list, ask: reuse one or create new?
  - Reuse: `$USER_CLIstart --api $API --id <id>`
  - Create new → Step 5
- **No sandboxes** → Step 5 directly

### Step 5 — Understand goal, recommend snapshot

First ask:
> "What do you want to use this sandbox for?"

Then list available snapshots:
```bash
$USER_CLIsnapshots --api $API
```

Standard snapshots (specs are FIXED by the snapshot — `--cpu/--memory/--disk`
cannot be combined with `--snapshot`; the API rejects it):

| Snapshot | Specs | For |
|----------|-------|-----|
| **0g-ubuntu22** | 1 CPU / 1 GB | General vibe coding, running code, light services |
| **0g-openclaw** | 2 CPU / 4 GB | AI coding assistant (OpenClaw gateway) in a secure sandbox |

Based on goal, recommend:

| User goal | Recommendation |
|-----------|----------------|
| General vibe coding / running code | **0g-ubuntu22** snapshot (1C/1G) |
| AI coding assistant in secure sandbox | **0g-openclaw** snapshot (2C/4G) |
| Specific environment (Rust, Python…) | Match from snapshot list |
| Needs custom CPU/memory | Create WITHOUT `--snapshot` and pass `--cpu/--memory/--disk` (subject to the provider's per-sandbox limits) |

**STOP and present recommendation:**
> "Based on your goal, I recommend **[snapshot]**: [one-line description].
> Shall I use this? Or do you have another preference?"

Wait for confirmation before proceeding.

### Step 6 — Create sandbox

```bash
# Without snapshot
$USER_CLIcreate --api $API --name <friendly-name>

# With snapshot
$USER_CLIcreate --api $API --name <friendly-name> --snapshot <name>

# Sealed sandbox — TEE attestation injected, SSH and toolbox permanently blocked.
# MUST include --snapshot (bare --sealed is rejected: sealing resolves the image
# digest, so the image must exist in the provider's internal registry).
$USER_CLIcreate --api $API --name <friendly-name> --sealed --snapshot <name>

# Restrict public ports — only listed ports are reachable without auth;
# all other ports require the owner's preview token. Omit = all ports public.
$USER_CLIcreate --api $API --name <friendly-name> --snapshot <name> --ports 8000,3000
# Rules: max 16 ports; system ports 22222/2280/33333 rejected; immutable after
# create; sealed + --ports must include 8080. The create response returns
# ready-to-use `preview_urls` for each listed port.
```

Copy the returned sandbox ID:
```bash
export SANDBOX_ID=<returned-id>
```

Billing starts immediately. **Do NOT wait for the sandbox to be ready** — start the mode discussion while it starts up. If user chose **openclaw** → skip to **OpenClaw Mode**.

### Step 6b — Accessing sandbox service ports

If the provider has `PROXY_DOMAIN` configured, user-defined service ports are reachable at:
```
<protocol>://<port>-<sandboxId>.<PROXY_DOMAIN>/<path>
```
The protocol matches the provider's setup (the hosted testnet uses **https**).
For example, if a process listens on port 8000 inside the sandbox:
```
https://8000-<sandboxId>.provider-private-sandbox-testnet.0g.ai/
```
Prefer the `preview_urls` field from the create response — it is already the
correct protocol + domain. Requires the provider to have wildcard DNS + TLS for
`*.<PROXY_DOMAIN>`; if the subdomain doesn't resolve, report it to the provider.

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
$USER_CLIexec --api $API --id $SANDBOX_ID --cmd "which rsync"
```

If the output is empty (not found), install it:
```bash
$USER_CLIexec --api $API --id $SANDBOX_ID \
  --cmd "sh -c 'apt-get update -qq && apt-get install -y rsync'"
```

Confirm installed before proceeding:
```bash
$USER_CLIexec --api $API --id $SANDBOX_ID --cmd "rsync --version"
```

### Step 2 — Test rsync protocol support (MANDATORY before syncing)

The sandbox SSH shell may be restricted and block the rsync protocol even when rsync binary is present. Always test first:

```bash
export LOCAL_DIR=/path/to/your/project
export REMOTE_DIR=~/workspace

# Get SSH credentials
# NOTE: use 2>&1 — "Password:" line comes from stderr
SSH_OUTPUT=$($USER_CLIssh-access --api $API --id $SANDBOX_ID 2>&1)
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

**If `RSYNC_OK=0` → upload via exec + base64 instead:**

> Daytona v0.189 changed `files/upload` to multipart form (JSON base64 body no
> longer works, and the CLI toolbox command only sends JSON). Write files
> through `exec` instead:

```bash
# Upload a single file
FILE=main.py
$USER_CLIexec --api $API --id $SANDBOX_ID \
  --cmd "sh -c 'mkdir -p $REMOTE_DIR && echo $(base64 -w0 $LOCAL_DIR/$FILE) | base64 -d > $REMOTE_DIR/$FILE'"

# For multiple files — loop over them
for FILE in $(find $LOCAL_DIR -type f -not -path '*/.git/*' -not -name '*.pyc'); do
  REL=${FILE#$LOCAL_DIR/}
  $USER_CLIexec --api $API --id $SANDBOX_ID \
    --cmd "sh -c 'mkdir -p $REMOTE_DIR/$(dirname $REL) && echo $(base64 -w0 $FILE) | base64 -d > $REMOTE_DIR/$REL'"
done
```

### Run code after sync

```bash
$USER_CLIexec --api $API --id $SANDBOX_ID \
  --cmd "sh -c 'cd $REMOTE_DIR && python3 main.py'"
```

From now on: agent only **edits local files + execs in sandbox**. Never manually rsync again. For watch mode with toolbox: upload only the changed file after each edit.

---

## Mode B — Direct Remote Execution

```bash
# Clone repo into sandbox
$USER_CLItoolbox \
  --api $API --id $SANDBOX_ID \
  --method POST --action git/clone \
  --body '{"url":"https://github.com/org/repo","path":"/home/daytona/project"}'

# Install deps and run
$USER_CLIexec --api $API --id $SANDBOX_ID \
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
> **You'll need Anthropic credentials.** Two DIFFERENT kinds — do not mix them up:
> - **API key** (`sk-ant-api…`, from https://console.anthropic.com/settings/keys)
>   → export as `ANTHROPIC_API_KEY` before starting the gateway.
> - **Claude Code OAuth token** (`sk-ant-oat…`, from `claude setup-token`)
>   → this is NOT an API key and must NOT go into `ANTHROPIC_API_KEY`.
>   Register it inside the sandbox via `openclaw models auth add` (paste-token
>   flow) after the gateway is set up.
>
> **Choose setup method:**
> - `A` — **Agent-assisted**: I'll run all steps and give you the SSH tunnel command
> - `B` — **Self-configure via SSH**: I'll give you a token and a step-by-step tutorial
>
> Choice (A or B)?

Proceed with Step 1.

**Step 1 — Configure gateway (mode + auth token), then start**

If using an **API key**, verify it is set in the terminal (`echo $ANTHROPIC_API_KEY`);
if empty, stop and ask the user to set it. (OAuth-token users skip this — their
credential is registered in Step 2b instead.)

> Current openclaw (2026.x) **refuses to bind to lan without gateway auth**
> ("Refusing to bind gateway to lan without auth") — the auth token is no longer
> auto-generated on first start. Set it explicitly BEFORE starting. Also:
> `nohup … &` inside `exec` dies with the exec session — use `setsid`.

```bash
$USER_CLIexec --api $API --id $SANDBOX_ID \
  --cmd "/bin/bash -c 'openclaw config set gateway.mode local && openclaw config set gateway.auth.mode token && openclaw config set gateway.auth.token \$(openssl rand -hex 16)'"

$USER_CLIexec --api $API --id $SANDBOX_ID \
  --cmd "/bin/bash -c 'export ANTHROPIC_API_KEY=$ANTHROPIC_API_KEY; setsid nohup openclaw gateway run --bind lan --port 3284 </dev/null >/tmp/openclaw.log 2>&1 & echo launched'"
```

Wait a few seconds, confirm it is actually up (the process must survive the exec
session — if `port3284:000`, it died; check /tmp/openclaw.log):
```bash
$USER_CLIexec --api $API --id $SANDBOX_ID \
  --cmd "/bin/bash -c 'sleep 5; curl -s -o /dev/null -w \"port3284:%{http_code}\" http://127.0.0.1:3284/'"
```

**Step 2 — Get gateway auth token**

```bash
$USER_CLIexec --api $API --id $SANDBOX_ID \
  --cmd "/bin/bash -c 'node -e \"console.log(require(\\\"/root/.openclaw/openclaw.json\\\").gateway.auth.token)\"'"
```

**Step 2b — (OAuth-token users only) register the Claude Code token**

```bash
$USER_CLIexec --api $API --id $SANDBOX_ID \
  --cmd "/bin/bash -c 'openclaw models auth add'"   # paste the sk-ant-oat… token when prompted
```
(If the interactive flow doesn't work over exec, do this step via SSH — Option B.)

Show the gateway token clearly, then say:
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
$USER_CLIssh-access --api $API --id $SANDBOX_ID
```

Present the SSH token and the following exact steps to the user:

```bash
# 1. SSH into the sandbox (use the token from ssh-access output as password)
ssh -p <PORT> -o StrictHostKeyChecking=no '<TOKEN>@<HOST>'

# 2. Inside the sandbox — set gateway mode + auth token, then start.
#    API-key users: export ANTHROPIC_API_KEY first.
#    OAuth-token users (claude setup-token): do NOT export it as ANTHROPIC_API_KEY —
#    run `openclaw models auth add` and paste the sk-ant-oat… token instead.
openclaw config set gateway.mode local
openclaw config set gateway.auth.mode token
openclaw config set gateway.auth.token $(openssl rand -hex 16)
export ANTHROPIC_API_KEY=sk-ant-api-<your-key>   # API-key users only
setsid nohup openclaw gateway run --bind lan --port 3284 </dev/null >/tmp/openclaw.log 2>&1 &
# Press Enter once to return to prompt, then:
sleep 5 && curl -s -o /dev/null -w "port3284:%{http_code}\n" http://127.0.0.1:3284/

# 3. Get auth token
node -e "console.log(require('/root/.openclaw/openclaw.json').gateway.auth.token)"

# 4. Exit sandbox, then in a new terminal — SSH tunnel (port forward)
ssh -N -L 13284:localhost:3284 -p 2222 -o StrictHostKeyChecking=no '<TOKEN>@<HOST>' &

# 5. Open browser
# http://localhost:13284/#token=<token-from-step-3>
```

**Important:** `ANTHROPIC_API_KEY` must be exported as an env var **before** starting the gateway — there is no `openclaw config set` for it. Do NOT suggest `openclaw config set anthropic.*`.

**Gotchas:**
- Must set `gateway.mode local` AND `gateway.auth.mode token` + `gateway.auth.token`
  before starting — current openclaw refuses lan bind without auth
- Backgrounding: plain `nohup … &` dies when the exec session ends —
  use `setsid nohup … </dev/null >log 2>&1 &`
- `ANTHROPIC_API_KEY` accepts real API keys (sk-ant-api…) ONLY.
  `claude setup-token` output (sk-ant-oat…) is an OAuth token → register via
  `openclaw models auth add`, never via ANTHROPIC_API_KEY
- Do NOT use `openclaw config set anthropic.*` (invalid key path, will error)
- Gateway token is at `gateway.auth.token` in /root/.openclaw/openclaw.json
- Browser URL must use `#token=` hash fragment
- SSH token expires 60 min → re-run `ssh-access` to refresh tunnel
- Stop gateway: `exec --cmd "/bin/bash -c 'pkill -f openclaw'"`

---

## Sandbox Management

```bash
# List
$USER_CLIlist --api $API

# Start stopped sandbox
$USER_CLIstart --api $API --id $SANDBOX_ID

# Stop (pauses billing)
$USER_CLIstop --api $API --id $SANDBOX_ID

# Delete (permanent)
$USER_CLIdelete --api $API --id $SANDBOX_ID

# SSH access (60-min token)
$USER_CLIssh-access --api $API --id $SANDBOX_ID

# Check balance (use address, not key)
$USER_CLIbalance --address <wallet-address> --provider $PROVIDER
```

---

## Toolbox Quick Reference

```bash
$USER_CLItoolbox \
  --api $API --id $SANDBOX_ID \
  [--method POST] --action <action> [--body '<json>']
```

| Action | Method | Description |
|--------|--------|-------------|
| `files` | GET | List files |
| `files/upload` | POST | ⚠ v0.189: multipart form — NOT usable via `--body` JSON; use exec+base64 instead |
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
| `NOT_ACKNOWLEDGED` after price/node change | `ackVersion` bumped in TappRegistry | Re-run `acknowledge` |
| Sandbox state is `stopped` | Auto-stopped or billing stopped it | Run `start` before `exec` |
| SSH token expired | 60-min TTL | Re-run `ssh-access` |
| `sudo apt-get` fails in sandbox | User may already be root | Try without sudo |
| rsync exit 255 | SSH closes after transfer | Normal — verify files instead |
| rsync `connection unexpectedly closed (code 12)` | Sandbox SSH is restricted shell, blocks rsync protocol | Use toolbox upload fallback |
| Toolbox 403 | Wrong owner | Confirm `SANDBOX_ID` belongs to this `USER_KEY` |
| `exec` output missing env vars | No login shell | Wrap: `sh -c '. ~/.cargo/env && ...'` |
