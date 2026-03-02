# CLI Reference

Two command-line tools are provided for operators and users:

| Tool | Role |
|------|------|
| `cmd/provider` | Provider operator: register/update service on-chain |
| `cmd/user` | End user: manage balance and sandboxes |

Private keys can be passed via `--key` flag or environment variable (`PROVIDER_KEY` / `USER_KEY`). The `0x` prefix is optional.

---

## `cmd/provider` — Provider Operations

### `init-service`

Register a new service, or update an existing one, on the settlement contract.
Must be called by the provider address (the key that will sign settlement transactions).

```bash
PROVIDER_KEY=0x<hex> go run ./cmd/provider/ init-service \
  --tee-signer <TEE-signer-address> \
  --url        <billing-proxy-url> \
  [--price     <neuron-per-minute>] \
  [--fee       <create-fee-neuron>] \
  [--rpc       <rpc-url>] \
  [--chain-id  <chain-id>] \
  [--contract  <proxy-address>]
```

**Flags**

| Flag | Default | Description |
|------|---------|-------------|
| `--key` | `PROVIDER_KEY` env | Provider private key (hex) |
| `--tee-signer` | (required) | Address derived from the TEE key (`tapp-cli get-app-key --app-id billing`) |
| `--url` | (required) | Public URL of the billing proxy (e.g. `http://1.2.3.4:8080`) |
| `--price` | `1000020` | Compute price in neuron/minute |
| `--fee` | `5000000` | Flat fee in neuron per sandbox creation |
| `--rpc` | `https://evmrpc-testnet.0g.ai` | EVM RPC endpoint |
| `--chain-id` | `16602` | Chain ID |
| `--contract` | `0x24cD979...` | Settlement contract (BeaconProxy) address |

**Example — testnet**

```bash
# Get TEE signer address first:
tapp-cli -s http://<server>:50051 get-app-key --app-id billing
# → Ethereum Address: 0x61beb835...

PROVIDER_KEY=0x859c3bd1... go run ./cmd/provider/ init-service \
  --tee-signer 0x61BEb835D1935Eec8cC04efa2f4e2B3cC8B8B6E3 \
  --url        http://47.236.111.154:8080 \
  --price      1000020 \
  --fee        5000000
```

**Output**

```
Provider:    0xB831371eb2703305f1d9F8542163633D0675CEd7
TEE signer:  0x61BEb835D1935Eec8cC04efa2f4e2B3cC8B8B6E3
Contract:    0x24cD979DBd0Ae924a3f0c832a724CF4C58E5C210
Service URL: http://47.236.111.154:8080
Price/min:   1000020 neuron
Create fee:  5000000 neuron

[1/1] AddOrUpdateService...
      tx: 0x...
      confirmed ✓

Done. Set PROVIDER_ADDRESS=0xB831371eb2703305f1d9F8542163633D0675CEd7 in your .env.
```

> **After calling `init-service`**: set `PROVIDER_ADDRESS` and `PROVIDER_PRIVATE_KEY` in your `.env`, then redeploy the billing service.

---

## `cmd/user` — User Operations

### Chain subcommands

These interact directly with the settlement contract on-chain.

---

#### `balance`

Show a user's on-chain balance and optionally their nonce and provider earnings.

```bash
go run ./cmd/user/ balance \
  [--key      <hex>] \
  [--address  <wallet-address>] \
  [--provider <provider-address>] \
  [--rpc      <rpc-url>] \
  [--contract <proxy-address>]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--key` | `USER_KEY` env | User private key; address is derived from it |
| `--address` | (derived from `--key`) | Wallet address to check (use without `--key`) |
| `--provider` | — | If set, also shows last nonce and provider earnings |

**Example**

```bash
USER_KEY=0x<hex> go run ./cmd/user/ balance \
  --provider 0xB831371eb2703305f1d9F8542163633D0675CEd7
```

```
Address: 0xdAc113A24f4c7c57792B67127D99Fdda258e1023
Balance: 10000000000000000 neuron  (0.010000 0G)
Nonce (vs provider 0xB831...):  3
Provider earnings: 9000000 neuron  (0.000009 0G)
```

---

#### `deposit`

Deposit 0G tokens into the settlement contract to fund sandbox usage.

```bash
go run ./cmd/user/ deposit \
  [--key     <hex>] \
  [--amount  <float-0g>] \
  [--rpc     <rpc-url>] \
  [--contract <proxy-address>] \
  [--chain-id <chain-id>]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--key` | `USER_KEY` env | User private key |
| `--amount` | `0.01` | Amount to deposit **in 0G** (e.g. `0.01` = 10¹⁶ neuron) |

**Example**

```bash
USER_KEY=0x<hex> go run ./cmd/user/ deposit --amount 0.01
```

```
User:     0xdAc113A24f4c7c57792B67127D99Fdda258e1023
Amount:   0.010000 0G (10000000000000000 neuron)
Contract: 0x24cD979DBd0Ae924a3f0c832a724CF4C58E5C210

[1/1] Deposit...
      tx: 0x...
      confirmed ✓

New balance: 10000000000000000 neuron  (0.010000 0G)
```

---

#### `acknowledge`

Acknowledge (or revoke) the provider's TEE signer. Must be done once before creating sandboxes.

```bash
go run ./cmd/user/ acknowledge \
  --provider <provider-address> \
  [--key     <hex>] \
  [--revoke] \
  [--rpc     <rpc-url>] \
  [--contract <proxy-address>] \
  [--chain-id <chain-id>]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--provider` | (required) | Provider address to acknowledge |
| `--key` | `USER_KEY` env | User private key |
| `--revoke` | false | Revoke instead of acknowledge |

**Example**

```bash
USER_KEY=0x<hex> go run ./cmd/user/ acknowledge \
  --provider 0xB831371eb2703305f1d9F8542163633D0675CEd7
```

```
User:     0xdAc113A24f4c7c57792B67127D99Fdda258e1023
Provider: 0xB831371eb2703305f1d9F8542163633D0675CEd7

[1/1] AcknowledgeTEESigner (accept=true)...
      tx: 0x...
      confirmed ✓
```

> If the provider updates their TEE signer (`init-service` called again), users must re-acknowledge.

---

### API subcommands

These call the billing proxy over HTTP using EIP-191 signed requests.
All require `--api` (the billing proxy URL) and the user's private key.

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
  --api   <billing-proxy-url> \
  [--key  <hex>] \
  [--image <snapshot>]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--api` | `http://localhost:8080` | Billing proxy URL |
| `--key` | `USER_KEY` env | User private key |
| `--image` | — | Custom sandbox snapshot (optional) |

**Example**

```bash
USER_KEY=0x<hex> go run ./cmd/user/ create --api http://47.236.111.154:8080
```

---

#### `list`

List your sandboxes (filtered by owner — you only see your own).

```bash
go run ./cmd/user/ list \
  --api  <billing-proxy-url> \
  [--key <hex>]
```

**Example**

```bash
USER_KEY=0x<hex> go run ./cmd/user/ list --api http://47.236.111.154:8080
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
  --api  <billing-proxy-url> \
  --id   <sandbox-id> \
  [--key <hex>]
```

**Example**

```bash
USER_KEY=0x<hex> go run ./cmd/user/ stop \
  --api http://47.236.111.154:8080 \
  --id  9c1d0f45-d7da-485d-8c70-e7f928491c00
```

---

#### `delete`

Delete a sandbox. Only the sandbox owner can delete it.

```bash
go run ./cmd/user/ delete \
  --api  <billing-proxy-url> \
  --id   <sandbox-id> \
  [--key <hex>]
```

---

## Onboarding Flow

Complete flow for a new user to start using sandboxes:

```bash
# 1. Fund your wallet with 0G on testnet (faucet or transfer)

# 2. Deposit into the settlement contract
USER_KEY=0x<hex> go run ./cmd/user/ deposit --amount 0.01

# 3. Acknowledge the TEE signer for the provider
USER_KEY=0x<hex> go run ./cmd/user/ acknowledge \
  --provider 0xB831371eb2703305f1d9F8542163633D0675CEd7

# 4. Create a sandbox
USER_KEY=0x<hex> go run ./cmd/user/ create --api http://<billing-proxy>:8080

# 5. List your sandboxes
USER_KEY=0x<hex> go run ./cmd/user/ list --api http://<billing-proxy>:8080

# 6. Stop when done
USER_KEY=0x<hex> go run ./cmd/user/ stop \
  --api http://<billing-proxy>:8080 \
  --id  <sandbox-id>
```
