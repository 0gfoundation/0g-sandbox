# 0G Sandbox — 合约注册表

网络:**0G 主网**(chain ID 16661)· **0G Galileo 测试网**(chain ID 16602,dev + testnet 用)。各段自述网络/浏览器/RPC。
所有者(合约 + beacon,2026-10-28 链上读取):主网 = `0xc0603224dE8c3B910a4487368E476e19dC6127cF`(Fordefi MPC vault,2026-10-16 起;部署者为 `0x73443d…`);testnet = `0x9C58F839451EBf3e7f82442bC48E4088746380Bb`(2026-10-14 起);dev = `0x2AB7F05bA6560ef2B21Aa1b52FCc96DB51Cfa0B3`(2026-10-19 起)。


> English version: [CONTRACTS.md](README.md)

---

## 主网合约

> 0G 主网(chain ID 16661)· 浏览器:https://chainscan.0g.ai · RPC:https://evmrpc.0g.ai
> 生产部署。部署于 2026-09-09。

| 组件 | 地址 |
|------|------|
| **Proxy**(稳定) | `0x650C16a491ad52dB6b7f3053bCaD650cb43d2206` |
| Beacon | `0x3916925C9F34016d0182c731Cd8D1021fDEAe207` |
| TappRegistry | `0x54874F536301c993922Dd95097e3902e7FBfe612` |

**升级历史:**

| 日期 | Impl | 说明 |
|------|------|------|
| 2026-09-09 | `0x90a0f1BcaCE1A516CF4D1918178EB99E08c16f24` | 主网首次部署(v2):provider 即 TEE signer;注册/注销/提现归 owner;结算要求收款人本人签名。绑定 TappRegistry `0x5487…e612`。Owner = 部署者 `0x7344…431b`。 |
| 2026-10-04 | (实现未变) | **所有权转移** —— 合约 `owner` 与 beacon owner 设为 `0x87605Ec8E10Eb373C1d070e15E5d78FAC4D7621d`(原 `0x7344…431b`)。实现未变。 |
| 2026-10-16 | (实现未变) | **所有权转移** —— 合约 `owner` 与 beacon owner 设为 Fordefi MPC vault `0xc0603224dE8c3B910a4487368E476e19dC6127cF`(原 `0x8760…621d`):合约 tx `0xcf7fce1484530133dacc873d6e75b728de9c049b8005d737cb6c06ca333e243c`,beacon tx `0xa903456f00729094ba82f11823a1c1c741d50c3828b83f2e71d00d5538dca2da`。实现未变。 |

KMS provider 实现(dev 2026-10-26、testnet 2026-10-27)尚未上主网;主网的 beacon 升级由 vault 发起。

```env
SETTLEMENT_CONTRACT=0x650C16a491ad52dB6b7f3053bCaD650cb43d2206
TAPP_REGISTRY=0x54874F536301c993922Dd95097e3902e7FBfe612
RPC_URL=https://evmrpc.0g.ai
CHAIN_ID=16661
```

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
| 2026-09-04 | (实现未变) | **所有权转移** —— 合约 `owner` 与 beacon owner 设为 `0x3f1a683418dba4c38dd853a7b896f7327a9fef9f`(原 `0xB831…CEd7`,当天经 `0xea69…1837` 中转)。实现未变。 |
| 2026-10-19 | (实现未变) | **所有权转移** —— 合约 `owner` 与 beacon owner 设为 `0x2AB7F05bA6560ef2B21Aa1b52FCc96DB51Cfa0B3`(原 `0x3f1a…9fef9f`):合约 tx `0x23c2987fbec6627f11cec151145c1ff8676efb072038e2aa241cb29261e2e0e4`,beacon tx `0x6cc2047fb81fb9699f4481b813b02a6958495b56dc40f26a4ef6a7fb7ce11890`。实现未变。 |
| 2026-10-26 | `0xD3f1B3633F015D126e8374b5EDBE699a680DC3D3` | **KMS provider 密钥**(#144):`addOrUpdateKmsService` 注册以 app 的 KMS key 为地址的 provider,须由该 app 的一个活跃节点背书(对 `KmsProvider(provider, appId)` 的 EIP-712 签名),并绑定到 app 当前的 TappRegistry 实例(`registeredAt`),结算与 owner 操作都会校验;`repinKmsProvider` 用于 TappRegistry 重新部署后重新绑定;新增 view `kmsBindings`、`kmsProviderDigest`、`isActiveProvider`。节点 signer 型 provider 不变;新状态占用一个 `__gap` 槽。部署 tx `0x4d6a26378fffd057fab27fe045f4febd06050b52bc1121062aa466f482e01143`,beacon `upgradeTo` tx `0xb07d9262ee6aaf700bef687a93cf19b50baa5c079151b1f8f2f65b90a9e70b0a`。尚未在 chainscan verify。2026-10-27 dev 沙盒注册了 KMS provider `0x6051…c13a`(tx `0x9fd5fc1dd675978669d69a03bd0b019e86c980f590b9b206dc9196c6f65ca6ea`),并移除了节点 signer 型服务 `0x5f04…9965`(tx `0x73051b4d77c94d05bba27d2ee156d53b562931da7f70c6f4de7ca3fa29a90c6b`)。 |

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
| 2026-09-04 | (实现未变) | **所有权转移** —— 合约 `owner` 与 beacon owner 设为 `0x3f1a683418dba4c38dd853a7b896f7327a9fef9f`(原 `0xB831…CEd7`,当天经 `0xea69…1837` 中转)。实现未变。 |
| 2026-10-14 | (实现未变) | **所有权转移** —— 合约 `owner` 与 beacon owner 设为 `0x9C58F839451EBf3e7f82442bC48E4088746380Bb`(原 `0x3f1a…9fef9f`):合约 tx `0x0ddd81829ab2a845ef16268bc993f64e235b6002af50e1342d3938366c0e9547`,beacon tx `0x555654195574a78476dd503d56f76db4e91f4bbf697d663a1c8cd34902faf3db`。实现未变。 |
| 2026-10-27 | `0x950Abcec39982F3e3eD6dF8F3D4e567AC69a180c` | KMS provider 实现(与 dev 2026-10-26 那行同一份源码,单独部署):**KMS provider 密钥**(#144):`addOrUpdateKmsService` 注册以 app 的 KMS key 为地址的 provider,须由该 app 的一个活跃节点背书(对 `KmsProvider(provider, appId)` 的 EIP-712 签名),并绑定到 app 当前的 TappRegistry 实例(`registeredAt`),结算与 owner 操作都会校验;`repinKmsProvider` 用于 TappRegistry 重新部署后重新绑定;新增 view `kmsBindings`、`kmsProviderDigest`、`isActiveProvider`。节点 signer 型 provider 不变;新状态占用一个 `__gap` 槽。部署 tx `0x242710d3277a0d813c3d38ee3bec3e32c18694267cdeb84ad9e9e89f1e720a83`,beacon `upgradeTo` tx `0x1931d7f2774576124a932d698f5dba4eaa96ca8f7d7ca7707b2b4cea3d3b5274`。尚未在 chainscan verify。同日 testnet 沙盒注册了 KMS provider `0xb769…9A5a`(tx `0xa6909e4d396c86554e81b80ffc30ffa4f9f024cb8a6b3c4dc7e1847a6378c2e2`)。旧的节点 signer 型服务 `0x3F7E…5467` 已不是活跃 provider,但仍在册(待 `removeService`)。 |

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
| `addOrUpdateKmsService(provider, url, appId, pricePerCPUPerMin, createFee, pricePerMemGBPerMin, endorsement)` | app owner 注册/更新 **KMS provider**:`provider` 是 app 的 KMS key(节点设 `PROVIDER_KEY_SOURCE=kms`),`endorsement` 是在册节点对 `KmsProvider(provider, appId)` 的 EIP-712 签名。首次调用把 provider 绑定到 app 当前的 TappRegistry 实例(`registeredAt`),之后每次调用都必须仍是同一个实例 |
| `kmsProviderDigest(provider, appId)` → bytes32 | view — 节点为 KMS provider 背书时签的 digest |
| `kmsBindings(provider)` → (epoch, appIdHash) | view — KMS provider 绑定的 app 实例;node-signer provider 的 `epoch` 为 0 |
| `isActiveProvider(provider)` → bool | view — provider 自己的 key 现在能否签 voucher(即结算规则) |
| `removeService(signer)` | app owner 注销节点服务(如机器重建后);同一笔 tx 把未提 earnings 清给 owner;用户余额仍可退款,nonce 水位保留 |
| `withdrawEarnings(signer)` | app owner 把节点累计收益提到 owner 钱包 |
| `services(provider)` / `serviceExists(provider)` | view — 业务条款(provider = 节点的 TEE signer 地址) |
| `getProviderEarnings(provider)` → uint256 | view |

**结算**

| 函数 | 说明 |
|---|---|
| `settleFeesWithTEE(vouchers[])` → statuses[] | 无需权限;voucher 必须由收款人本人签名(`recovered == v.provider`),且收款人的 key 对该 appId 有效:node-signer provider 要是在册节点,KMS provider 要 app 仍是它绑定的那个实例。一个 provider 无法替别的 provider 结算 |
| `previewSettlementResults(vouchers[])` → statuses[] | view — 试算结算状态 |

**管理 / 初始化**

| 函数 | 说明 |
|---|---|
| `initialize(tappRegistry_)` | 一次性,在 proxy 上 |
| `owner()` / `transferOwnership(newOwner)` | 合约管理员 |
| `setTappRegistry(newRegistry)` | TappRegistry 重新部署后用来切换指向;ack 状态从新 registry 读 |
| `repinKmsProvider(provider)` | 只用于 TappRegistry 重新部署后:把 KMS provider 重新绑定到新 registry 里该 app 的实例。仅合约 owner |
| `tappRegistry()` / `domainSeparator()` / `LOCK_TIME()` | view |

**事件:** `Deposited`、`RefundRequested`、`RefundWithdrawn`、`VoucherSettled`、`EarningsWithdrawn(provider, to, amount)`、`ServiceUpdated`、`ServiceRemoved(provider, appOwner)`、`KmsProviderBound(provider, appId, endorsingNode, epoch)`、`KmsProviderRepinned(provider, previousEpoch, epoch)`、`OwnershipTransferred`、`TappRegistryUpdated`。

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

**KMS provider**(节点设 `PROVIDER_KEY_SOURCE=kms`):第 3 步不用 `--signer`,provider 地址和
节点背书从节点的 `/api/info` 读:

```bash
OWNER_KEY=0x<owner-key> go run ./cmd/provider/ register --kms \
  --contract       0x<sandbox-serving-proxy> \
  --app-id         0g-sandbox-provider \
  --url            https://<sandbox-host> \
  --price-per-cpu  <neuron/cpu/min> \
  --price-per-mem  <neuron/memGB/min> \
  --fee            <neuron>
```

给打印出来的 provider 地址充一次 gas 即可,之后重启也一直用它付结算 gas。

---

## 设计说明

- **Proxy 地址永不变** — 升级只替换 implementation，proxy 地址是对外稳定地址
- **结算开放** — `settleFeesWithTEE` 任何人可调用，provider 由 voucher 内的 `v.provider` 字段标识，与 `msg.sender` 无关
- **Trust root 委托** — SandboxServing 只持有商业条款；TEE 签名身份与用户 acknowledgement 都在 TappRegistry 中，每次 voucher 验签都会查询
- **provider 就是 TEE signer(v2)** — 合约里所有 provider 地址(services 键、voucher 收款人、余额桶、earnings 账)都是 TappRegistry 某个节点的 TEE signer 地址。signer 私钥不出 enclave、随机器消亡,所以注册/注销/提现全部归 appId 的 TappRegistry owner。一个 appId 多个节点:每个 signer 账本完全隔离——余额**刻意不共享**(各自独立部署的 billing proxy 用各自 Redis 做预留准入,互相看不见在途预留,共享余额会超卖)。
- **KMS provider** — 取代轮换的方案:provider 是 app 从 KMS 取的 key(`GetSecretResource`,material 为 `provider` 的 hex),每次启动都一样,所以余额、nonce、service 和 gas 在重启、重建后都不用动。
  - **验证:** tapp-server ≥ 0.11.0 把这把 key 列进每个节点的 evidence(`runtime_data.kms_keys`),链上则由在册节点背书注册。
  - **结算:** KMS provider 的 voucher 只在 app 仍是它绑定的那个 TappRegistry 实例时有效。app 最后一个节点被摘掉后,谁重新注册这个 appId,KMS 就把同一把 key 给谁。这个绑定在 `removeService` 之后也保留,所以新注册者碰不到已有的余额和 earnings,即使用户 ack 了新 app 也一样。
  - **前提:** KMS 必须开着 attested admission。不开的话,app owner 可以把一把普通 key 登记成节点,在 TEE 外拿到 provider key。
  - **每个 appId 只能有一个 billing 实例:** app 的所有节点派生出同一个 provider。它的余额桶和 voucher nonce 会被几个看不到彼此预留和 nonce 计数的 billing 实例共用,这和 node-signer 账本要隔离是同一个原因。换机器时先停旧的 billing,再起新的。
- **轮换** — 机器重建产生新 signer。流程:TappRegistry 先 add 新节点(新旧并存)→ 排空旧 signer 的 voucher 队列 → `cmd/provider rotate` 迁移服务条目 → 摘旧节点。用户余额走退款通道离开死 signer;`removeService` 把它的 earnings 清给 owner。
- **`appId` 每个 signer 写一次** — 绑定后只能就地改 URL/价格。要换 `appId` 先 `removeService`(用户余额、待退款、已结算 nonce 保留——nonce 不动,重注册后旧 voucher 无法重放)。
