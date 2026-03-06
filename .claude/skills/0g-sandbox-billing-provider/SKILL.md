# 0G Sandbox Billing — Provider Skill

---

## Quick Reference

| | Value |
|---|---|
| **RPC** | `https://evmrpc-testnet.0g.ai` |
| **Chain ID** | `16602` |
| **Current contract** | `0x24cD979DBd0Ae924a3f0c832a724CF4C58E5C210` |
| **Go binary** | `/usr/local/go/bin/go` |

```bash
export PATH=$PATH:/usr/local/go/bin
export RPC=https://evmrpc-testnet.0g.ai
export CHAIN_ID=16602
export CONTRACT=0x24cD979DBd0Ae924a3f0c832a724CF4C58E5C210
export PROVIDER_KEY=0x<provider-private-key>
```

---

## Provider Concepts

### Roles
- **Provider key** (`PROVIDER_KEY` / `PROVIDER_PRIVATE_KEY`) — signs settlement txs (`msg.sender == provider`). The provider address shown in the UI is derived from this key.
- **TEE key** (`MOCK_APP_PRIVATE_KEY`) — signs vouchers (EIP-712, off-chain). In dev, same as provider key. In production, fetched from TDX enclave via tapp-daemon.

### Stake
- `providerStake` is set by the contract owner during `initialize()` (deploy time) or via `setProviderStake()`.
- **First registration** (`addOrUpdateService`) requires attaching `msg.value >= providerStake`.
- **Updates** (changing URL, price, signer) require **no additional stake**.
- `cmd/setup` auto-reads `providerStake` from the contract and attaches it automatically.

### Pricing
- `computePricePerMin` — compute cost per minute in neuron (1 0G = 1e18 neuron)
- `createFee` — one-time fee charged when a user creates a sandbox
- Both are set in `addOrUpdateService` and can be updated at any time.
- Updating price/signer increments `signerVersion`, requiring all users to re-acknowledge before vouchers can settle.

---

## Workflow

### 1. Deploy a new contract

```bash
go run ./cmd/deploy/ \
  --rpc $RPC \
  --key $PROVIDER_KEY \
  --chain-id $CHAIN_ID \
  --stake <neuron>        # e.g. 100000000000000000 = 0.1 0G; use 0 for no stake requirement
```

Output:
```
Implementation : 0x...
Beacon         : 0x...
Proxy (stable) : 0x...   ← this is your CONTRACT address
```

Set `SETTLEMENT_CONTRACT=<proxy>` in your `.env`.

### 2. Register as provider (one-time setup)

```bash
MOCK_TEE=true \
MOCK_APP_PRIVATE_KEY=$PROVIDER_KEY \
go run ./cmd/setup/ \
  --rpc $RPC \
  --chain-id $CHAIN_ID \
  --contract $CONTRACT \
  --url https://your-provider-url.example.com \
  --price-per-min 1000000000000000 \   # neuron/min
  --create-fee 60000000000000000 \     # neuron per sandbox creation
  --deposit 1                          # 0G to deposit for self-testing
```

`cmd/setup` automatically:
1. Reads `providerStake` from the contract
2. Attaches it as `msg.value` on first registration (skips if already registered)
3. Deposits 0G for your own address (for testing)
4. Acknowledges the TEE signer for your own address

### 3. Check provider status

```bash
# Balance, earnings, registration status
MOCK_TEE=true \
MOCK_APP_PRIVATE_KEY=$PROVIDER_KEY \
go run ./cmd/checkbal/ \
  --rpc $RPC \
  --contract $CONTRACT
```

Or via the user CLI:
```bash
go run ./cmd/user/ providers --api http://localhost:8080
```

### 4. Update service (price / URL / signer)

```bash
MOCK_TEE=true \
MOCK_APP_PRIVATE_KEY=$PROVIDER_KEY \
go run ./cmd/setup/ \
  --rpc $RPC \
  --contract $CONTRACT \
  --url https://new-url.example.com \
  --price-per-min 2000000000000000 \
  --create-fee 60000000000000000 \
  --deposit 0     # skip deposit on update
```

**Note**: Changing price or signer increments `signerVersion`. All users must re-acknowledge before vouchers settle. The billing server detects this via `ServiceUpdated` events.

### 5. Admin: update required stake

```bash
# Only the contract owner can call this
cast send $CONTRACT "setProviderStake(uint256)" <neuron> \
  --rpc-url $RPC \
  --private-key $OWNER_KEY
```

Or write a Go script using `contract.SetProviderStake(auth, newStake)`.

### 6. Admin: transfer ownership

```bash
cast send $CONTRACT "transferOwnership(address)" <newOwner> \
  --rpc-url $RPC \
  --private-key $OWNER_KEY
```

### 7. Upgrade contract

```bash
# Deploy new impl + upgrade beacon to point to it
go run ./cmd/upgrade/ \
  --rpc $RPC \
  --key $PROVIDER_KEY \
  --chain-id $CHAIN_ID \
  --proxy $CONTRACT
```

State (balances, nonces, service registrations) is preserved across upgrades.

### 8. Withdraw earnings

```bash
cast send $CONTRACT "withdrawEarnings()" \
  --rpc-url $RPC \
  --private-key $PROVIDER_KEY
```

Or earnings accumulate and can be withdrawn at any time.

---

## Running the Billing Server

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

## Tapp Deployment (Production)

```bash
# Build image
docker build -t billing:latest .

# Deploy / redeploy
tapp-cli stop-app  -s http://47.236.111.154:50051 --app-id billing
tapp-cli start-app -s http://47.236.111.154:50051 --app-id billing -f docker-compose.yml

# Logs
tapp-cli get-app-logs -s http://47.236.111.154:50051 --app-id billing -n 100
```

---

## Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| `insufficient stake` on addOrUpdateService | First registration without stake value | `cmd/setup` auto-handles this; or attach `msg.value >= providerStake` |
| `PROVIDER_MISMATCH` on settlement | `msg.sender` ≠ `v.provider` | Check `PROVIDER_PRIVATE_KEY` matches provider address in contract |
| `INVALID_NONCE` on settlement | Redis nonce stale / out of sync | Restart billing service (seeds nonce from chain on startup) |
| `not owner` on setProviderStake | Wrong key | Use the deployer key that initialized the proxy |
| Users get `NOT_ACKNOWLEDGED` after price change | `signerVersion` incremented | Users must re-call `acknowledgeTEESigner` |
| Settlement tx reverts | Various | Check `PROVIDER_PRIVATE_KEY` is funded for gas |
