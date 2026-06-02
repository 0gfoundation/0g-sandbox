# 测试指南

> English version: [TESTING.md](TESTING.md)

---

## 层级概览

| 层级 | 命令 | 外部依赖 | 耗时 |
|---|---|---|---|
| 单元测试 | `go test ./...` | 无 | < 5s |
| 链集成测试 | `go test ./internal/chain/...` | 需先 `make build-contracts` | < 5s |
| 组件测试 | `go test ./cmd/billing/` | 需先 `make build-contracts` | < 30s |
| 端到端测试 | `go test -tags e2e ./cmd/billing/` | 真实链 + Redis + Daytona | 数分钟 |

---

## 一、单元测试

无任何外部依赖，全部使用 httptest / miniredis / go-ethereum 模拟后端在内存中运行。

```bash
go test ./...
```

| 包 | 测试内容 |
|---|---|
| `internal/auth` | EIP-191 签名验证、nonce 防重放、过期检查 |
| `internal/billing` | OnCreate/OnStart/OnStop/OnDelete、voucher 生成、nonce 递增 |
| `internal/daytona` | HTTP 客户端、认证头、错误处理 |
| `internal/proxy` | 路由拦截、owner 注入/过滤、余额检查 |
| `internal/settler` | 结算状态处理、persistStop、DLQ |
| `internal/voucher` | EIP-712 签名、usageHash 构造 |

---

## 二、链集成测试

使用 go-ethereum 模拟后端（无网络），需要编译好的合约产物。

**前置条件**

```bash
make build-contracts   # Docker 编译 Solidity，输出到 contracts/out/
```

**运行**

```bash
go test ./internal/chain/... -v
```

在模拟链上部署完整的 impl + beacon + proxy 三件套，验证
`GetLastNonce`、`SettleFeesWithTEE`、`GetAccount` 等链上操作。

---

## 三、组件测试

在内存中运行完整计费管道（auth → proxy → billing → settler），
使用模拟链 + miniredis + mock Daytona httptest，不访问任何外部服务。

**前置条件**

```bash
make build-contracts   # 组件测试需要合约字节码来部署
```

**运行**

```bash
go test ./cmd/billing/ -v -run TestComponent
```

| 测试 | 场景 |
|---|---|
| `TestComponent_HappyPath` | 创建 sandbox → create-fee 结算 → 链上 lastNonce == 1 |
| `TestComponent_InsufficientBalance` | 余额为 0 → InsufficientBalance → Daytona 自动停止 |
| `TestComponent_OwnershipFiltering` | owner 标签注入、列表过滤、跨用户 403 |

---

## 四、端到端测试（E2E）

连接真实 0G Galileo 测试网、真实 Redis 和真实 Daytona 实例。
需要 `-tags e2e` 编译标签才会编译。

### 前置条件

**1. 账户准备**

E2E 测试运行前需要 provider 的 `appId` 已在 TappRegistry 中注册、SandboxServing
中已绑定该 `appId`，并且测试钱包已充值并 acknowledge：

```
tapp.registerApp(appId, ...)        # 注册 TEE-app trust root
sandbox.addOrUpdateService(...)     # 把 URL/价格/createFee 绑定到 appId
sandbox.deposit(user, provider, ...)# 用户账户充值（建议 ≥ 0.1 0G）
tapp.acknowledgeApp(appId)          # 用户在当前 TEE 节点集合下 acknowledge
```

通过 CLI 走完这套流程：

```bash
# Provider 侧
PROVIDER_KEY=0x<provider-key> go run ./cmd/provider/ register \
  --api      http://<billing-host>:8080 \
  --app-id   <appId> \
  --price-per-cpu <neuron/cpu/min> \
  --price-per-mem <neuron/memGB/min> \
  --create-fee    <neuron>

# 用户侧
USER_KEY=0x<user-key> go run ./cmd/user/ deposit \
  --provider 0x<provider-address> \
  --amount   0.1
USER_KEY=0x<user-key> go run ./cmd/user/ acknowledge \
  --provider 0x<provider-address> \
  --tapp     0x<tapp-registry-address>
```

**2. 本地服务**
- Redis 在 `localhost:6379`（或通过 `REDIS_ADDR` 指定）
- Daytona 在 `localhost:3000`（或通过 `INTEGRATION_DAYTONA_URL` 指定）

### 环境变量

| 变量 | 默认值 | 说明 |
|---|---|---|
| `MOCK_TEE` | — | **必填**，设为 `true` 启用 mock TEE 模式 |
| `MOCK_APP_PRIVATE_KEY` | — | **必填**，TEE 私钥（`0x` 前缀可选） |
| `INTEGRATION_RPC_URL` | `https://evmrpc-testnet.0g.ai` | 链 RPC |
| `INTEGRATION_CONTRACT` | `0x24cD979DBd0Ae924a3f0c832a724CF4C58E5C210` | 合约地址 |
| `INTEGRATION_DAYTONA_URL` | `http://localhost:3000` | Daytona API 地址 |
| `INTEGRATION_DAYTONA_KEY` | `daytona_admin_key` | Daytona admin key |
| `REDIS_ADDR` | `localhost:6379` | Redis 地址 |
| `REDIS_PASSWORD` | — | Redis 密码（可选） |
| `INTEGRATION_USER_KEY` | 同 `MOCK_APP_PRIVATE_KEY` | 用户私钥（默认与 TEE 共用） |
| `INTEGRATION_VOUCHER_INTERVAL_SEC` | `5` | voucher 生成周期（秒） |
| `INTEGRATION_CREATE_FEE` | `1` | 创建费（neuron） |
| `INTEGRATION_COMPUTE_PRICE` | `1` | 计算费单价（neuron/sec） |

### 运行

```bash
MOCK_TEE=true \
MOCK_APP_PRIVATE_KEY=0xYOUR_PRIVATE_KEY_HERE \
go test -v -tags e2e ./cmd/billing/ -run TestE2E -timeout 10m
```

运行单个测试：

```bash
MOCK_TEE=true \
MOCK_APP_PRIVATE_KEY=0xYOUR_PRIVATE_KEY_HERE \
go test -v -tags e2e ./cmd/billing/ -run TestE2E_AutoStopInsufficientBalance -timeout 10m
```

自定义计费参数：

```bash
MOCK_TEE=true \
MOCK_APP_PRIVATE_KEY=0xYOUR_PRIVATE_KEY_HERE \
INTEGRATION_VOUCHER_INTERVAL_SEC=10 \
INTEGRATION_CREATE_FEE=100 \
INTEGRATION_COMPUTE_PRICE=50 \
go test -v -tags e2e ./cmd/billing/ -run TestE2E -timeout 10m
```

### 端到端测试用例

| 测试 | 场景 | 预期结果 |
|---|---|---|
| `TestE2E_CreateFeeSettled` | 创建 sandbox | 链上 nonce +1（create-fee voucher） |
| `TestE2E_ComputeFeeSettled` | 运行 6 个计费周期后停止 | nonce 至少 +8（create-fee + 周期 compute × 6 + OnStop）；实际 delta 受 generator 对齐影响 |
| `TestE2E_InsufficientBalance` | 余额为 0 的账号创建 sandbox | 代理返回 HTTP 402，Daytona 不会收到请求 |
| `TestE2E_AutoStopInsufficientBalance` | 临时账号充值恰好够一个计费周期 | `stop:sandbox:<id>` 出现后被清理，sandbox 被 Daytona 停止 |

> `TestMain` 负责启动共享环境（settler + generator + proxy server），
> 所有 `TestE2E_*` 共用同一环境顺序执行，无需重复启动。
>
> **nonce delta 说明：** `TestE2E_ComputeFeeSettled` 使用 `t.Cleanup` 停止 sandbox，
> 该 OnStop voucher 在后台异步结算，可能在下一个测试的观测窗口内完成，属于正常的异步时序。

---

## 附：Daytona 客户端集成测试

`internal/daytona` 包含 3 个真实 Daytona 测试，在 `go test ./...` 时自动运行。
若 Daytona 不可达（`GET /api/health` 失败），这 3 个测试会自动 skip，不影响 CI。

```bash
# 覆盖 Daytona 地址（默认 localhost:3000）
DAYTONA_API_URL=http://localhost:3000 \
DAYTONA_ADMIN_KEY=daytona_admin_key \
go test ./internal/daytona/... -v -run TestIntegration
```
