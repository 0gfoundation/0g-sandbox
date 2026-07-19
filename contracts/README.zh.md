# 0G Sandbox — 合约注册表

网络：**0G Galileo 测试网**（chain ID 16602）
浏览器：https://chainscan-galileo.0g.ai
部署者/所有者：`0xB831371eb2703305f1d9F8542163633D0675CEd7`

> English version: [CONTRACTS.md](README.md)

---

## 开发合约

> 用于本地开发和集成测试。数据可随时重置。

| 组件 | 地址 |
|------|------|
| **Proxy**（稳定地址）| `0x2024eB0Cc14316fF8Cc425bFB7CC37FD8713E9b3` |
| Beacon | `0xaa77C82Dc6b4243Ff272d88619BD4f23455CCB6E` |

**升级历史：**

| 日期 | Impl | 变更说明 |
|------|------|---------|
| 初始 | — | 首次部署：per-provider 余额隔离，owner 模型 |
| 2026-03-10 | `0x9a3D6C66e3e6E020D8D40d851Db76D76EBfa93f2` | 移除 `settleFeesWithTEE` 中 `msg.sender == provider` 限制，TEE key 直接签结算 tx，无需 `PROVIDER_PRIVATE_KEY` |

```env
SETTLEMENT_CONTRACT=0x2024eB0Cc14316fF8Cc425bFB7CC37FD8713E9b3
```

---

## 测试网合约

> 正式测试网部署，用于 provider 注册和真实计费测试。

| 组件 | 地址 |
|------|------|
| **Proxy**（稳定地址）| `0xA07b0033cA65B06B090535944C121D8677FDC12c` |
| Beacon | `0xfdc08C0CdF629589D05E03849846006c37E800D5` |

**升级历史：**

| 日期 | Impl | 说明 |
|------|------|------|
| 2026-06-08 | `0xf870247949B35dC8174212F338DcdE9fCa95d5Bb` | 全新 proxy 重新部署（取代 `0xd7e0CD22…`);per-resource 定价 + TappRegistry trust root |
| 2026-06-08 | `0xe95DA05Bf17CAF09Cb129A706760bA52B55f14eE` | 新增 `deregisterService` —— 软清除 service,使（写一次的）`appId` 可更换 |

**Provider 质押：** 100 0G（`100000000000000000000` neuron），按节点存在 TappRegistry 里(不在 SandboxServing)。

```env
SETTLEMENT_CONTRACT=0xA07b0033cA65B06B090535944C121D8677FDC12c
```

---

## 合约架构

```
User/Billing ──► BeaconProxy  (稳定地址，所有 ETH/状态存于此)
                     │ 从 beacon 读取实现地址
                     ▼
               UpgradeableBeacon  (存储当前 impl，由部署者拥有)
                     │ delegatecall
                     ▼
               SandboxServing impl  (纯逻辑，无状态，可替换)
```

**代理地址永不改变**。升级只需替换实现合约。
给定代理地址，可在链上推导出 beacon 和 impl 地址：

```bash
# Beacon 地址 — ERC-1967 slot
cast storage <proxy> 0xa3f0ad74e5423aebfd80d3ef4346578335a9a72aeaee59ff6cb3582b35133d50

# 当前实现地址
cast call <beacon> "implementation()(address)"

# Beacon 所有者
cast call <beacon> "owner()(address)"
```

---

## 接口

`SandboxServing` 是结算合约:用户把 0G 充值并指定某个 provider;provider 把业务条款
(URL、价格、createFee)绑定到 TappRegistry 的 `appId`;之后用 TEE 签名的 voucher 在链上
结算计算费。信任身份(活跃 TEE 签名集合、用户 acknowledgement)存在 **TappRegistry**,
每次结算都会查询。

**用户(计费)**

| 函数 | 说明 |
|---|---|
| `deposit(recipient, provider)` payable | 给 `recipient` 充值,指定 `provider` |
| `requestRefund(provider, amount)` | 发起退款;经 `LOCK_TIME` 后可提取 |
| `withdrawRefund(provider)` | 提取已解锁的退款 |
| `getBalance(user, provider)` → (balance, pendingRefund, refundUnlockAt) | view |
| `balanceOfBatch(users[], provider)` → uint256[] | view |
| `getLastNonce(user, provider)` → uint256 | view — 最后结算的 nonce |
| `isTEEAcknowledged(user, provider)` → bool | view — 委托给 `tapp.isAcknowledged(user, appId)` |

**Provider**

| 函数 | 说明 |
|---|---|
| `addOrUpdateService(url, appId, pricePerCPUPerMin, createFee, pricePerMemGBPerMin)` | 注册/更新;`appId` 写一次;调用者须是该 appId 的 TappRegistry owner **或被授权的委托 provider** |
| `deregisterService()` | 软清除自己的 service,使 `appId` 可更换;余额/earnings/nonce 保留 |
| `withdrawEarnings()` | 提取累计结算收益 |
| `authorizeProvider(appId, provider)` | 仅 app owner — 把该 appId 的商业服务管理委托给另一个钱包;委托方注册自己完全独立的 service 条目(余额/nonce/earnings 互相隔离) |
| `revokeProvider(appId, provider)` | 仅 app owner — 软撤销:只挡住该委托方后续的 `addOrUpdateService`,已有 service 和结算照常;要切断签 voucher 的能力去 TappRegistry 摘节点(`remove-node-onchain`) |
| `authorizedProviders(appId, provider)` → bool | view — 委托状态 |
| `services(provider)` / `serviceExists(provider)` | view — 业务条款 |
| `getProviderEarnings(provider)` → uint256 | view |

**结算**

| 函数 | 说明 |
|---|---|
| `settleFeesWithTEE(vouchers[])` → statuses[] | 无需权限;provider 由 `v.provider` 标识;按 appId 的活跃 TEE 节点验 EIP-712 签名 |
| `previewSettlementResults(vouchers[])` → statuses[] | view — 试算结算状态 |

**管理 / 初始化**

| 函数 | 说明 |
|---|---|
| `initialize(tappRegistry_)` | 一次性,在 proxy 上 |
| `owner()` / `transferOwnership(newOwner)` | 合约管理员 |
| `setTappRegistry(newRegistry)` | TappRegistry 重新部署后用来切换指向;ack 状态从新 registry 读 |
| `tappRegistry()` / `domainSeparator()` / `LOCK_TIME()` | view |

**事件:** `Deposited`、`RefundRequested`、`RefundWithdrawn`、`VoucherSettled`、`EarningsWithdrawn`、`ServiceUpdated`、`ServiceDeregistered`、`ProviderAuthorized`、`ProviderRevoked`、`OwnershipTransferred`、`TappRegistryUpdated`。

---

## 部署（首次）

分三步部署完整的 beacon-proxy 合约栈：
1. SandboxServing 实现合约（无构造参数）
2. UpgradeableBeacon（impl, deployer）
3. BeaconProxy（beacon, initialize(tappRegistry)）

前置条件：目标链上已有 TappRegistry 部署（地址查阅 0g-tapp repo 的部署记录）。

```bash
go run ./cmd/deploy/ \
  --rpc      https://evmrpc-testnet.0g.ai \
  --key      0x<deployer-private-key> \
  --chain-id 16602 \
  --tapp     0x<tapp-registry-address>
```

输出：
```
Implementation : 0x...
Beacon         : 0x...
Proxy (stable) : 0x...   ← 将此地址设为 SETTLEMENT_CONTRACT
TappRegistry   : 0x...   ← 将此地址设为 TAPP_REGISTRY
```

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `--rpc` | `https://evmrpc-testnet.0g.ai` | EVM RPC 地址 |
| `--key` | （必填）| 部署者私钥（十六进制，0x 可选）|
| `--chain-id` | `16602` | 链 ID |
| `--tapp` | （必填）| TappRegistry 合约地址 — 传入 `initialize()` |

---

## 升级

部署新实现合约并将 beacon 指向它。
**代理地址不变** — 无需更新 `.env`，无需用户重新确认。

```bash
go run ./cmd/upgrade/ \
  --rpc      https://evmrpc-testnet.0g.ai \
  --key      0x<deployer-private-key> \
  --chain-id 16602 \
  --proxy    0x<proxy-address>
```

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `--rpc` | `https://evmrpc-testnet.0g.ai` | EVM RPC 地址 |
| `--key` | （必填）| 部署者/所有者私钥 |
| `--chain-id` | `16602` | 链 ID |
| `--proxy` | （二选一*）| BeaconProxy 地址 — 自动解析 beacon |
| `--beacon` | （二选一*）| UpgradeableBeacon 地址（与 `--proxy` 二选一）|

\* 提供 `--proxy` 或 `--beacon` 其中之一。

---

## 验证

在区块浏览器上验证全部三个合约。
**只需提供代理地址** — beacon 和 impl 从链上自动解析。

```bash
./scripts/verify-contracts.sh --proxy 0x<proxy-address>
```

---

## Provider 注册

Provider 的 trust root 存放在 **TappRegistry**：`appId` → composeHash、镜像 hash 列表、
当前活跃 TEE 节点集合。Provider 先通过 `tapp-cli` 在 TappRegistry 注册 app，再在
**SandboxServing** 中把 URL / 价格 / createFee 绑定到该 `appId`。

完整 flag 说明见 [`CLI.md`](../docs/CLI.md)。

```bash
# 1. 在 TappRegistry 中启动并注册 app（用 provider 钱包执行,使其成为 app owner）
tapp-cli -s http://<tapp-server>:50051 start-app \
  --app-id 0g-sandbox-provider \
  -f docker-compose.yml

# 2. 把 SandboxServing 加为该 app 的 invalidator
#    （后续在 SandboxServing 改价格会自动 bump TappRegistry 的 ackVersion）
tapp-cli authorize-invalidator-onchain \
  --app-id    0g-sandbox-provider \
  --contract  0x<sandbox-serving-proxy>

# 3. 在 SandboxServing 中把业务信息绑定到 appId
PROVIDER_KEY=0x<provider-key> go run ./cmd/provider/ register \
  --api            http://<billing-proxy>:8080 \
  --app-id         0g-sandbox-provider \
  --price-per-cpu  <neuron/cpu/min> \
  --price-per-mem  <neuron/memGB/min> \
  --create-fee     <neuron>
```

完成后在 `.env` 中设置 `PROVIDER_ADDRESS` 与 `TAPP_REGISTRY`，并确保 provider 钱包
持有足够 0G 用于结算 gas。

---

## 设计说明

- **Proxy 地址永不变** — 升级只替换 implementation，proxy 地址是对外稳定地址
- **结算开放** — `settleFeesWithTEE` 任何人可调用，provider 由 voucher 内的 `v.provider` 字段标识，与 `msg.sender` 无关
- **Trust root 委托** — SandboxServing 只持有商业条款；TEE 签名身份与用户 acknowledgement 都在 TappRegistry 中，每次 voucher 验签都会查询
- **`appId` 写一次** — 一旦 `addOrUpdateService` 绑定了非空 `appId`，之后只能就地修改 URL / 价格 / createFee，无法替换 trust root。要绑定**不同的** `appId`,需先调 **`deregisterService`**:软清除调用者自己的 service 条目(url/appId/价格/createFee),但保留用户余额、待退款、已结算 nonce 和累计 `providerEarnings`(仍可提取),然后重新 register。触发 `ServiceDeregistered(provider)` 事件。
- **Provider 委托** — 一个 appId 可以由多个钱包分别运营:app owner 调 `authorizeProvider(appId, wallet)` 授权,每个委托方注册自己的 service 条目,余额、nonce、earnings 完全隔离(与自注册 provider 同构)。余额**刻意不共享**:各自独立部署的 billing proxy 用各自的 Redis 做预留准入,互相看不见对方的在途预留,共享链上余额会导致超卖。注意结算边界是 appId 级而非委托方级:该 appId 的任何在册 TappRegistry 节点都能给任何委托方的 voucher 签名,实践中靠代码指纹(attested compose/镜像哈希)保证节点只给自己的 `PROVIDER_ADDRESS` 出账。
