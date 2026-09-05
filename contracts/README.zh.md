# 0G Sandbox — 合约注册表

网络：**0G Galileo 测试网**（chain ID 16602）
浏览器：https://chainscan-galileo.0g.ai
部署者：`0xB831371eb2703305f1d9F8542163633D0675CEd7`
所有者（合约 + beacon,dev 与 testnet）：`0x3f1a683418dba4c38dd853a7b896f7327a9fef9f`


> English version: [CONTRACTS.md](README.md)

---

## 开发合约

> 用于本地开发和集成测试。数据可随时重置。

| 组件 | 地址 |
|------|------|
| **Proxy**（稳定地址）| `0x3D0F2D62A60c8e62095671FfB23D15Cc4C98ca7c` |
| Beacon | `0xBF04734BC87E12aB81E21bb4018b9bFa4c118721` |
| TappRegistry | `0x2Ce80374318B1d7Fb3345724457a182E0ad165c9` |

**升级历史：**

| 日期 | Impl | 变更说明 |
|------|------|---------|
| 初始 | — | 首次部署：per-provider 余额隔离，owner 模型 |
| 2026-03-10 | `0x9a3D6C66e3e6E020D8D40d851Db76D76EBfa93f2` | 移除 `settleFeesWithTEE` 中 `msg.sender == provider` 限制，TEE key 直接签结算 tx，无需 `PROVIDER_PRIVATE_KEY` |
| 2026-07-19 | `0x47a8E809Cd81b94eD19874da73C0E3F82DD90E5C` | **v2 重新部署(新 proxy/beacon)**:provider 即 TEE signer;注册/注销/提现归 owner;结算要求收款人本人签名。绑定 TappRegistry `0x2Ce80374318B1d7Fb3345724457a182E0ad165c9`。旧 dev proxy `0x2024eB0C…E9b3` 退役(仅退款) |
| 2026-09-05 | (实现未变) | **所有权转移** —— 合约 `owner` 与 beacon owner 设为 `0x3f1a683418dba4c38dd853a7b896f7327a9fef9f`(原 `0xB831…CEd7`)。实现未变。 |

```env
SETTLEMENT_CONTRACT=0x3D0F2D62A60c8e62095671FfB23D15Cc4C98ca7c
TAPP_REGISTRY=0x2Ce80374318B1d7Fb3345724457a182E0ad165c9
```

---

## 测试网合约

> 生产测试网部署,用于 provider 注册与真实计费测试。

| 组件 | 地址 |
|------|------|
| **Proxy**(稳定地址) | `0x3490B9053AC46F7Bf71A1ceBffcB2be2C1405b41` |
| Beacon | `0x79D6D7B5468AA134360bf73cc667FC63f704B62d` |
| TappRegistry | `0x2Ce80374318B1d7Fb3345724457a182E0ad165c9` |

**升级历史:**

| 日期 | Impl | 说明 |
|------|------|------|
| 2026-07-20 | `0x7a1A5FC5B1A6AC1127e2D8b63400615B2ea49C47` | **v2 重新部署(新 proxy/beacon)**:provider 即 TEE signer;注册/注销/提现归 owner;结算要求收款人本人签名。已在 chainscan verify。取代 v1 测试网 proxy `0xA07b0033…FC12c`(绑 TappRegistry `0x2Ce8…`)与 `0x3d4d8a05…cf6f`——均退役(仅退款) |
| 2026-09-05 | (实现未变) | **所有权转移** —— 合约 `owner` 与 beacon owner 设为 `0x3f1a683418dba4c38dd853a7b896f7327a9fef9f`(原 `0xB831…CEd7`)。实现未变。 |

**Provider 质押:** 按节点存于 TappRegistry(不在 SandboxServing);见 registry 的 `minStakeAmount()`(当前 1 0G)。

```env
SETTLEMENT_CONTRACT=0x3490B9053AC46F7Bf71A1ceBffcB2be2C1405b41
TAPP_REGISTRY=0x2Ce80374318B1d7Fb3345724457a182E0ad165c9
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
| `addOrUpdateService(signer, url, appId, pricePerCPUPerMin, createFee, pricePerMemGBPerMin)` | app owner 为节点注册/更新服务;`signer`(= provider 地址)必须是该 appId 的在册 TappRegistry 节点;`appId` 每个 signer 写一次 |
| `removeService(signer)` | app owner 注销节点服务(如机器重建后);同一笔 tx 把未提 earnings 清给 owner;用户余额仍可退款,nonce 水位保留 |
| `withdrawEarnings(signer)` | app owner 把节点累计收益提到 owner 钱包 |
| `services(provider)` / `serviceExists(provider)` | view — 业务条款(provider = 节点的 TEE signer 地址) |
| `getProviderEarnings(provider)` → uint256 | view |

**结算**

| 函数 | 说明 |
|---|---|
| `settleFeesWithTEE(vouchers[])` → statuses[] | 无需权限;voucher 必须由收款人本人签名(`recovered == v.provider`)且该地址是 appId 在册节点——节点无法替别的节点结算 |
| `previewSettlementResults(vouchers[])` → statuses[] | view — 试算结算状态 |

**管理 / 初始化**

| 函数 | 说明 |
|---|---|
| `initialize(tappRegistry_)` | 一次性,在 proxy 上 |
| `owner()` / `transferOwnership(newOwner)` | 合约管理员 |
| `setTappRegistry(newRegistry)` | TappRegistry 重新部署后用来切换指向;ack 状态从新 registry 读 |
| `tappRegistry()` / `domainSeparator()` / `LOCK_TIME()` | view |

**事件:** `Deposited`、`RefundRequested`、`RefundWithdrawn`、`VoucherSettled`、`EarningsWithdrawn(provider, to, amount)`、`ServiceUpdated`、`ServiceRemoved(provider, appOwner)`、`OwnershipTransferred`、`TappRegistryUpdated`。

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
- **provider 就是 TEE signer(v2)** — 合约里所有 provider 地址(services 键、voucher 收款人、余额桶、earnings 账)都是 TappRegistry 某个节点的 TEE signer 地址。signer 私钥不出 enclave、随机器消亡,所以注册/注销/提现全部归 appId 的 TappRegistry owner。一个 appId 多个节点:每个 signer 账本完全隔离——余额**刻意不共享**(各自独立部署的 billing proxy 用各自 Redis 做预留准入,互相看不见在途预留,共享余额会超卖)。
- **轮换** — 机器重建产生新 signer。流程:TappRegistry 先 add 新节点(新旧并存)→ 排空旧 signer 的 voucher 队列 → `cmd/provider rotate` 迁移服务条目 → 摘旧节点。用户余额走退款通道离开死 signer;`removeService` 把它的 earnings 清给 owner。
- **`appId` 每个 signer 写一次** — 绑定后只能就地改 URL/价格。要换 `appId` 先 `removeService`(用户余额、待退款、已结算 nonce 保留——nonce 不动,重注册后旧 voucher 无法重放)。
