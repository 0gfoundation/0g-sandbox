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

## 快速上手：在私有沙盒中运行 OpenClaw

**前提条件**：已安装 [Claude Code](https://claude.ai/claude-code)。

### 方式一 — 安装 Claude Code 插件（推荐，无需克隆仓库）

在 Claude Code 中执行：

```
/plugin marketplace add 0gfoundation/0g-sandbox
/plugin install 0g-private-sandbox@0g-sandbox
/reload-plugins
```

之后随时通过以下命令调用：

```
/0g-private-sandbox
```

### 方式二 — 克隆仓库

```bash
git clone https://github.com/0gfoundation/0g-sandbox.git
cd 0g-sandbox
claude
```

然后用自然语言描述需求，例如：

> "我想用 0G private sandbox 来玩 OpenClaw"

---

Claude 会引导你完成后续所有步骤。需要填写配置时，你唯一需要提供的就是托管的测试网 Broker：

| 项目 | 值 |
|------|-----|
| **测试网 Broker** | `https://private-sandbox-testnet.0g.ai` |

Claude（或 CLI）会从 Broker 的 `GET /api/info` 读取其余信息：`contract_address`、`rpc_url`（`https://evmrpc-testnet.0g.ai`）和 `chain_id`（`16602`），因此无需手动填写其他内容。

---

## 合约部署

合约架构、部署/升级/验证操作及合约地址见 [`CONTRACTS.zh.md`](contracts/README.zh.md)。

---

## 启动服务器

将 `docker/sandbox/.env.dev` 复制为 `.env`，填入必要参数，然后：

```bash
cp docker/sandbox/.env.dev .env
# 编辑 .env
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
| `COMPUTE_PRICE_PER_SEC` | `16667` | 计算费用 fallback（链上 Provider 注册后以链上值为准）|
| `CREATE_FEE` | `5000000` | 创建沙盒固定费用 fallback（链上注册后以链上值为准）|
| `VOUCHER_INTERVAL_SEC` | `60` | voucher 刷新间隔（秒）|
| `SSH_GATEWAY_HOST` | — | SSH 命令中重写的网关主机（如 `<provider-ip>`）；未设置时退回到浏览器 hostname |
| `PROXY_DOMAIN` | — | 沙盒服务端口 URL 的域名模板：`http://<port>-<id>.<PROXY_DOMAIN>/<path>`。可用 `<your-ip>.nip.io:4000`（nip.io）或 `sandbox.yourdomain.com`（配合 nginx 的真实域名）。|
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
docker build --target sandbox -t 0g-sandbox:dev .

# 部署（或修改后重新部署）
tapp-cli -s http://<tapp-server>:50051 stop-app  --app-id 0g-sandbox
tapp-cli -s http://<tapp-server>:50051 start-app --app-id 0g-sandbox -f docker-compose.yml

# 查看容器状态
tapp-cli -s http://<tapp-server>:50051 get-app-container-status --app-id 0g-sandbox

# 查看日志
tapp-cli -s http://<tapp-server>:50051 get-app-logs --app-id 0g-sandbox -n 100
```

TEE 密钥由 tapp-daemon 自动生成和管理。Provider 在 TappRegistry 中注册的是 `appId`，
而不是 raw signer 地址；TappRegistry 记录该 app 当前的活跃 TEE 节点集合，billing proxy
在每次 voucher 验签时都会查询。

```bash
# 查看当前 app 的 trust root（composeHash、镜像 hash 列表、TEE 节点）
tapp-cli -s http://<tapp-server>:50051 get-app-info --app-id 0g-sandbox
```

> 若 TEE 密钥发生轮换（重新部署、手动 `addNode` / `removeNode`），TappRegistry 会
> 自动递增 `ackVersion(appId)`，所有用户的旧 acknowledgement 都会失效，需通过
> `cmd/user acknowledge` 重新签署后 voucher 才能继续结算。

---

## Broker：用户接入门户

**Broker** 是运行在 TEE 中的用户接入门户。用户不直接连接 Provider，而是通过 Broker 完成
Provider 发现、请求路由和余额自动充值。

```
用户 ──► Broker（TEE）
              │
              ├── Provider 市场      从链上事件索引 Provider URL
              ├── 反向代理           /proxy/:addr/* → Provider 后端（消除 CORS 问题）
              ├── 余额监控           每隔 T_monitor 秒轮询链上余额
              └── Payment Layer 客户端  余额不足时调用 deposit(user, provider, amount)
```

### 为什么要在 TEE 中运行？

Broker 用 TEE 密钥对 Payment Layer 请求签名，让 Payment Layer 能够验证充值指令来自合法的、
未经篡改的 Broker，而非伪造的调用方。信任根植于 TEE 硬件，而非运营者的手动配置。

### Provider 市场

Broker 通过链上事件索引已注册的 Provider（`cmd/broker` → `internal/indexer`），
并在 `GET /api/providers` 暴露列表。Dashboard 前端通过此接口让用户选择 Provider，
无需用户知道任何 Provider 的直接 URL。

### 余额监控与自动充值

沙盒启动或重启时，billing proxy 向 Broker 注册 session（`POST /api/session`）。
Broker 追踪每个 session 的 CPU/内存，计算用户的总消耗速率。当链上余额低于阈值时，
自动调用 Payment Layer 完成充值。

#### 参数调优

所有参数从一个核心变量推导：**T_react**（从触发告警到资金到账的时间）：

| 参数 | 公式 | 自动充值（T_react ≈ 60s）| 手动充值（T_react = 10 min）|
|------|------|--------------------------|--------------------------|
| `BROKER_MONITOR_INTERVAL_SEC` | `VOUCHER_INTERVAL_SEC / 2` | **30s** | 30s |
| `BROKER_THRESHOLD_INTERVALS` | `T_react / T_monitor` | **3** | 20 |
| `BROKER_TOPUP_INTERVALS` | `THRESHOLD_INTERVALS × 2` | **6** | 40 |

- **Threshold** = burn_rate × interval × `THRESHOLD_INTERVALS` — 触发充值请求的余额阈值
- **Topup 金额** = burn_rate × interval × `TOPUP_INTERVALS` — 充值后目标余额
- 自动充值链上延迟 ≈ 30–60s（Payment Layer 排队 + 约 2 个区块确认，0G 出块约 6s）

> **日志缓冲区说明**：tapp-cli 日志缓冲区固定大小（约 50 行）。`BROKER_MONITOR_INTERVAL_SEC=6`
> 时，告警期间约 2 分钟缓冲区就会填满，`get-app-logs` 返回的全是旧日志。
> 建议保持 `BROKER_MONITOR_INTERVAL_SEC ≥ 30`。

### 环境变量

| 变量 | 默认值 | 说明 |
|----------|---------|-------------|
| `SETTLEMENT_CONTRACT` | （必填）| BeaconProxy 合约地址（与 billing proxy 相同）|
| `RPC_URL` | `https://evmrpc-testnet.0g.ai` | EVM RPC 地址 |
| `CHAIN_ID` | `16602` | 链 ID |
| `BROKER_PORT` | `8082` | HTTP 端口 |
| `BROKER_MONITOR_INTERVAL_SEC` | `300` | 余额轮询间隔（秒）|
| `BROKER_THRESHOLD_INTERVALS` | `2` | 余额 < burn × interval × N 时触发告警 |
| `BROKER_TOPUP_INTERVALS` | `3` | 充值目标为 burn × interval × N neuron |
| `PAYMENT_LAYER_URL` | — | Payment Layer HTTP 地址；为空则仅记录日志（noop）|
| `BROKER_DEBUG` | `false` | 开启 `GET /api/monitor` 查看实时 session 列表 |

### Tapp 部署

Broker 作为独立 tapp 应用（`0g-broker`）与 `0g-sandbox` 并行运行：

```bash
# 构建（多阶段 Dockerfile 的 broker 阶段）
docker build --target broker -t 0g-broker:latest .
docker push <registry>/0g-broker:latest

# 部署
tapp-cli -s http://<tapp-server>:50051 stop-app  --app-id 0g-broker
tapp-cli -s http://<tapp-server>:50051 start-app --app-id 0g-broker \
  -f docker/broker/docker-compose.yml

# 查看日志
tapp-cli -s http://<tapp-server>:50051 get-app-logs --app-id 0g-broker --service broker -n 50
```

Billing proxy 通过 `BROKER_URL=http://<broker-host>:8082` 与 Broker 通信。

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
