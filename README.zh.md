# 0G Sandbox

专为 Vibe Coding 打造的私有隔离沙盒，由 0G Network 提供支持。

> English version: [README.md](README.md)

---

## 问题

Vibe Coding 面临两个相互矛盾的需求：

1. **本地环境隔离不够彻底。** 在本地运行不受信任或实验性的代码，可能污染开发环境、泄露凭证或产生
   不可逆的副作用。需要完全隔离的远程沙盒环境。

2. **远程服务器受他人控制。** 租用云服务器虽然解决了隔离问题，但云厂商可以查看或篡改运行其上的
   代码和数据，沙盒环境本身不可信。

## 解决方案

**0G Sandbox** 将 [0G Tapp](https://0g.ai)（基于 TEE 的可信执行环境）与
[Daytona](https://daytona.io)（沙盒运行时）相结合，同时满足两个需求：

- **隔离性**：每个沙盒都是完全容器化的 Daytona 工作区，与用户本地环境及其他用户的沙盒完全隔离。
- **机密性**：计费代理及其 TEE 签名密钥运行在由 0G Tapp 管理的硬件 TEE 飞地（TDX）中，
  宿主机无法查看工作负载内容，也无法伪造 voucher。
  最重要的是，**Provider 无法看到用户的代码** —— 沙盒工作负载在 TEE 内运行，对基础设施运营方完全不透明。
- **无需信任的计费**：用户向 0G Network 上的 Solidity 合约充值，计算费用通过 TEE 密钥签名的
  EIP-712 voucher 结算，无需可信中介。

### 为什么不直接租云厂商的 TDX 服务器？

云厂商的 TDX 实例只保护了"代码执行"这一段。在 vibe coding 场景中，威胁面远不止于此：
你的 **prompt、上下文、中间结果**都要经过 AI 模型，而云厂商对推理过程没有任何机密性保证。

0G Sandbox 的设计目标是与 [0G Compute](https://0g.ai)（TEE 内的 AI 推理）结合，
构建全链路机密的 vibe coding 流程：

```
Prompt ──► 0G Compute（TEE 内 AI 推理）
                │
                ▼ 生成的代码
           0G Sandbox（TEE 内代码执行）
                │
                ▼ 运行结果
           在 0G Network 链上无信任结算
```

整个流程的每一步——你写了什么、AI 生成了什么、代码跑出了什么——对任何一方都不可见，
包括 0G 自己。这是云厂商作为单点信任方根本无法提供的端到端保证。

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

## 脚本工具

### 部署（首次）

分三步部署完整的 beacon-proxy 合约栈：
1. SandboxServing 实现合约（无构造参数）
2. UpgradeableBeacon（impl, deployer）
3. BeaconProxy（beacon, initialize(providerStake)）

```bash
go run ./cmd/deploy/ \
  --rpc      https://evmrpc-testnet.0g.ai \
  --key      0x<deployer-private-key> \
  --chain-id 16602 \
  --stake    0
```

输出：
```
Implementation : 0x...
Beacon         : 0x...
Proxy (stable) : 0x...   ← 将此地址设为 SETTLEMENT_CONTRACT
```

参数说明：

| 参数 | 默认值 | 说明 |
|------|---------|-------------|
| `--rpc` | `https://evmrpc-testnet.0g.ai` | EVM RPC 地址 |
| `--key` | （必填）| 部署者私钥（十六进制，0x 可选）|
| `--chain-id` | `16602` | 链 ID |
| `--stake` | `0` | 传入 `initialize()` 的 `providerStake`（neuron）|

---

### 升级

部署新实现合约并将 beacon 指向它。
**代理地址不变** — 无需更新 `.env`，无需用户重新确认。

```bash
go run ./cmd/upgrade/ \
  --rpc      https://evmrpc-testnet.0g.ai \
  --key      0x<deployer-private-key> \
  --chain-id 16602 \
  --proxy    0x<proxy-address>
```

输出：
```
New implementation : 0x...
Upgrade tx         : 0x...
Beacon             : 0x... (unchanged)
```

参数说明：

| 参数 | 默认值 | 说明 |
|------|---------|-------------|
| `--rpc` | `https://evmrpc-testnet.0g.ai` | EVM RPC 地址 |
| `--key` | （必填）| 部署者/所有者私钥 |
| `--chain-id` | `16602` | 链 ID |
| `--proxy` | （二选一*）| BeaconProxy 地址 — 自动解析 beacon |
| `--beacon` | （二选一*）| UpgradeableBeacon 地址（与 `--proxy` 二选一）|

\* 提供 `--proxy` 或 `--beacon` 其中之一。

---

### 验证

在区块浏览器上验证全部三个合约。
**只需提供代理地址** — beacon 和 impl 从链上自动解析。

```bash
./scripts/verify-contracts.sh --proxy 0x<proxy-address>
```

脚本从代理的 ERC-1967 slot 读取 beacon 地址，调用 `beacon.implementation()` 和 `beacon.owner()`，
提交三个合约的验证请求，并轮询直到全部确认。

升级后运行相同命令 — 新 impl 重新验证，beacon 和 proxy 显示 `already_verified` 并自动跳过。

可选参数：

| 参数 | 默认值 | 说明 |
|------|---------|-------------|
| `--proxy` | （必填）| BeaconProxy 地址 |
| `--rpc` | `https://evmrpc-testnet.0g.ai` | EVM RPC 地址 |
| `--api` | `https://chainscan-galileo.0g.ai/open/api` | Etherscan 兼容 API |

---

## 启动服务器

将 `.env.example` 复制为 `.env`，填入必要参数，然后：

```bash
go run ./cmd/billing/
```

关键环境变量：

| 变量 | 默认值 | 说明 |
|----------|---------|-------------|
| `DAYTONA_API_URL` | （必填）| Daytona API 地址 |
| `DAYTONA_ADMIN_KEY` | （必填）| Daytona 管理员密钥 |
| `SETTLEMENT_CONTRACT` | （必填）| BeaconProxy 合约地址 |
| `RPC_URL` | （必填）| EVM RPC 地址 |
| `CHAIN_ID` | （必填）| 链 ID（如 16602）|
| `REDIS_ADDR` | `redis:6379` | Redis 地址 |
| `COMPUTE_PRICE_PER_SEC` | `16667` | 每个沙盒的费用（neuron/秒，约 1M neuron/分钟）|
| `CREATE_FEE` | `5000000` | 每次沙盒创建的固定费用（neuron）|
| `VOUCHER_INTERVAL_SEC` | `3600` | voucher 刷新间隔（秒）|
| `PORT` | `8080` | HTTP 服务端口 |
| `MOCK_TEE` | — | 设为 `true` 用于本地开发（使用 `MOCK_APP_PRIVATE_KEY` 代替 TDX gRPC）|
| `MOCK_APP_PRIVATE_KEY` | — | `MOCK_TEE=true` 时使用的十六进制私钥 |

### SSH 网关密钥生成

SSH 网关需要两个 ed25519 密钥对，以 base64 编码存入 `.env`。
每次部署时生成一次：

```bash
# 生成网关私钥（SSH 网关服务使用）
ssh-keygen -t ed25519 -C "daytona-gateway" -f /tmp/daytona_gw -N ""
DAYTONA_SSH_GATEWAY_PRIVATE_KEY=$(base64 -w0 < /tmp/daytona_gw)

# 生成主机密钥（向 SSH 客户端标识服务器身份）
ssh-keygen -t ed25519 -C "daytona-gateway-host" -f /tmp/daytona_host -N ""
DAYTONA_SSH_GATEWAY_HOST_KEY=$(base64 -w0 < /tmp/daytona_host)

# 打印值，粘贴到 .env
echo "DAYTONA_SSH_GATEWAY_PRIVATE_KEY=$DAYTONA_SSH_GATEWAY_PRIVATE_KEY"
echo "DAYTONA_SSH_GATEWAY_HOST_KEY=$DAYTONA_SSH_GATEWAY_HOST_KEY"

# 清理临时文件
rm -f /tmp/daytona_gw /tmp/daytona_gw.pub /tmp/daytona_host /tmp/daytona_host.pub
```

这些值不得提交到代码仓库（`.gitignore` 已覆盖 `*.key`/`*.pem`；base64 值仅保存在 `.env` 中）。

### Tapp 部署（生产环境）

计费服务运行在 0G Tapp TEE 飞地内，通过 `tapp-cli` 部署：

```bash
# 构建镜像
docker build -t billing:latest .

# 部署（或修改后重新部署）
tapp-cli -s http://<tapp-server>:50051 stop-app  --app-id billing
tapp-cli -s http://<tapp-server>:50051 start-app --app-id billing -f docker-compose.yml

# 查看容器状态
tapp-cli -s http://<tapp-server>:50051 get-app-container-status --app-id billing

# 查看日志
tapp-cli -s http://<tapp-server>:50051 get-app-logs --app-id billing -n 100
```

TEE 密钥由 tapp-daemon 自动生成和管理。注册 Provider 前先获取其以太坊地址：

```bash
tapp-cli -s http://<tapp-server>:50051 get-app-key --app-id billing
# → Ethereum Address: 0x...  ← 作为 --tee-signer 传给 cmd/provider register
```

> **注意**：若重新部署后 TEE 密钥发生变化，需用新地址重新注册 Provider
> （`cmd/provider register --tee-signer <新地址>`）。
> 这会递增 `signerVersion`，所有用户须重新 acknowledge 后 voucher 才能结算。

---

## 开发

```bash
# 编译合约（需要 Docker）
make build-contracts

# 重新生成 Go 绑定
make abigen

# 运行所有测试
go test ./...

# 运行 Solidity 测试（需要 Docker）
make test-contracts
```
