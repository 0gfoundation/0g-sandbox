# CLI Reference

Two command-line tools are provided for operators and users:

| Tool | Role |
|------|------|
| `cmd/provider` | Provider operator: register/update service on-chain |
| `cmd/user` | End user: manage balance and sandboxes |

Private keys can be passed via `--key` flag or environment variable (`PROVIDER_KEY` / `USER_KEY`). The `0x` prefix is optional.

---

## `cmd/provider` — Provider Operations

### `register` / `init-service`

Bind a SandboxServing service to a TappRegistry appId and set its prices.
(`init-service` is an alias for `register`.)

Prerequisites (done on TappRegistry by the same provider wallet, in separate txs
via `tapp-cli`):

1. `tapp-cli register-onchain` — registers the app + first TEE node, pays stake.
2. `tapp-cli authorize-invalidator-onchain --invalidator <SETTLEMENT_CONTRACT>` —
   permits this contract to bump the app's ack version on price changes.

```bash
PROVIDER_KEY=0x<hex> go run ./cmd/provider/ register \
  --app-id      <tapp-app-id> \
  --url         <0g-sandbox-url> \
  [--price-per-cpu <neuron-per-cpu-per-minute>] \
  [--price-per-mem <neuron-per-gb-per-minute>] \
  [--fee        <create-fee-neuron>] \
  [--rpc        <rpc-url>] \
  [--chain-id   <chain-id>] \
  [--contract   <proxy-address>]
```

**Flags**

| Flag | Default | Description |
|------|---------|-------------|
| `--key` | `PROVIDER_KEY` env | Provider private key (hex). Must derive to the app owner registered in TappRegistry. |
| `--app-id` | (required) | TappRegistry appId this service is bound to. Must already be registered + have this contract authorized as an invalidator. |
| `--url` | (required) | Public URL of the billing proxy (e.g. `http://1.2.3.4:8080`) |
| `--price-per-cpu` | `1000000000000000` | Price per CPU core per minute (neuron) |
| `--price-per-mem` | `500000000000000` | Price per GB memory per minute (neuron) |
| `--fee` | `60000000000000000` | Flat fee per sandbox creation (neuron) |
| `--rpc` | `https://evmrpc-testnet.0g.ai` | EVM RPC endpoint |
| `--chain-id` | `16602` | Chain ID |
| `--contract` | deployed testnet addr | Settlement contract (BeaconProxy) address |

The contract verifies on call: `tap.getAppInfo(appId).owner == msg.sender` and
`tap.isAuthorizedInvalidator(appId, address(this)) == true`. Stake is not
collected here — TappRegistry holds per-node stake.

The appId field on the service is **set-once**: the first call writes it,
subsequent calls must pass the same value or revert. To switch to a different
appId, the operator must deregister and re-register.

**Example — testnet**

```bash
# Steps 1 + 2: register the app in TappRegistry + authorize this contract.
# See the `tapp-cli` documentation in the 0g-tapp repo for the full command set.

# Step 3: bind the service in SandboxServing.
PROVIDER_KEY=0x... go run ./cmd/provider/ register \
  --app-id        my-sandbox-app \
  --url           http://<provider-host>:8080 \
  --price-per-cpu 1000000000000000 \
  --price-per-mem 500000000000000 \
  --fee           60000000000000000
```

**Output**

```
Provider:       0xea69...1837
AppId:          my-sandbox-app
Contract:       0x<proxy-address>
Service URL:    http://<provider-host>:8080
CPU price/min:  1000000000000000 neuron
Mem price/min:  500000000000000 neuron/GB
Create fee:     60000000000000000 neuron

[1/1] AddOrUpdateService...
      tx: 0x...
      confirmed ✓

Done. Provider address: 0xea69...1837
```

> **After calling `register`**: set `PROVIDER_ADDRESS` in your `.env`, then redeploy the billing service.

---

### `status`

Show the current on-chain service registration, pricing, and earnings for a provider.

```bash
PROVIDER_KEY=0x<hex> go run ./cmd/provider/ status \
  [--address  <provider-address>] \
  [--rpc      <rpc-url>] \
  [--contract <proxy-address>]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--key` | `PROVIDER_KEY` env | Provider private key (address derived from it) |
| `--address` | — | Provider address (alternative to `--key`) |

TEE signer and per-node stake are managed in TappRegistry — query that
contract directly for the cluster view.

**Example**

```bash
PROVIDER_KEY=0x<hex> go run ./cmd/provider/ status
```

```
Provider:       0xea69...1837
Contract:       0x<proxy-address>
Registered:     true
Contract owner: 0x...

Service:
  URL:              http://<provider-host>:8080
  AppId:            my-sandbox-app
  CPU price/min:    1000000000000000 neuron
  Mem price/min:    500000000000000 neuron/GB
  Create fee:       60000000000000000 neuron
  Earnings:         5000000000000000000 neuron
```

---

### `withdraw`

Withdraw all accumulated earnings from the settlement contract to the provider wallet.

```bash
PROVIDER_KEY=0x<hex> go run ./cmd/provider/ withdraw \
  [--rpc      <rpc-url>] \
  [--chain-id <chain-id>] \
  [--contract <proxy-address>]
```

**Example**

```bash
PROVIDER_KEY=0x<hex> go run ./cmd/provider/ withdraw
```

```
Provider:  0xea69...1837
Earnings:  5000000000000000000 neuron

Withdrawing earnings...
  tx: 0x...
  confirmed ✓  (5000000000000000000 neuron withdrawn)
```

---

### `push-image`

Load a local Docker image into the deployment's internal registry via the runner
container. Required before registering a custom image as a snapshot.

```bash
go run ./cmd/provider/ push-image \
  --image   <local-image:tag> \
  [--name   <registry-name:tag>] \
  [--runner <runner-container>] \
  [--registry <registry-addr>]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--image` | (required) | Local Docker image name, e.g. `rust-sandbox:1.0.0` |
| `--name` | same as `--image` | Name to use inside the registry |
| `--runner` | `0g-sandbox-billing-runner-1` | Runner container name |
| `--registry` | `registry:6000` | Internal registry address |

Image tag must **not** be `:latest` — use an explicit version.

**Example**

```bash
# Build locally
docker build -t rust-sandbox:1.0.0 -f rust.Dockerfile .

# Push into internal registry
go run ./cmd/provider/ push-image --image rust-sandbox:1.0.0

# → prints the full registry path to use in the next step:
#   provider snapshot --image registry:6000/daytona/rust-sandbox:1.0.0 --name rust-sandbox
```

---

### Import Image (dashboard / HTTP API)

Pull an image from an external registry directly into the internal registry
(`registry:6000/daytona/`). This avoids the need to `docker save | docker load`
through the runner container, and works when the billing service runs inside a TEE.

**Via dashboard** — open `/dashboard`, go to the Provider tab, click **↓ Import Image**,
and fill in:

| Field | Description |
|-------|-------------|
| Source Image | Full image ref, e.g. `docker.io/library/alpine:3.19` |
| Username | Source registry username (leave blank for public images) |
| Password | Source registry password or token |
| Target Name | Name under `registry:6000/daytona/`, e.g. `my-image` |
| Target Tag | Version tag — must not be `latest` |

**Via HTTP API** (provider-only, EIP-191 auth required):

```bash
curl -X POST http://<provider-host>:8080/api/registry/pull \
  -H "Content-Type: application/json" \
  -H "X-Wallet-Address: 0x<provider-address>" \
  -H "X-Signed-Message: <base64-signed-msg>" \
  -H "X-Wallet-Signature: <sig>" \
  -d '{
    "src":      "docker.io/library/alpine:3.19",
    "name":     "alpine",
    "tag":      "3.19",
    "username": "",
    "password": ""
  }'
# → {"image":"registry:6000/daytona/alpine:3.19"}
```

> The pull runs synchronously and may take several minutes for large images.
> After import, use `snapshot` to register the image as a named snapshot.

---

### `snapshot`

Register a Docker image (already in the internal registry) as a named Daytona
snapshot. The snapshot becomes a base image users can choose when creating sandboxes.

```bash
PROVIDER_KEY=0x<hex> go run ./cmd/provider/ snapshot \
  --api    <0g-sandbox-url> \
  --image  <registry-image> \
  [--name   <snapshot-name>] \
  [--cpu    <cores>] \
  [--memory <gb>] \
  [--disk   <gb>] \
  [--tiers]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--api` | `http://localhost:8080` | 0G Sandbox service URL |
| `--image` | (required) | Full registry image path |
| `--name` | derived from `--image` | Snapshot name shown to users |
| `--cpu` | `1` | CPU cores for sandboxes using this snapshot |
| `--memory` | `1` | Memory in GB |
| `--disk` | `3` | Disk in GB |
| `--tiers` | false | Auto-create three variants: `<name>-small` (1C/1G/3G), `<name>-medium` (2C/4G/10G), `<name>-large` (4C/8G/20G) |
| `--key` | `PROVIDER_KEY` env | Provider private key |

**Example — single snapshot**

```bash
PROVIDER_KEY=0x<hex> go run ./cmd/provider/ snapshot \
  --api    http://<provider-host>:8080 \
  --image  registry:6000/daytona/rust-sandbox:1.0.0 \
  --name   rust-sandbox \
  --cpu    2 \
  --memory 4 \
  --disk   10
```

**Example — tiered snapshots**

```bash
PROVIDER_KEY=0x<hex> go run ./cmd/provider/ snapshot \
  --api   http://<provider-host>:8080 \
  --image registry:6000/daytona/rust-sandbox:1.0.0 \
  --name  rust-sandbox \
  --tiers
# → creates rust-sandbox-small, rust-sandbox-medium, rust-sandbox-large
```

Wait for `state: active` (Daytona pulls the image), then users can create sandboxes:

```bash
USER_KEY=0x<hex> go run ./cmd/user/ create \
  --api      http://<provider-host>:8080 \
  --snapshot rust-sandbox
```

---

### `delete-snapshot`

Delete a snapshot by its UUID. The UUID can be found via `provider snapshots` or
the provider dashboard.

```bash
PROVIDER_KEY=0x<hex> go run ./cmd/provider/ delete-snapshot \
  --api <0g-sandbox-url> \
  --id  <snapshot-uuid>
```

| Flag | Default | Description |
|------|---------|-------------|
| `--api` | `http://localhost:8080` | 0G Sandbox service URL |
| `--id` | (required) | Snapshot UUID (not name) |
| `--key` | `PROVIDER_KEY` env | Provider private key |

**Example**

```bash
PROVIDER_KEY=0x<hex> go run ./cmd/provider/ delete-snapshot \
  --api http://<provider-host>:8080 \
  --id  a1b2c3d4-e5f6-7890-abcd-ef1234567890
```

---

### `snapshots`

List all available snapshots.

```bash
PROVIDER_KEY=0x<hex> go run ./cmd/provider/ snapshots \
  --api <0g-sandbox-url>
```

---

## `cmd/user` — User Operations

### Chain subcommands

These interact directly with the settlement contract on-chain.

---

#### `balance`

Show a user's on-chain wallet balance. With `--provider`, also shows the contract balance for that provider, last nonce, and the provider's total accumulated earnings.

```bash
go run ./cmd/user/ balance \
  (--key <hex> | --address <wallet-address>) \
  [--provider <provider-address>] \
  [--rpc      <rpc-url>] \
  [--contract <proxy-address>]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--key` | `USER_KEY` env | User private key; address is derived from it |
| `--address` | — | Wallet address to check (alternative to `--key`) |
| `--provider` | — | If set, shows contract balance, nonce, and provider total earnings |

> `--key` or `--address` is required. `--provider` is strongly recommended — without it only the native wallet balance is shown.

**Example**

```bash
USER_KEY=0x<hex> go run ./cmd/user/ balance \
  --provider 0xB831371eb2703305f1d9F8542163633D0675CEd7
```

```
Address:          0xdAc113A24f4c7c57792B67127D99Fdda258e1023
Wallet balance:   10000000000000000 neuron  (0.010000 0G)  ← for gas
Contract balance: 9000000000000000 neuron   (0.009000 0G)  ← for sandbox (provider 0xB831...)
Nonce (vs provider): 3
Provider earnings: 50000000000000000 neuron  (0.050000 0G)  ← provider's total, all users
```

---

#### `deposit`

Deposit 0G tokens into the settlement contract to fund sandbox usage.

```bash
go run ./cmd/user/ deposit \
  --provider <provider-address> \
  [--key      <hex>] \
  [--amount   <float-0g>] \
  [--rpc      <rpc-url>] \
  [--contract <proxy-address>] \
  [--chain-id <chain-id>]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--provider` | (required) | Provider address to deposit for |
| `--key` | `USER_KEY` env | User private key |
| `--amount` | `0.01` | Amount to deposit **in 0G** (e.g. `0.01` = 10¹⁶ neuron) |

**Example**

```bash
USER_KEY=0x<hex> go run ./cmd/user/ deposit \
  --provider 0xB831371eb2703305f1d9F8542163633D0675CEd7 \
  --amount 0.1
```

```
User:     0xdAc113A24f4c7c57792B67127D99Fdda258e1023
Provider: 0xB831371eb2703305f1d9F8542163633D0675CEd7
Amount:   0.100000 0G (100000000000000000 neuron)
Contract: 0x24cD979DBd0Ae924a3f0c832a724CF4C58E5C210

[1/1] Deposit...
      tx: 0x...
      confirmed ✓

New balance (for provider 0xB831...): 100000000000000000 neuron  (0.100000 0G)
```

---

#### `acknowledge`

Acknowledge (or revoke) the provider's TEE identity. Required once per
`(user, app)` before creating sandboxes against that provider. The call
itself targets TappRegistry's `acknowledgeApp(appId)`; the CLI first resolves
`appId` from `sandbox.services(provider)` and prints both the commercial
terms (URL/prices/createFee from SandboxServing) and the trust root
(composeHash/imageHashes/active TEE nodes from TappRegistry) before
prompting.

```bash
go run ./cmd/user/ acknowledge \
  --provider <provider-address> \
  --tapp     <tapp-registry-address> \
  [--key     <hex>] \
  [--revoke] \
  [--yes] \
  [--rpc     <rpc-url>] \
  [--contract <proxy-address>] \
  [--chain-id <chain-id>]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--provider` | (required) | Sandbox-service provider address |
| `--tapp` | `TAPP_REGISTRY` env | TappRegistry contract address (required) |
| `--key` | `USER_KEY` env | User private key |
| `--revoke` | false | Revoke instead of acknowledge (calls `revokeAcknowledgement`) |
| `--yes` | false | Skip the interactive `[y/N]` prompt (use in scripts) |

**Example**

```bash
USER_KEY=0x<hex> go run ./cmd/user/ acknowledge \
  --provider 0x<provider> \
  --tapp     0x<tapp-registry>
```

```
== Provider commercial terms (SandboxServing 0x...) ==
  URL:           http://<provider-host>:8082
  AppId:         my-sandbox-app
  Create fee:    60000000000000000 neuron  (0.060000 0G per sandbox)
  CPU price:     1000000000000000 neuron/CPU/min
  Mem price:     500000000000000 neuron/GB/min

== Trust root (TappRegistry 0x...) ==
  App owner:     0xea69...1837   (matches provider ✓)
  Registered:    2026-06-02T03:14:48Z
  Ack version:   0
  Compose hash:  0x...
  Volumes hash:  0x...
  Image hashes:  (11)
                 sha256:c12037...
                 ...
  Active TEE nodes (1):
    0x0EB3...DB8
      teeUrl: http://<tapp-server>:50051

User:            0xdAc113A24f4c7c57792B67127D99Fdda258e1023
Current ack:     false

→ tapp.acknowledgeApp("my-sandbox-app")   on chain 16602
Proceed? [y/N]: y
      tx: 0x...
      confirmed ✓
```

> Whenever the provider triggers a TEE node change (via `tapp-cli`) or a
> price update in SandboxServing, the app's `ackVersion` bumps in
> TappRegistry and every user's prior ack becomes stale — they must call
> `acknowledge` again.

---

### API subcommands

These call the billing proxy over HTTP using EIP-191 signed requests.
All require `--api` (the 0G Sandbox service URL) and the user's private key.

Authentication uses three HTTP headers injected automatically by the CLI:

| Header | Content |
|--------|---------|
| `X-Wallet-Address` | User's Ethereum address |
| `X-Signed-Message` | Base64-encoded JSON `{action, expires_at, nonce, payload, resource_id}` |
| `X-Wallet-Signature` | EIP-191 signature over the message |

---

#### `create`

Create a new sandbox. Requires sufficient on-chain balance and prior `acknowledge`.

```bash
go run ./cmd/user/ create \
  --api        <0g-sandbox-url> \
  [--key       <hex>] \
  [--snapshot  <snapshot-name>] \
  [--name      <display-name>] \
  [--class     small|medium|large] \
  [--cpu       <cores>] \
  [--memory    <gb>] \
  [--disk      <gb>]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--api` | `http://localhost:8080` | 0G Sandbox service URL |
| `--key` | `USER_KEY` env | User private key |
| `--snapshot` | — | Snapshot name to use as the sandbox base |
| `--name` | — | Sandbox display name |
| `--class` | — | Preset resource class: `small`, `medium`, or `large` |
| `--cpu` | — | CPU cores (overrides `--class`) |
| `--memory` | — | Memory in GB (overrides `--class`) |
| `--disk` | — | Disk in GB (overrides `--class`) |
| `--sealed` | `false` | Create a sealed sandbox: injects TEE attestation, blocks SSH and toolbox access |

**Example**

```bash
# Standard sandbox
USER_KEY=0x<hex> go run ./cmd/user/ create --api http://<provider>:8080

# Sealed sandbox (SSH/toolbox blocked; TEE attestation injected)
USER_KEY=0x<hex> go run ./cmd/user/ create --api http://<provider>:8080 --sealed
```

---

#### `list`

List your sandboxes (filtered by owner — you only see your own).

```bash
go run ./cmd/user/ list \
  --api  <0g-sandbox-url> \
  [--key <hex>]
```

**Example**

```bash
USER_KEY=0x<hex> go run ./cmd/user/ list --api http://<provider-host>:8080
```

```json
{
  "id": "9c1d0f45-d7da-485d-8c70-e7f928491c00",
  "labels": { "daytona-owner": "0xdAc113..." },
  "state": "started"
}
```

---

#### `stop`

Stop a running sandbox. Only the sandbox owner can stop it.

```bash
go run ./cmd/user/ stop \
  --api  <0g-sandbox-url> \
  --id   <sandbox-id> \
  [--key <hex>]
```

**Example**

```bash
USER_KEY=0x<hex> go run ./cmd/user/ stop \
  --api http://<provider-host>:8080 \
  --id  9c1d0f45-d7da-485d-8c70-e7f928491c00
```

---

#### `delete`

Delete a sandbox. Only the sandbox owner can delete it.

```bash
go run ./cmd/user/ delete \
  --api  <0g-sandbox-url> \
  --id   <sandbox-id> \
  [--key <hex>]
```

---

#### `snapshots`

List snapshots available for use when creating sandboxes.

```bash
go run ./cmd/user/ snapshots \
  --api  <0g-sandbox-url> \
  [--key <hex>]
```

---

> **Note on `providers`:** This subcommand reads directly from the chain (not the billing proxy),
> so it takes `--rpc` / `--contract` / `--chain-id` instead of `--api`.

---

## Onboarding Flow

Complete flow for a new user to start using sandboxes:

```bash
# 1. Fund your wallet with 0G on testnet (faucet or transfer)

# 2. Deposit into the settlement contract (--provider required)
USER_KEY=0x<hex> go run ./cmd/user/ deposit \
  --provider 0x<provider-address> \
  --amount 0.1

# 3. Acknowledge the provider's trust root (lives in TappRegistry).
#    CLI prints prices + composeHash + active TEE nodes, then prompts.
USER_KEY=0x<hex> go run ./cmd/user/ acknowledge \
  --provider 0x<provider-address> \
  --tapp     0x<tapp-registry-address>

# 4. Create a sandbox
USER_KEY=0x<hex> go run ./cmd/user/ create --api http://<0g-sandbox>:8080

# 5. List your sandboxes
USER_KEY=0x<hex> go run ./cmd/user/ list --api http://<0g-sandbox>:8080

# 6. Stop when done
USER_KEY=0x<hex> go run ./cmd/user/ stop \
  --api http://<0g-sandbox>:8080 \
  --id  <sandbox-id>
```
