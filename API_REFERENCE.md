# 0G Sandbox — SDK & API Reference

> Billing proxy that authenticates users via EIP-191 wallet signatures, manages Daytona sandboxes,
> and settles usage fees on-chain in 0G tokens.

---

## Table of Contents

1. [Quick Start](#quick-start)
2. [Authentication](#authentication)
3. [HTTP API Reference](#http-api-reference)
4. [Toolbox API (Remote Execution)](#toolbox-api-remote-execution)
5. [CLI Reference](#cli-reference)
6. [On-Chain Contract API](#on-chain-contract-api)
7. [Data Types & Objects](#data-types--objects)
8. [Error Reference](#error-reference)
9. [Billing Concepts](#billing-concepts)

---

## Quick Start

### 1. Deposit funds on-chain

```bash
USER_KEY=0x<your-private-key> go run ./cmd/user/ deposit \
  --provider 0x<provider-address> \
  --amount 0.01 \
  --rpc https://evmrpc-testnet.0g.ai \
  --chain-id 16602 \
  --contract 0x<contract-address>
```

### 2. Acknowledge the TEE signer

```bash
USER_KEY=0x<your-private-key> go run ./cmd/user/ acknowledge \
  --provider 0x<provider-address> \
  --rpc https://evmrpc-testnet.0g.ai \
  --chain-id 16602 \
  --contract 0x<contract-address>
```

### 3. Create a sandbox

```bash
USER_KEY=0x<your-private-key> go run ./cmd/user/ create \
  --api http://<0g-sandbox>:8080
```

### 4. Run a command in the sandbox

```bash
USER_KEY=0x<your-private-key> go run ./cmd/user/ exec \
  --api http://<0g-sandbox>:8080 \
  --id <sandbox-id> \
  --cmd "python3 --version"
```

---

## Authentication

All `/api/` endpoints (except public ones) require three HTTP headers derived from an
**EIP-191** wallet signature.

### Required Headers

| Header | Format | Description |
|--------|--------|-------------|
| `X-Wallet-Address` | `0x<hex>` | Your Ethereum wallet address |
| `X-Signed-Message` | Base64 string | The signed request object, JSON-encoded then base64'd |
| `X-Wallet-Signature` | `0x<hex>` | 65-byte ECDSA signature (R\|\|S\|\|V, V in {27,28}) |

### Signed Request Object

Construct this JSON object and serialize it **with fields in this order**:

```json
{
  "action":      "create",
  "expires_at":  1709500000,
  "nonce":       "a3f8c2d1e4b7069512345678abcdef01",
  "payload":     {},
  "resource_id": ""
}
```

| Field | Type | Rules |
|-------|------|-------|
| `action` | string | Operation name: `create`, `list`, `stop`, `delete`, `toolbox`, etc. |
| `expires_at` | int64 | Unix timestamp (seconds). Must be `> now` and `≤ now + 5 minutes`. |
| `nonce` | string | 32-char hex (16 random bytes). Each nonce is accepted only once (stored in Redis until expiry). |
| `payload` | JSON | Request body as JSON object. Use `{}` for requests with no body. |
| `resource_id` | string | Sandbox ID for resource-specific operations; empty string for `create` / `list`. |

### Signing Algorithm

```
1. Build the SignedRequest JSON object (fields in order as shown above)
2. Serialize to UTF-8 JSON bytes  →  msgBytes
3. Compute EIP-191 hash:
     prefix = "\x19Ethereum Signed Message:\n" + len(msgBytes)   (decimal, not hex)
     hash   = keccak256(prefix + msgBytes)
4. ECDSA sign hash with your private key
5. Append V: sig = R||S||V where V ∈ {27, 28}  (add 27 if go-ethereum returns 0/1)
6. X-Signed-Message  = base64StdEncode(msgBytes)
   X-Wallet-Signature = "0x" + hex(sigBytes)
   X-Wallet-Address   = checksumHex(publicKeyToAddress(privKey))
```

### Go Implementation

```go
import (
    "crypto/rand"
    "encoding/base64"
    "encoding/hex"
    "encoding/json"
    "fmt"
    "time"

    "github.com/ethereum/go-ethereum/crypto"
)

type signedRequest struct {
    Action     string          `json:"action"`
    ExpiresAt  int64           `json:"expires_at"`
    Nonce      string          `json:"nonce"`
    Payload    json.RawMessage `json:"payload"`
    ResourceID string          `json:"resource_id"`
}

func signRequest(privKey *ecdsa.PrivateKey, action, resourceID string, payload json.RawMessage) (xSignedMessage, xSignature, xWalletAddr string) {
    addr := crypto.PubkeyToAddress(privKey.PublicKey)

    nonceBuf := make([]byte, 16)
    rand.Read(nonceBuf)

    req := signedRequest{
        Action:     action,
        ExpiresAt:  time.Now().Add(3 * time.Minute).Unix(),
        Nonce:      hex.EncodeToString(nonceBuf),
        Payload:    payload,
        ResourceID: resourceID,
    }
    msgBytes, _ := json.Marshal(req)

    prefix := fmt.Sprintf("\x19Ethereum Signed Message:\n%d", len(msgBytes))
    hash := crypto.Keccak256([]byte(prefix), msgBytes)

    sigBytes, _ := crypto.Sign(hash, privKey)
    sigBytes[64] += 27 // normalize V

    return base64.StdEncoding.EncodeToString(msgBytes),
        "0x" + hex.EncodeToString(sigBytes),
        addr.Hex()
}
```

### JavaScript / ethers.js Implementation

```js
import { ethers } from "ethers";

async function signRequest(wallet, action, resourceId, payload = {}) {
  const nonce = ethers.hexlify(ethers.randomBytes(16)).slice(2); // 32 hex chars
  const expiresAt = Math.floor(Date.now() / 1000) + 180; // 3 min

  const req = { action, expires_at: expiresAt, nonce, payload, resource_id: resourceId };
  const msgBytes = new TextEncoder().encode(JSON.stringify(req));

  // ethers.signMessage prepends the EIP-191 prefix automatically
  const signature = await wallet.signMessage(msgBytes);

  return {
    "X-Wallet-Address":   wallet.address,
    "X-Signed-Message":   btoa(String.fromCharCode(...msgBytes)),
    "X-Wallet-Signature": signature,
  };
}

// Usage
const wallet = new ethers.Wallet("0x<private-key>");
const headers = await signRequest(wallet, "create", "", {});
const resp = await fetch("http://<proxy>/api/sandbox", {
  method: "POST",
  headers: { ...headers, "Content-Type": "application/json" },
  body: JSON.stringify({}),
});
```

---

## HTTP API Reference

### Public Endpoints (no auth)

#### `GET /healthz`
Liveness probe.
```json
{ "ok": true }
```

#### `GET /api/info`
Server configuration and pricing.
```json
{
  "contract_address":     "0x...",
  "provider_address":     "0x...",
  "chain_id":             16602,
  "rpc_url":              "https://evmrpc-testnet.0g.ai",
  "create_fee":           "60000000000000000",
  "compute_price_per_sec":"0",
  "voucher_interval_sec": 60,
  "min_balance":          "60000000000000000",
  "sealed_only":          false
}
```

> `compute_price_per_sec` is the flat-rate fallback; it is `"0"` when per-resource pricing
> (`price_per_cpu_per_min` / `price_per_mem_gb_per_min`) is configured on-chain.
> `min_balance` = `create_fee + compute_price_per_sec × voucher_interval_sec`.
>
> `sealed_only` reflects the provider's `SEALED_ONLY` env. When `true`, every create
> request that doesn't set `"sealed": true` is rejected with HTTP 400.

#### `GET /api/providers`
On-chain service data for all known providers.
```json
[
  {
    "address":                "0x...",
    "url":                    "https://...",
    "app_id":                 "0g-sandbox-provider",
    "price_per_cpu_per_min":  "1000000000000000",
    "price_per_cpu_per_sec":  "16666666666666",
    "price_per_mem_gb_per_min":"500000000000000",
    "price_per_mem_gb_per_sec":"8333333333333",
    "create_fee":             "60000000000000000"
  }
]
```
All monetary amounts are in **neuron** (1 0G = 10¹⁸ neuron).

---

### Sandbox Endpoints (auth required)

#### `POST /api/sandbox` — Create sandbox

**Headers:** auth headers (action = `"create"`, resource_id = `""`)

**Body:**
```json
{
  "image":   "ubuntu:22.04",
  "sealed":  false
}
```
All fields are optional.

| Field | Type | Description |
|-------|------|-------------|
| `image` | string | Docker image or snapshot name to use |
| `sealed` | bool | If `true`, creates a sealed sandbox (see below) |

**Sealed sandboxes** (`"sealed": true`):
- Resolves the image to its content digest via the internal registry (hard failure if unresolvable)
- Generates an ephemeral secp256k1 keypair as the container's signing identity
- Injects two env vars into the container: `SANDBOX_SEAL_KEY` (private key; stripped from response) and `SANDBOX_SEAL_ATTESTATION` (JSON with `seal_id`, `pubkey`, `image_hash`, TEE `signature`, `ts`)
- Sets labels `0g-sealed: "true"` and `0g-seal-id: <32-char hex>`
- **Blocks SSH and toolbox access** for the sandbox's lifetime

**Provider-side `SEALED_ONLY=true`:** when the operator sets this env on the
billing server, every create request **without** `"sealed": true` is rejected
with HTTP 400 (`{"error": "this provider only accepts sealed sandboxes; set \"sealed\": true in the create request"}`). Check `GET /api/info`'s `sealed_only` field upfront to know.

**Response `200`:** Sandbox object (see [Data Types](#data-types--objects))

**Billing:** Deducts CREATE_FEE immediately. Minimum balance required:
`CREATE_FEE + COMPUTE_PRICE_PER_SEC × VOUCHER_INTERVAL_SEC`

---

#### `GET /api/sandbox` — List sandboxes

**Headers:** auth headers (action = `"list"`, resource_id = `""`)

**Response `200`:** Array of sandbox objects filtered to the caller's own sandboxes.

Also available as `GET /api/sandbox/paginated` with the same semantics.

---

#### `GET /api/sandbox/:id` — Get sandbox

**Headers:** auth headers (action = `"list"`, resource_id = `":id"`)

**Response `200`:** Sandbox object
**Response `403`:** Not the owner

---

#### `POST /api/sandbox/:id/stop` — Stop sandbox

**Headers:** auth headers (action = `"stop"`, resource_id = `":id"`)

**Response `200`:** Response from Daytona
**Billing:** Emits a final compute voucher for elapsed time since last voucher.

---

#### `DELETE /api/sandbox/:id` — Delete sandbox

**Headers:** auth headers (action = `"delete"`, resource_id = `":id"`)

**Response `200`:** Response from Daytona
**Billing:** Emits a final compute voucher before deletion.

---

#### `POST /api/sandbox/:id/start` — Start a stopped sandbox

**Headers:** auth headers (action = `"start"`, resource_id = `":id"`)

**Response `200`:** Response from Daytona
**Response `402`:** TEE signer not acknowledged
**Billing:** Opens a new compute session.

---

#### `POST /api/sandbox/:id/archive` — Archive sandbox

**Headers:** auth headers (action = `"archive"`, resource_id = `":id"`)

**Response `200`:** Response from Daytona
**Billing:** Emits a final compute voucher; closes compute session.

---

#### `PUT /api/sandbox/:id/labels` — Update labels

**Headers:** auth headers (action = `"labels"`, resource_id = `":id"`)

**Body:**
```json
{ "my-label": "value" }
```
> Note: `daytona-owner` is protected and cannot be overwritten.

---

#### `POST /api/sandbox/:id/ssh-access` — Get SSH access token

**Headers:** auth headers (action = `"ssh-access"`, resource_id = `":id"`)

**Response `200`:**
```json
{
  "sshCommand": "ssh -p 2222 user@<host> -i <key>",
  "token":      "<short-lived-token>"
}
```
**Response `403`:** Sandbox is sealed (`"sealed": true` at create time) — SSH access is permanently blocked.

---

#### `POST /api/sandbox/:id/ensure-billing` — Ensure billing session exists

**Headers:** auth headers (action = `"ensure-billing"`, resource_id = `":id"`)

Idempotent. Ensures a billing session exists for a sandbox whose `OnCreate` hook may not have
fired (e.g., client disconnected before the 2xx response arrived).

**Response `200`:**
```json
{ "ok": true }
```

> **Blocked endpoints:** `POST /api/sandbox/:id/autostop` and
> `POST /api/sandbox/:id/autoarchive` return `403 Forbidden` — these lifecycle
> policies are managed by the billing proxy and cannot be overridden by users.

---

### Event & Session Endpoints (auth required)

#### `GET /api/events` — Query settlement events

**Headers:** auth headers (action = `"list"`, resource_id = `""`)

**Query params:** `?lookback=<blocks>` (default ≈ 43200, ~24h at 2s/block; `0` = all history)

**Response `200`:**
```json
{
  "current_block": 7700000,
  "from_block":    7656800,
  "events": [
    {
      "user":      "0x...",
      "provider":  "0x...",
      "total_fee": "60001200",
      "nonce":     "42",
      "status":    "SUCCESS",
      "tx_hash":   "0x...",
      "block":     7654321,
      "timestamp": 1709500000
    }
  ]
}
```

---

#### `GET /api/sessions` — Active billing sessions (admin only)

**Headers:** auth headers (action = `"list"`, resource_id = `""`)

Caller wallet must be in `ADMIN_ADDRESSES`.

**Response `200`:** Array of session objects (see [Data Types](#data-types--objects))

---

#### `DELETE /api/sessions/:id` — Close one billing session (admin only)

Deletes the Redis session for one sandbox and deregisters it from the broker.
The sandbox itself is not stopped or deleted — use this to clean up an orphan
session left behind when a sandbox was archived outside the billing-proxy
control path. Idempotent: succeeds even if no session exists.

**Response `200`:** `{"ok": true}`

---

#### `POST /api/archive-all` — Archive all running sandboxes (admin only)

Used before redeployment. Stops then archives all `started`/`starting` sandboxes;
archives `stopped` sandboxes directly.

**Response `200`:**
```json
{ "archived": ["id1", "id2"], "skipped": [], "failed": [] }
```

---

#### `DELETE /api/sandbox/:id/force` — Force-delete any sandbox (admin only)

Operator-intent override of `DELETE /api/sandbox/:id`. Deletes regardless of owner.
Kept distinct from the standard delete (which also accepts admin via
`withOwnerOrAdmin`) so operator overrides are easy to grep in audit logs.

**Response `200`:** Response from Daytona
**Billing:** Emits a final compute voucher.

---

#### `POST /api/sandbox/:id/force-stop` — Force-stop any sandbox (admin only)

Operator-intent override of `POST /api/sandbox/:id/stop`. Synchronous: blocks
until Daytona reports the sandbox in stopped/archived/error state so a
follow-up start cannot race the in-progress stop.

**Response `200`:** `{"id": "...", "state": "stopped"}`
**Billing:** Triggers `OnStop` and broker deregister.

---

#### `GET /api/audit-log` — Local Redis billing event log (admin only)

Append-only stream of billing-relevant events (created / stopped / auto_stopped /
settled). Distinct from `GET /api/events`, which reads on-chain `VoucherSettled`
logs — this one captures broker-proxy-side state changes that may never reach
chain (e.g. stopped before first voucher).

**Response `200`:** Array of `{timestamp, kind, sandbox_id, owner, …}` entries.

---

#### Queue + observability (admin only)

These power the operator dashboard; reachable from `cmd/user` only with an
admin wallet.

| Path | Purpose |
|---|---|
| `GET /api/queue/summary` | Pending voucher count per `(user, provider)` |
| `GET /api/queue/dlq` | Vouchers in the dead-letter queue (signature mismatch / provider mismatch) |
| `POST /api/queue/dlq/discard` | Discard a DLQ entry by signature digest |
| `POST /api/queue/aggregate` | Collapse pending vouchers for one `(user, provider)` into a single voucher |
| `GET /api/observability` | Queue depth + recent alert history |

---

## Toolbox API (Remote Execution)

The toolbox proxy forwards requests to the Daytona toolbox inside a sandbox, with ownership
verification. Path format: `/api/toolbox/{sandboxId}/toolbox/{action}`.

**Note:** Returns `403 Forbidden` for sealed sandboxes (`0g-sealed: "true"`).

**Auth headers:** action = `"toolbox"`, resource_id = `"{sandboxId}"`

All HTTP methods (GET, POST, PUT, DELETE) are supported.

### Common Actions

| Action | Method | Description |
|--------|--------|-------------|
| `process/execute` | POST | Execute a shell command |
| `files` | GET | List files |
| `files/download` | GET | Download a file (`?path=<path>`) |
| `files/upload` | POST | Upload a file |
| `files/find` | GET | Search for files |
| `git/status` | GET | Git status |
| `git/clone` | POST | Clone a repository |
| `git/commit` | POST | Git commit |
| `git/push` | POST | Git push |
| `git/pull` | POST | Git pull |
| `project-dir` | GET | Get project directory path |
| `user-home-dir` | GET | Get user home directory |

### `POST /api/toolbox/:id/toolbox/process/execute`

**Body:**
```json
{ "command": "echo hello", "timeout": 30 }
```

**Response `200`:**
```json
{ "exitCode": 0, "result": "hello\n" }
```

### Example: List files via curl

```bash
curl -X GET "http://<proxy>/api/toolbox/<sandbox-id>/toolbox/files" \
  -H "X-Wallet-Address: 0x..." \
  -H "X-Signed-Message: <base64>" \
  -H "X-Wallet-Signature: 0x..."
```

---

## CLI Reference

The `cmd/user` binary provides a reference client for both on-chain and proxy operations.
Set `USER_KEY=0x<private-key>` as an environment variable to avoid passing `--key` every time.

For local testing, avoid typing private keys directly into long shell commands.
Prefer a local env file that is not committed:

```bash
printf 'export USER_KEY=0x<key>\n' > .env.user
chmod 600 .env.user
source .env.user
```

### Chain Subcommands

#### `balance` — Check account balance

```bash
USER_KEY=0x<key> go run ./cmd/user/ balance \
  [--address 0x<address>]           # defaults to key's address
  [--provider 0x<provider>]         # shows per-provider nonce and earnings if set
  [--rpc <url>]                     # default: https://evmrpc-testnet.0g.ai
  [--chain-id <id>]                 # default: 16602
  [--contract 0x<addr>]             # default: deployed contract on 0G Galileo
```

#### `deposit` — Deposit 0G tokens

```bash
USER_KEY=0x<key> go run ./cmd/user/ deposit \
  --amount 0.01 \                   # in 0G (e.g., 0.01 = 10^16 neuron)
  [--rpc <url>] [--chain-id <id>] [--contract 0x<addr>]
```

#### `acknowledge` — Acknowledge TEE signer

Users must acknowledge a provider's TEE signer before the provider can charge their account.

```bash
USER_KEY=0x<key> go run ./cmd/user/ acknowledge \
  --provider 0x<provider-address> \
  [--revoke]                        # pass to revoke instead of acknowledge
  [--rpc <url>] [--chain-id <id>] [--contract 0x<addr>]
```

---

### API Subcommands

Most API subcommands require `--api <0g-sandbox-url>` and `USER_KEY` env var. Exception: `providers` reads directly from the chain.

#### `providers` — List available providers

Reads on-chain service registrations directly — **no `--api` flag**, no authentication required.

```bash
go run ./cmd/user/ providers \
  [--rpc      <rpc-url>] \
  [--chain-id <chain-id>] \
  [--contract <proxy-address>]
```

Output:
```
Found 2 provider(s) on-chain:

[1] 0xB831371eb2703305f1d9F8542163633D0675CEd7
    URL:         http://<provider-host>:8080
    Create fee:  0.0600 0G
    CPU price:   0.000017 0G/CPU/sec  (0.0010 0G/CPU/min)
    Mem price:   0.000008 0G/GB/sec   (0.0005 0G/GB/min)
    TEE signer:  0x3Dc1A35f37FBfDb82900A00d209b4f3a2124E99d (v5)
```

Use the provider address shown here for `balance`, `acknowledge`, and `deposit`.

#### `create` — Create a sandbox

```bash
USER_KEY=0x<key> go run ./cmd/user/ create \
  --api http://<proxy>:8080 \
  [--snapshot <snapshot-name>] \
  [--name     <display-name>] \
  [--class    small|medium|large] \
  [--cpu      <cores>] \
  [--memory   <gb>] \
  [--disk     <gb>]
```

#### `list` — List sandboxes

```bash
USER_KEY=0x<key> go run ./cmd/user/ list --api http://<proxy>:8080
```

#### `start` — Start a stopped sandbox

```bash
USER_KEY=0x<key> go run ./cmd/user/ start \
  --api http://<proxy>:8080 \
  --id <sandbox-id>
```

#### `stop` — Stop a sandbox

```bash
USER_KEY=0x<key> go run ./cmd/user/ stop \
  --api http://<proxy>:8080 \
  --id <sandbox-id>
```

#### `delete` — Delete a sandbox

```bash
USER_KEY=0x<key> go run ./cmd/user/ delete \
  --api http://<proxy>:8080 \
  --id <sandbox-id>
```

#### `exec` — Execute a shell command

```bash
USER_KEY=0x<key> go run ./cmd/user/ exec \
  --api http://<proxy>:8080 \
  --id <sandbox-id> \
  --cmd "python3 -c \"print('hello')\"" \
  [--timeout 30]                    # seconds, default 30
```

Output: stdout/stderr of the command. Exits with the command's exit code.

For shell features such as `&&`, pipes, redirects, globs, or environment
expansion, wrap the command explicitly:

```bash
USER_KEY=0x<key> go run ./cmd/user/ exec \
  --api http://<proxy>:8080 \
  --id <sandbox-id> \
  --cmd "sh -lc 'mkdir -p /tmp/demo && ls -ld /tmp/demo'"
```

#### `toolbox` — Arbitrary toolbox API call

```bash
USER_KEY=0x<key> go run ./cmd/user/ toolbox \
  --api http://<proxy>:8080 \
  --id <sandbox-id> \
  --action <action-path> \          # e.g. files, git/status, process/execute
  [--method GET|POST|PUT|DELETE] \  # default GET
  [--body '{"key":"value"}']        # JSON body for POST/PUT
```

**Examples:**
```bash
# List files
USER_KEY=0x<key> go run ./cmd/user/ toolbox --api http://<proxy>:8080 --id <id> --action files

# Git status
USER_KEY=0x<key> go run ./cmd/user/ toolbox --api http://<proxy>:8080 --id <id> --action git/status

# Execute process
USER_KEY=0x<key> go run ./cmd/user/ toolbox --api http://<proxy>:8080 --id <id> \
  --action process/execute --method POST --body '{"command":"ls -la","timeout":10}'
```

#### `ssh-access` — Get temporary SSH access token

Token valid for 60 minutes. The CLI prints the SSH command to stdout and the
temporary password token to stderr. Quote the token when passing it to
automation, because it may contain shell metacharacters.

```bash
USER_KEY=0x<key> go run ./cmd/user/ ssh-access \
  --api http://<proxy>:8080 \
  --id <sandbox-id>
# stdout: ssh -p 2222 <user>@<host>
# stderr: Password: <token>
```

Use for direct SSH or rsync sync:
```bash
SSH_OUTPUT=$(USER_KEY=0x<key> go run ./cmd/user/ ssh-access --api http://<proxy>:8080 --id <id> 2>&1)
SSH_CMD=$(printf '%s\n' "$SSH_OUTPUT" | grep '^ssh ')
TOKEN=$(printf '%s\n' "$SSH_OUTPUT" | awk '/^Password:/ {print $2}')
PORT=$(echo $SSH_CMD | awk '{print $3}')
USER_HOST=$(echo $SSH_CMD | awk '{print $4}')

# Direct SSH
ssh -p $PORT -o StrictHostKeyChecking=no $USER_HOST
```

Some provider SSH gateways may not support `scp` or the rsync remote protocol.
Test with a small file before relying on rsync for project sync:

```bash
# Rsync local directory to sandbox
sshpass -p "$TOKEN" rsync -avz --delete -e "ssh -p $PORT -o StrictHostKeyChecking=no" \
  ./my-project/ "${USER_HOST}:/home/daytona/project/"
```

If rsync closes the connection or hangs, use the toolbox `files/upload` endpoint
or `cmd/user exec` with a verified archive upload until first-class CLI upload
support is available.

---

## On-Chain Contract API

The settlement contract is a `BeaconProxy → UpgradeableBeacon → SandboxServing` stack deployed
on the 0G Galileo testnet (chain ID 16602).

### Key Functions (SandboxServing ABI)

#### `deposit(address recipient, address provider)`

Deposit 0G tokens into a user's account for a specific provider.
```
payable; msg.value = amount in neuron (wei)
```

#### Acknowledging a provider (TappRegistry)

The provider's TEE trust root lives in TappRegistry, not in SandboxServing.
Users call `tappRegistry.acknowledgeApp(string appId)` to accept it and
`tappRegistry.revokeAcknowledgement(string appId)` to revoke. The current
`appId` for a provider is read from `sandbox.services(provider).appId`.

When the active TEE node set changes, or when SandboxServing's prices/createFee
are updated, TappRegistry bumps `ackVersion(appId)` and prior acknowledgements
are invalidated automatically — users must re-acknowledge before further
vouchers settle.

#### `getBalance(address user, address provider) → (balance, pendingRefund, refundUnlockAt)`

Read-only. Returns user balance and refund state (in neuron) for a specific provider.

#### `getLastNonce(address user, address provider) → uint256`

Read-only. Returns the last settled nonce for a `(user, provider)` pair.

#### `getProviderEarnings(address provider) → uint256`

Read-only. Returns total neuron earned by a provider.

#### `services(address provider) → Service`

Read-only. Returns provider registration details (public mapping auto-getter):
```solidity
struct Service {
    string  url;
    string  appId;
    uint256 pricePerCPUPerMin;
    uint256 pricePerMemGBPerMin;
    uint256 createFee;
}
```

#### `settleFeesWithTEE(SandboxVoucher[] vouchers, bytes[] signatures)`

Called by the provider's settler. Users do not call this directly.

---

## Data Types & Objects

### Sandbox Object

```json
{
  "id":    "6f3a1b2c-...",
  "state": "started",
  "labels": {
    "daytona-owner": "0x1234...abcd",
    "0g-sealed":     "true",
    "0g-seal-id":    "a3f8c2d1e4b706951234567890abcdef"
  }
}
```

| Field | Type | Values |
|-------|------|--------|
| `id` | string | UUID |
| `state` | string | `started`, `stopped`, `starting`, `stopping`, `archived`, `error` |
| `labels["daytona-owner"]` | string | Owner wallet address (hex) |
| `labels["0g-sealed"]` | string | `"true"` if the sandbox was created with `sealed: true`; absent otherwise |
| `labels["0g-seal-id"]` | string | 32-char hex identifier correlating the sandbox to its TEE attestation; absent for non-sealed sandboxes |

The `SANDBOX_SEAL_KEY` env var injected at create time is **stripped from all API responses** — it is only ever visible inside the container itself.

### Proxy URL

User-defined service ports inside a sandbox (e.g. 8080, 9090) are accessible via the Daytona
proxy URL when the server is configured with `PROXY_DOMAIN`:

```
http://<port>-<sandboxId>.<PROXY_DOMAIN>/<path>
```

Examples:
- `PROXY_DOMAIN=<your-ip>.nip.io:4000` → `http://8080-<id>.<your-ip>.nip.io:4000/`
- `PROXY_DOMAIN=sandbox.yourdomain.com` → `http://8080-<id>.sandbox.yourdomain.com/`

System ports (22222/TERMINAL, 2280/TOOLBOX, 33333/RECORDING) are not accessible via this URL
regardless of the `public` flag.

---

### Provider Info Object

```json
{
  "address":                 "0x...",
  "url":                     "https://...",
  "app_id":                  "0g-sandbox-provider",
  "price_per_cpu_per_min":   "1000000000000000",
  "price_per_cpu_per_sec":   "16666666666666",
  "price_per_mem_gb_per_min":"500000000000000",
  "price_per_mem_gb_per_sec":"8333333333333",
  "create_fee":              "60000000000000000"
}
```

All monetary values in **neuron** (string).

---

### VoucherSettled Event

```json
{
  "user":      "0x...",
  "provider":  "0x...",
  "total_fee": "60001200",
  "nonce":     "42",
  "status":    "SUCCESS",
  "tx_hash":   "0x...",
  "block":     7654321,
  "timestamp": 1709500000
}
```

---

### Billing Session Object

```json
{
  "sandbox_id":     "6f3a1b2c-...",
  "owner":          "0x...",
  "state":          "started",
  "start_time":     1709490000,
  "last_voucher_at":1709496000,
  "accrued_neuron": "100002000"
}
```

---

## Error Reference

All errors return JSON: `{ "error": "<message>" }`

### HTTP Status Codes

| Code | Cause |
|------|-------|
| `400 Bad Request` | Missing required fields or malformed request body |
| `401 Unauthorized` | Missing/invalid auth headers, expired signature (`expires_at ≤ now`), signature too far in future (`expires_at > now + 5min`), nonce already used |
| `402 Payment Required` | Insufficient balance to create sandbox, or TEE signer not acknowledged |
| `403 Forbidden` | Sandbox is owned by a different wallet; or provider-only endpoint; or managed endpoint (`autostop`/`autoarchive`) |
| `500 Internal Server Error` | Redis error or unexpected failure |
| `502 Bad Gateway` | Upstream Daytona or chain RPC error |

### Auth Error Messages

| `error` field | Cause |
|---------------|-------|
| `missing auth headers` | One or more of the three headers is absent |
| `invalid X-Signed-Message encoding` | Base64 decode failed |
| `invalid signed message JSON` | JSON parse of decoded bytes failed |
| `request expired` | `expires_at ≤ now` |
| `expires_at too far in future` | `expires_at > now + 5min` |
| `invalid signature` | ECDSA recovery failed or recovered address ≠ `X-Wallet-Address` |
| `nonce already used` | This nonce was seen before (replay protection) |

---

## Billing Concepts

### Token Units

| Unit | Value |
|------|-------|
| 1 neuron | 10⁻¹⁸ 0G (smallest unit, like wei) |
| 1 0G | 10¹⁸ neuron |

All API amounts use **neuron** as `string` (to avoid integer overflow in JSON).

### Billing Lifecycle

```
User calls POST /api/sandbox
  → proxy checks min balance (CREATE_FEE + one interval worth of compute)
  → forwards to Daytona with daytona-owner label injected
  → billing.OnCreate() emits create-fee voucher + opens compute session in Redis

Every VOUCHER_INTERVAL_SEC:
  → RunGenerator() finds all open sessions
  → emits compute vouchers: elapsed_sec × COMPUTE_PRICE_PER_SEC neuron each

settler.Run() drains voucher queue:
  → previews settlement status on-chain
  → submits SettleFeesWithTEE() in batches
  → on INSUFFICIENT_BALANCE: writes stop:sandbox:<id> key to Redis

runStopHandler():
  → reads stop keys from Redis
  → calls Daytona stop on the sandbox
  → cleans up session and stop keys
```

### Minimum Balance

A sandbox creation is rejected unless:

```
user_balance ≥ CREATE_FEE + COMPUTE_PRICE_PER_SEC × VOUCHER_INTERVAL_SEC
```

With defaults (`CREATE_FEE=5000000`, `COMPUTE_PRICE_PER_SEC=16667`, `VOUCHER_INTERVAL_SEC=3600`):

```
min_balance = 5_000_000 + 16_667 × 3_600 = 65_001_200 neuron ≈ 6.5 × 10⁻¹¹ 0G
```

### Settlement Status Codes

| Code | Name | Meaning |
|------|------|---------|
| `0` | `SUCCESS` | Settled; balance deducted |
| `1` | `INSUFFICIENT_BALANCE` | Balance too low; sandbox will be auto-stopped |
| `2` | `PROVIDER_MISMATCH` | Voucher's provider ≠ tx sender |
| `3` | `NOT_ACKNOWLEDGED` | User has not acknowledged this provider's TEE signer |
| `4` | `INVALID_NONCE` | Nonce ≤ last settled nonce (must be strictly increasing) |
| `5` | `INVALID_SIGNATURE` | TEE signature verification failed |

### Voucher Structure (EIP-712)

Vouchers are signed by the TEE key inside the enclave:

```solidity
SandboxVoucher {
    address user,
    address provider,
    bytes32 usageHash,   // keccak256(sandboxID, periodStart, periodEnd, elapsedSec)
    uint256 nonce,       // per-(user,provider) counter, strictly increasing
    uint256 totalFee     // charge in neuron
}
```

| Field | Description |
|-------|-------------|
| `user` | The wallet address being charged |
| `provider` | The provider's wallet address (identified from voucher, not checked against `msg.sender`) |
| `usageHash` | Opaque usage fingerprint: `keccak256(sandboxID ‖ periodStart ‖ periodEnd ‖ elapsedSec)` |
| `nonce` | Strictly increasing per `(user, provider)` pair; seeded from chain on startup |
| `totalFee` | `elapsedSec × COMPUTE_PRICE_PER_SEC` for compute vouchers; `CREATE_FEE` for create vouchers |

The domain separator uses:
```
name    = "SandboxServing"
version = "1"
chainId = <chain ID>
verifyingContract = <settlement contract address>
```

Users never construct or verify vouchers directly — the proxy handles this automatically.
