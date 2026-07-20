---
name: 0g-private-sandbox-provider
description: Use this skill when a provider wants to deploy, register, or operate their 0G Private Sandbox service — covering tapp-cli deployment, on-chain registration, node rotation, snapshot management, resource quotas, earnings withdrawal, sandbox monitoring, and settlement history.
version: 3.0.0
author: 0G Labs
tags: [0g, sandbox, provider, registration, snapshot, tapp]
repository: https://github.com/0gfoundation/0g-sandbox
---

# 0G Private Sandbox — Provider Skill

## Identity model (v2 — read this first)

- The **provider address** on-chain is the node's **TEE signer address** — its
  key lives inside the enclave, never leaves, and changes when the machine is
  rebuilt (service restarts keep it). It is the ledger identity: voucher payee,
  user balance bucket, earnings account.
- The **app owner** (the appId's TappRegistry owner wallet) does ALL
  management: register/remove services, withdraw earnings, rotate nodes,
  dashboard/admin APIs. Export it as `OWNER_KEY` (legacy alias `PROVIDER_KEY`
  still accepted).
- Nothing identity-related is configured in `.env`: the billing service derives
  its provider address from the TEE key and resolves the owner from
  `getAppInfo(BACKEND_APP_NAME).owner` at runtime. The owner is always an
  admin; `ADMIN_ADDRESSES` adds extra operator wallets (additive).

---

## MANDATORY: Session setup

Detect the language of the user's message and respond in that language throughout the entire session.

First, set up the `0g-provider` CLI (run silently before asking anything):

```bash
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m); [ "$ARCH" = "x86_64" ] && ARCH=amd64; [ "$ARCH" = "aarch64" ] && ARCH=arm64
if command -v 0g-provider &>/dev/null; then
  export PROVIDER_CLI=0g-provider
elif [ -x "$HOME/.local/bin/0g-provider" ]; then
  export PROVIDER_CLI="$HOME/.local/bin/0g-provider"
else
  mkdir -p ~/.local/bin
  curl -fsSL "https://github.com/0gfoundation/0g-sandbox/releases/latest/download/0g-provider_${OS}_${ARCH}" \
    -o ~/.local/bin/0g-provider && chmod +x ~/.local/bin/0g-provider
  export PROVIDER_CLI="$HOME/.local/bin/0g-provider"
fi
echo "CLI ready: $PROVIDER_CLI"
```

Then output the following verbatim and wait for the answer:

---

What would you like to do?

**A — First-time setup**
Deploy the billing service and register it on-chain for the first time.

**B — Update service registration**
Update URL or pricing on an already-deployed service.

**C — Rotate after a machine rebuild**
The TEE signer changed — migrate the service to the new signer safely.

**D — Day-to-day operations**
- `snapshot` — Register, import, list, or delete sandbox images
- `withdraw` — Withdraw a node's earnings to the owner wallet
- `quota` — Set machine-wide resource caps (oversell control)
- `monitor` — View active billing sessions, force-delete sandboxes, archive all
- `events` — View voucher settlement history
- `status` — Check a node's registration and earnings

Type A, B, C, or the operation name →

---

After receiving the answer, proceed to the relevant section below.

---

## A — First-time Setup

Full sequence: deploy → get the node's TEE signer → register on-chain (owner-signed) → verify.

### Step 1 — Deploy the billing service

Use the **0g-tapp-cli skill** to deploy. Key points:

> - App ID (e.g. `0g-sandbox-provider`) must equal `BACKEND_APP_NAME` in `.env` —
>   it is the same string as the TappRegistry appId.
> - `.env` needs no wallet addresses (see Identity model). Optional:
>   `ADMIN_ADDRESSES` for extra operator wallets.
> - ⚠ Every `.env` change alters the app's `volumesHash` → redeploy +
>   `update-onchain` bumps `ackVersion` → **all users must re-acknowledge**.
>   Batch config changes; don't drip them.

```bash
tapp-cli -s http://<tapp-server>:50051 start-app -f docker-compose.yml --app-id <app-id>
tapp-cli -s http://<tapp-server>:50051 get-task-status --task-id <task-id>
```

### Step 2 — TappRegistry registration (owner wallet, via tapp-cli)

```bash
# Registers the app + its FIRST node (signer auto-fetched from the server), pays per-node stake
tapp-cli -s http://<tapp-server>:50051 -k 0x<owner-key> register-onchain \
  --app-id <app-id> --rpc-url <rpc> --contract <tapp-registry> --stake-wei <wei>

# Authorize the settlement contract to bump ackVersion on price changes (once per contract)
cast send <tapp-registry> "authorizeInvalidator(string,address)" "<app-id>" <settlement-contract> \
  --rpc-url <rpc> --private-key 0x<owner-key> --legacy --gas-price 3000000000
```

### Step 3 — Get the node's TEE signer (= the provider address)

Either of:

```bash
tapp-cli -s http://<tapp-server>:50051 get-app-key --app-id <app-id>
# or, once the billing service is up:
curl -s http://<billing-proxy>:8082/api/info | grep provider_address
```

### Step 4 — Register the service on-chain (owner-signed)

Dashboard (recommended): open `/dashboard`, connect the **owner wallet**
(App Owner row shows which), Service Registration → "Register / Update" —
the signer field is pre-filled from `/api/info`.

CLI:

```bash
OWNER_KEY=0x<owner-key> $PROVIDER_CLI register \
  --rpc <rpc-url> --chain-id <chain-id> \
  --contract <settlement-contract> \
  --app-id <app-id> \
  --signer <tee-address-from-step3> \
  --url <billing-proxy-public-url> \
  --price-per-cpu <neuron> --price-per-mem <neuron> --fee <neuron>
```

Network presets: testnet `--rpc https://evmrpc-testnet.0g.ai --chain-id 16602`;
mainnet `--rpc https://evmrpc.0g.ai --chain-id 16661`.

No stake is collected here — stake lives in TappRegistry per node (Step 2).
The contract checks: caller == appId's TappRegistry owner, signer is an active
node, contract is an authorized invalidator.

### Step 5 — Verify + restart billing to pick up on-chain pricing

```bash
$PROVIDER_CLI status --rpc <rpc-url> --contract <settlement-contract> \
  --address <tee-signer> --tapp <tapp-registry>

tapp-cli -s http://<tapp-server>:50051 stop-service  --app-id <app-id> --service-name sandbox
tapp-cli -s http://<tapp-server>:50051 start-service --app-id <app-id> --service-name sandbox
```

`/api/info` should then show `signer.status: "aligned"` and the on-chain prices.

---

## B — Update Service Registration

Re-run `register` (owner-signed) with new values — same `--signer`, same `--app-id`.

> **Price/createFee changes bump `ackVersion`** — every existing user must
> re-run `acknowledge` before their vouchers settle again. URL-only changes do
> NOT invalidate acks.

### Unit conversion reference

| Human-readable | Neuron value |
|----------------|-------------|
| 0.001 0G/CPU/min | `1000000000000000` |
| 0.0005 0G/GB/min | `500000000000000` |
| 0.06 0G create fee | `60000000000000000` |

---

## C — Rotate After a Machine Rebuild

A rebuilt machine has a **new TEE signer** → new provider identity. The old
signer's key is gone forever. Run these in order:

```
1. Machine back up with the new key. The settler detects its signer is not a
   registered node and HOLDS the voucher queue (no gas burned, no lost revenue).
2. tapp-cli add-node-onchain — ADD the new signer (do NOT replace):
   old + new nodes coexist so both signers' pending vouchers can settle.
3. Wait for the OLD signer's queue to drain: GET /api/queue/summary → 0.
4. OWNER_KEY=0x<key> $PROVIDER_CLI rotate --contract <settlement> \
     --old 0x<dead-signer> --new 0x<new-signer>
   (copies appId/URL/prices to the new signer, removes the old service and
    sweeps its pending earnings to the owner; same prices → no ack bump)
5. tapp-cli remove-node-onchain for the old signer (stake unlocks ~1 day → withdraw).
6. Users with balance at the old signer: requestRefund → 2h lock →
   withdrawRefund → deposit to the new signer.
```

To just remove a node's service (no successor): `$PROVIDER_CLI remove-service --signer 0x<addr>`.

---

## Snapshot Management

Snapshots are provider-managed shared base images. Specs (CPU/mem/disk) are
fixed per snapshot — users cannot override them at create time.

**Easiest via dashboard:** `/dashboard` → Snapshots (owner wallet login).

### Option A: Build locally and push

```bash
docker build -t <image-name>:<version> -f <Dockerfile> .   # tag must NOT be :latest
```

Base image must be `daytonaio/sandbox:0.5.0-slim` (runs as `USER daytona`):

```dockerfile
FROM daytonaio/sandbox:0.5.0-slim
RUN curl https://sh.rustup.rs -sSf | sh -s -- -y --default-toolchain stable --profile minimal
ENV PATH="/home/daytona/.cargo/bin:${PATH}"   # NOT /root/.cargo/bin
```

Push into the internal registry via the runner:

```bash
$PROVIDER_CLI push-image --image <image-name>:<version> --runner <runner-container-name>
```

### Option B: Import from an external registry

**Via dashboard:** Snapshots → "↓ Import Image" (source image, optional
credentials, target name + tag). Synchronous — large images take minutes.
(API: `POST /api/registry/pull`, admin-signed.)

### Register / list / delete snapshots

```bash
OWNER_KEY=0x<key> $PROVIDER_CLI snapshot \
  --api http://<billing-proxy>:8082 \
  --image registry:6000/daytona/<image-name>:<version> \
  --name <snapshot-name> [--cpu N --memory N --disk N | --tiers]

OWNER_KEY=0x<key> $PROVIDER_CLI snapshots --api http://<billing-proxy>:8082
OWNER_KEY=0x<key> $PROVIDER_CLI delete-snapshot --api http://<billing-proxy>:8082 --id <name>
```

`--tiers` sizes: small (1C/1GB/10GB), medium (2C/4GB/30GB), large (4C/8GB/60GB).
State: `pending` → `active` in ~30s. Standard base snapshots to offer users:
**0g-ubuntu22** (1C/1G, general) and **0g-openclaw** (2C/4G, AI coding gateway).

---

## Resource Quotas (oversell control)

Two independent layers (both verified live):

1. **Per-sandbox cgroup limits** — `RESOURCE_LIMITS_DISABLED=false` on the
   runner service (compose already sets it; the runner image bakes `true`, so
   removing the line silently unlimits every sandbox).
2. **Machine-wide cap** — Daytona **org quota**, checked on every create:
   total CPU/mem/disk across all sandboxes + per-sandbox maxima. This is the
   ONLY total-capacity gate. (`DEFAULT_RUNNER_CPU/MEMORY/DISK` do NOT cap
   anything — they only rank runners in multi-runner setups; useless with one
   runner. The runner also auto-reports real host resources over them.)

Sizing rule of thumb: usable = host − stack overhead (~2C/6G); then CPU ×4
(compressible), memory ×1.5 (OOM risk — add swap), disk ×2. Per-sandbox max
should stay small relative to the total.

**Fresh install:** set in `.env` (compose passes them through):

```bash
DEFAULT_REGION_ENFORCE_QUOTAS=true
DEFAULT_ORG_QUOTA_TOTAL_CPU_QUOTA=<usable*4>
DEFAULT_ORG_QUOTA_TOTAL_MEMORY_QUOTA=<usable*1.5>
DEFAULT_ORG_QUOTA_TOTAL_DISK_QUOTA=<volume*2>
DEFAULT_ORG_QUOTA_MAX_CPU_PER_SANDBOX=4
DEFAULT_ORG_QUOTA_MAX_MEMORY_PER_SANDBOX=8
DEFAULT_ORG_QUOTA_MAX_DISK_PER_SANDBOX=50
```

**Existing install:** env seeds are ignored (rows already exist) — update the DB
directly, effective immediately, no restart:

```sql
UPDATE region SET "enforceQuotas"=true WHERE id='us';
-- INSERT the region_quota row first if the table is empty:
INSERT INTO region_quota ("organizationId","regionId",total_cpu_quota,total_memory_quota,
  total_disk_quota,max_cpu_per_sandbox,max_memory_per_sandbox,max_disk_per_sandbox,"sandboxClass")
VALUES ('<org-uuid>','us',<cpu>,<mem>,<disk>,4,8,50,'container');
```

Over-limit creates fail with `Total CPU limit exceeded` (nothing is billed).
Stopped-but-not-deleted sandboxes still occupy quota (they can be started back).

---

## Withdraw Earnings

Earnings accrue per node (TEE signer) and are withdrawn **by the owner, to the
owner wallet** — the signer key has no payout rights.

```bash
OWNER_KEY=0x<key> $PROVIDER_CLI withdraw \
  --rpc <rpc-url> --chain-id <chain-id> \
  --contract <settlement-contract> --signer <tee-signer>
```

Or via dashboard: Earnings card → "Withdraw Earnings" (connect owner wallet).

---

## Monitor — Billing Sessions & Sandboxes

**Via dashboard:** `/dashboard` → All Sandboxes (owner or ADMIN_ADDRESSES wallet).

- Force delete: Delete button / `DELETE /api/sandbox/<id>/force` (signed)
- Archive all before a redeploy: "Archive All" / `POST /api/archive-all` —
  user sandboxes are backed up and restorable afterwards

---

## Voucher Settlement History

**Via dashboard:** `/dashboard` → Voucher Settlement History → time range → Load.

**Via API:** `GET /api/events?since=<unix-ts>&page=<n>&page_size=50`
Each event: `timestamp`, `user`, `total_fee`, `nonce`, `status`, `tx_hash`.

---

## Status Check

```bash
# Read-only, no key needed. --address is the node's TEE signer.
$PROVIDER_CLI status --rpc <rpc-url> --contract <settlement-contract> \
  --address <tee-signer> [--tapp <tapp-registry>]
```

Shows: URL, appId, pricing, create fee, earnings; with `--tapp` also the app
owner and the node's TappRegistry state (active/stake).

---

## Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| `register` reverts: `not app owner` | Key isn't the appId's TappRegistry owner | Use the owner key (`OWNER_KEY`) |
| `register` reverts: `signer not an active node` | Signer not added in TappRegistry | `tapp-cli add-node-onchain` first |
| `register` reverts: invalidator error | Settlement contract not authorized for this appId | Run `authorizeInvalidator` (once per settlement contract — re-do after a contract migration) |
| Users suddenly get `NOT_ACKNOWLEDGED` / 402 | `ackVersion` bumped: price change, node change, or **any `.env` change + update-onchain** (volumesHash) | Users re-run `acknowledge`; batch config changes to avoid churn |
| Vouchers pile up, nothing settles | Settler holds the queue while its signer isn't a registered node (rotation window) | `add-node-onchain` the new signer — settler resumes automatically |
| Dashboard says "admin only" | Connected wallet is neither the app owner nor in `ADMIN_ADDRESSES` | Connect the owner wallet (App Owner row shows it) |
| `/api/info` signer.status `mismatch` | On-chain service appId ≠ `BACKEND_APP_NAME`, or node removed | Check registration; see startup log "appId drift" |
| On-chain `imageHashes` empty (`0x`) | Old tapp-server parses `docker compose images` wrong | Upgrade tapp-server, then `update-onchain` (bumps ackVersion) |
| Snapshot stays `pending` | Daytona can't pull the image | registry:6000 reachable? tag must not be `:latest` |
| Everyone can create unlimited sandboxes | Org quota not enforced | See Resource Quotas — `enforceQuotas` + region_quota row |
| SSH shows IP instead of domain | `SSH_GATEWAY_HOST` set to IP | Set it to the domain (confirm the LB forwards :2222 first) |
