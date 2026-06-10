# 0G Sandbox — SDK 与 API 参考

> English version: [API_REFERENCE.md](API_REFERENCE.md)

> 计费代理:通过 EIP-191 钱包签名认证用户、管理 Daytona 沙箱,并用 0G 代币在链上结算用量费用。

---

## 目录

1. [快速开始](#快速开始)
2. [认证](#认证)
3. [HTTP API 参考](#http-api-参考)
4. [Toolbox API(远程执行)](#toolbox-api远程执行)
5. [CLI 参考](#cli-参考)
6. [链上合约 API](#链上合约-api)
7. [数据类型与对象](#数据类型与对象)
8. [错误参考](#错误参考)
9. [计费概念](#计费概念)

---

## 快速开始

### 1. 在链上充值

```bash
USER_KEY=0x<your-private-key> go run ./cmd/user/ deposit \
  --provider 0x<provider-address> \
  --amount 0.01 \
  --rpc https://evmrpc-testnet.0g.ai \
  --chain-id 16602 \
  --contract 0x<contract-address>
```

### 2. Acknowledge(确认)TEE signer

```bash
USER_KEY=0x<your-private-key> go run ./cmd/user/ acknowledge \
  --provider 0x<provider-address> \
  --rpc https://evmrpc-testnet.0g.ai \
  --chain-id 16602 \
  --contract 0x<contract-address>
```

### 3. 创建沙箱

```bash
USER_KEY=0x<your-private-key> go run ./cmd/user/ create \
  --api http://<0g-sandbox>:8080
```

### 4. 在沙箱里执行命令

```bash
USER_KEY=0x<your-private-key> go run ./cmd/user/ exec \
  --api http://<0g-sandbox>:8080 \
  --id <sandbox-id> \
  --cmd "python3 --version"
```

---

## 认证

除公开端点外,所有 `/api/` 端点都需要三个由 **EIP-191** 钱包签名派生的 HTTP 头。

### 必需的请求头

| Header | 格式 | 说明 |
|--------|------|------|
| `X-Wallet-Address` | `0x<hex>` | 你的以太坊钱包地址 |
| `X-Signed-Message` | Base64 字符串 | 签名的请求对象,先 JSON 编码再 base64 |
| `X-Wallet-Signature` | `0x<hex>` | 65 字节 ECDSA 签名(R\|\|S\|\|V,V ∈ {27,28}) |

### 签名请求对象

构造以下 JSON 对象,并**严格按此字段顺序**序列化:

```json
{
  "action":      "create",
  "expires_at":  1709500000,
  "nonce":       "a3f8c2d1e4b7069512345678abcdef01",
  "payload":     {},
  "resource_id": ""
}
```

| 字段 | 类型 | 规则 |
|------|------|------|
| `action` | string | 操作名:`create`、`list`、`stop`、`delete`、`toolbox` 等 |
| `expires_at` | int64 | Unix 时间戳(秒)。必须 `> now` 且 `≤ now + 5 分钟`。 |
| `nonce` | string | 32 字符 hex(16 随机字节)。每个 nonce 只接受一次(存在 Redis 直到过期)。 |
| `payload` | JSON | 请求体 JSON 对象。无请求体时用 `{}`。 |
| `resource_id` | string | 资源相关操作的沙箱 ID;`create` / `list` 用空字符串。 |

### 签名算法

```
1. 构造 SignedRequest JSON 对象(字段顺序如上)
2. 序列化为 UTF-8 JSON 字节  →  msgBytes
3. 计算 EIP-191 哈希:
     prefix = "\x19Ethereum Signed Message:\n" + len(msgBytes)   (十进制,非 hex)
     hash   = keccak256(prefix + msgBytes)
4. 用私钥对 hash 做 ECDSA 签名
5. 追加 V: sig = R||S||V,其中 V ∈ {27, 28}  (go-ethereum 返回 0/1 时加 27)
6. X-Signed-Message  = base64StdEncode(msgBytes)
   X-Wallet-Signature = "0x" + hex(sigBytes)
   X-Wallet-Address   = checksumHex(publicKeyToAddress(privKey))
```

### Go 实现

```go
import (
    "crypto/rand"
    "encoding/base64"
    "encoding/hex"
    "encoding/json"
    "fmt"
    "time"

    "github.com/ethereum/go-ethereum/crypto"
)

type signedRequest struct {
    Action     string          `json:"action"`
    ExpiresAt  int64           `json:"expires_at"`
    Nonce      string          `json:"nonce"`
    Payload    json.RawMessage `json:"payload"`
    ResourceID string          `json:"resource_id"`
}

func signRequest(privKey *ecdsa.PrivateKey, action, resourceID string, payload json.RawMessage) (xSignedMessage, xSignature, xWalletAddr string) {
    addr := crypto.PubkeyToAddress(privKey.PublicKey)

    nonceBuf := make([]byte, 16)
    rand.Read(nonceBuf)

    req := signedRequest{
        Action:     action,
        ExpiresAt:  time.Now().Add(3 * time.Minute).Unix(),
        Nonce:      hex.EncodeToString(nonceBuf),
        Payload:    payload,
        ResourceID: resourceID,
    }
    msgBytes, _ := json.Marshal(req)

    prefix := fmt.Sprintf("\x19Ethereum Signed Message:\n%d", len(msgBytes))
    hash := crypto.Keccak256([]byte(prefix), msgBytes)

    sigBytes, _ := crypto.Sign(hash, privKey)
    sigBytes[64] += 27 // normalize V

    return base64.StdEncoding.EncodeToString(msgBytes),
        "0x" + hex.EncodeToString(sigBytes),
        addr.Hex()
}
```

### JavaScript / ethers.js 实现

```js
import { ethers } from "ethers";

async function signRequest(wallet, action, resourceId, payload = {}) {
  const nonce = ethers.hexlify(ethers.randomBytes(16)).slice(2); // 32 hex chars
  const expiresAt = Math.floor(Date.now() / 1000) + 180; // 3 min

  const req = { action, expires_at: expiresAt, nonce, payload, resource_id: resourceId };
  const msgBytes = new TextEncoder().encode(JSON.stringify(req));

  // ethers.signMessage prepends the EIP-191 prefix automatically
  const signature = await wallet.signMessage(msgBytes);

  return {
    "X-Wallet-Address":   wallet.address,
    "X-Signed-Message":   btoa(String.fromCharCode(...msgBytes)),
    "X-Wallet-Signature": signature,
  };
}

// Usage
const wallet = new ethers.Wallet("0x<private-key>");
const headers = await signRequest(wallet, "create", "", {});
const resp = await fetch("http://<proxy>/api/sandbox", {
  method: "POST",
  headers: { ...headers, "Content-Type": "application/json" },
  body: JSON.stringify({}),
});
```

---

## HTTP API 参考

### 公开端点(无需认证)

#### `GET /healthz`
存活探针。
```json
{ "ok": true }
```

#### `GET /api/info`
服务器配置与定价。
```json
{
  "contract_address":     "0x...",
  "provider_address":     "0x...",
  "chain_id":             16602,
  "rpc_url":              "https://evmrpc-testnet.0g.ai",
  "create_fee":           "60000000000000000",
  "compute_price_per_sec":"0",
  "voucher_interval_sec": 60,
  "min_balance":          "60000000000000000",
  "sealed_only":          false
}
```

> `compute_price_per_sec` 是 flat-rate 兜底值;当链上配置了 per-resource 定价
> (`price_per_cpu_per_min` / `price_per_mem_gb_per_min`)时它为 `"0"`。
> `min_balance` = `create_fee + compute_price_per_sec × voucher_interval_sec`。
>
> `sealed_only` 反映 provider 的 `SEALED_ONLY` 环境变量。为 `true` 时,任何未设
> `"sealed": true` 的 create 请求都会被 HTTP 400 拒绝。

#### `GET /api/providers`
本 billing 实例所服务的那个 provider(`PROVIDER_ADDRESS`)的链上 service 数据。返回一个
数组——目前只有一条——实时读自 `SandboxServing.services`。若该 provider 没有有效的
service 注册(例如调用 `deregisterService` 之后),返回 `[]`。
```json
[
  {
    "address":                "0x...",
    "url":                    "https://...",
    "app_id":                 "0g-sandbox-provider-testnet",
    "price_per_cpu_per_min":  "1000000000000000",
    "price_per_cpu_per_sec":  "16666666666666",
    "price_per_mem_gb_per_min":"500000000000000",
    "price_per_mem_gb_per_sec":"8333333333333",
    "create_fee":             "60000000000000000"
  }
]
```
所有金额单位为 **neuron**(1 0G = 10¹⁸ neuron)。

> **多 provider 发现(市场视图):** broker 暴露自己的 `GET /api/providers`,由一个
> 链上 indexer 支撑——它扫描 `ServiceUpdated` 事件并列出**所有活跃 provider**,且按
> TappRegistry 过滤:一个 provider 只有在仍是其 `appId` 的当前 owner 时才出现
> (已注销 / 被取代的会被剔除)。市场视图用 broker 端点;本 billing 端点只报告自己的
> provider。

---

### 沙箱端点(需认证)

#### `POST /api/sandbox` — 创建沙箱

**Headers:** 认证头(action = `"create"`,resource_id = `""`)

**Body:**
```json
{
  "image":   "ubuntu:22.04",
  "sealed":  false
}
```
所有字段可选。

| 字段 | 类型 | 说明 |
|------|------|------|
| `image` | string | 要用的 Docker 镜像或 snapshot 名 |
| `sealed` | bool | `true` 则创建 sealed 沙箱(见下) |

**Sealed 沙箱**(`"sealed": true`):
- 通过内部 registry 把镜像解析为 content digest(无法解析则硬失败)
- 生成一个临时 secp256k1 密钥对作为容器的签名身份
- 向容器注入两个环境变量:`SANDBOX_SEAL_KEY`(私钥;从响应中剥除)和 `SANDBOX_SEAL_ATTESTATION`(JSON,含 `seal_id`、`pubkey`、`image_hash`、TEE `signature`、`ts`)
- 设置标签 `0g-sealed: "true"` 和 `0g-seal-id: <32-char hex>`
- 在沙箱整个生命周期内**阻断 SSH 和 toolbox 访问**

**Provider 侧 `SEALED_ONLY=true`:** 运营方在 billing 服务器设了这个环境变量后,任何
**不带** `"sealed": true` 的 create 请求都会被 HTTP 400 拒绝
(`{"error": "this provider only accepts sealed sandboxes; set \"sealed\": true in the create request"}`)。可先查 `GET /api/info` 的 `sealed_only` 字段。

**响应 `200`:** Sandbox 对象(见 [数据类型](#数据类型与对象))

**计费:** 立即扣除 CREATE_FEE。所需最低余额:
`CREATE_FEE + COMPUTE_PRICE_PER_SEC × VOUCHER_INTERVAL_SEC`

---

#### `GET /api/sandbox` — 列出沙箱

**Headers:** 认证头(action = `"list"`,resource_id = `""`)

**响应 `200`:** 过滤到调用者自己的沙箱对象数组。

也可用 `GET /api/sandbox/paginated`,语义相同。

---

#### `GET /api/sandbox/:id` — 获取沙箱

**Headers:** 认证头(action = `"list"`,resource_id = `":id"`)

**响应 `200`:** Sandbox 对象
**响应 `403`:** 非 owner

---

#### `POST /api/sandbox/:id/stop` — 停止沙箱

**Headers:** 认证头(action = `"stop"`,resource_id = `":id"`)

**响应 `200`:** 来自 Daytona 的响应
**计费:** 为上次 voucher 以来的时长出一张最终 compute voucher。

---

#### `DELETE /api/sandbox/:id` — 删除沙箱

**Headers:** 认证头(action = `"delete"`,resource_id = `":id"`)

**响应 `200`:** 来自 Daytona 的响应
**计费:** 删除前出一张最终 compute voucher。

---

#### `POST /api/sandbox/:id/start` — 启动已停止的沙箱

**Headers:** 认证头(action = `"start"`,resource_id = `":id"`)

**响应 `200`:** 来自 Daytona 的响应
**响应 `402`:** 未 acknowledge TEE signer
**计费:** 开启新的 compute session。

---

#### `POST /api/sandbox/:id/archive` — 归档沙箱

**Headers:** 认证头(action = `"archive"`,resource_id = `":id"`)

**响应 `200`:** 来自 Daytona 的响应
**计费:** 出一张最终 compute voucher;关闭 compute session。

---

#### `PUT /api/sandbox/:id/labels` — 更新标签

**Headers:** 认证头(action = `"labels"`,resource_id = `":id"`)

**Body:**
```json
{ "my-label": "value" }
```
> 注意:`daytona-owner` 受保护,不可被覆盖。

---

#### `POST /api/sandbox/:id/ssh-access` — 获取 SSH 访问令牌

**Headers:** 认证头(action = `"ssh-access"`,resource_id = `":id"`)

**响应 `200`:**
```json
{
  "sshCommand": "ssh -p 2222 user@<host> -i <key>",
  "token":      "<short-lived-token>"
}
```
**响应 `403`:** 沙箱是 sealed 的(创建时 `"sealed": true`)—— SSH 访问被永久阻断。

---

#### `POST /api/sandbox/:id/ensure-billing` — 确保计费 session 存在

**Headers:** 认证头(action = `"ensure-billing"`,resource_id = `":id"`)

幂等。为那些 `OnCreate` 钩子可能没触发的沙箱(例如客户端在 2xx 响应到达前断开)确保存在计费 session。

**响应 `200`:**
```json
{ "ok": true }
```

> **被禁用的端点:** `POST /api/sandbox/:id/autostop` 和
> `POST /api/sandbox/:id/autoarchive` 返回 `403 Forbidden` —— 这些生命周期策略
> 由 billing 代理管理,用户不能覆盖。

---

### 事件与 Session 端点(需认证)

#### `GET /api/events` — 查询结算事件

**Headers:** 认证头(action = `"list"`,resource_id = `""`)

**Query 参数:** `?lookback=<blocks>`(默认 ≈ 43200,按 2s/块约 24h;`0` = 全部历史)

**响应 `200`:**
```json
{
  "current_block": 7700000,
  "from_block":    7656800,
  "events": [
    {
      "user":      "0x...",
      "provider":  "0x...",
      "total_fee": "60001200",
      "nonce":     "42",
      "status":    "SUCCESS",
      "tx_hash":   "0x...",
      "block":     7654321,
      "timestamp": 1709500000
    }
  ]
}
```

---

#### `GET /api/sessions` — 活跃计费 session(仅管理员)

**Headers:** 认证头(action = `"list"`,resource_id = `""`)

调用者钱包必须在 `ADMIN_ADDRESSES` 中。

**响应 `200`:** session 对象数组(见 [数据类型](#数据类型与对象))

---

#### `DELETE /api/sessions/:id` — 关闭一个计费 session(仅管理员)

删除某沙箱的 Redis session 并从 broker 注销它。沙箱本身不会被停止或删除 —— 用于清理在
billing 代理控制路径之外被归档的沙箱遗留的孤儿 session。幂等:即使无 session 也成功。

**响应 `200`:** `{"ok": true}`

---

#### `POST /api/archive-all` — 归档所有运行中的沙箱(仅管理员)

重新部署前使用。先停止再归档所有 `started`/`starting` 沙箱;`stopped` 的直接归档。

**响应 `200`:**
```json
{ "archived": ["id1", "id2"], "skipped": [], "failed": [] }
```

---

#### `DELETE /api/sandbox/:id/force` — 强制删除任意沙箱(仅管理员)

`DELETE /api/sandbox/:id` 的运营方意图覆盖版,无视 owner 删除。与标准 delete(它也通过
`withOwnerOrAdmin` 接受管理员)区分开,便于在审计日志里 grep 出运营方覆盖操作。

**响应 `200`:** 来自 Daytona 的响应
**计费:** 出一张最终 compute voucher。

---

#### `POST /api/sandbox/:id/force-stop` — 强制停止任意沙箱(仅管理员)

`POST /api/sandbox/:id/stop` 的运营方意图覆盖版。同步:阻塞直到 Daytona 报告沙箱处于
stopped/archived/error 状态,这样后续 start 不会和进行中的 stop 竞态。

**响应 `200`:** `{"id": "...", "state": "stopped"}`
**计费:** 触发 `OnStop` 和 broker 注销。

---

#### `GET /api/audit-log` — 本地 Redis 计费事件日志(仅管理员)

只追加的计费相关事件流(created / stopped / auto_stopped / settled)。与 `GET /api/events`
(读链上 `VoucherSettled` 日志)不同 —— 这个捕获 broker-proxy 侧可能永远不上链的状态变化
(例如首张 voucher 之前就被停止)。

**响应 `200`:** `{timestamp, kind, sandbox_id, owner, …}` 条目数组。

---

#### 队列 + 可观测性(仅管理员)

这些为运营 dashboard 提供数据;仅管理员钱包能从 `cmd/user` 访问。

| 路径 | 用途 |
|---|---|
| `GET /api/queue/summary` | 每个 `(user, provider)` 的待结算 voucher 数 |
| `GET /api/queue/dlq` | 死信队列中的 voucher(签名不符 / provider 不符) |
| `POST /api/queue/dlq/discard` | 按签名摘要丢弃一条 DLQ 条目 |
| `POST /api/queue/aggregate` | 把某个 `(user, provider)` 的待结算 voucher 合并成一张 |
| `GET /api/observability` | 队列深度 + 最近告警历史 |

---

## Toolbox API(远程执行)

toolbox 代理把请求转发给沙箱内的 Daytona toolbox,并做 owner 校验。路径格式:
`/api/toolbox/{sandboxId}/toolbox/{action}`。

**注意:** 对 sealed 沙箱(`0g-sealed: "true"`)返回 `403 Forbidden`。

**认证头:** action = `"toolbox"`,resource_id = `"{sandboxId}"`

支持所有 HTTP 方法(GET、POST、PUT、DELETE)。

### 常用 Action

| Action | 方法 | 说明 |
|--------|------|------|
| `process/execute` | POST | 执行 shell 命令 |
| `files` | GET | 列文件 |
| `files/download` | GET | 下载文件(`?path=<path>`) |
| `files/upload` | POST | 上传文件 |
| `files/find` | GET | 搜索文件 |
| `git/status` | GET | Git status |
| `git/clone` | POST | 克隆仓库 |
| `git/commit` | POST | Git commit |
| `git/push` | POST | Git push |
| `git/pull` | POST | Git pull |
| `project-dir` | GET | 获取项目目录路径 |
| `user-home-dir` | GET | 获取用户 home 目录 |

### `POST /api/toolbox/:id/toolbox/process/execute`

**Body:**
```json
{ "command": "echo hello", "timeout": 30 }
```

**响应 `200`:**
```json
{ "exitCode": 0, "result": "hello\n" }
```

### 示例:用 curl 列文件

```bash
curl -X GET "http://<proxy>/api/toolbox/<sandbox-id>/toolbox/files" \
  -H "X-Wallet-Address: 0x..." \
  -H "X-Signed-Message: <base64>" \
  -H "X-Wallet-Signature: 0x..."
```

---

## CLI 参考

`cmd/user` 二进制提供了一个同时覆盖链上和代理操作的参考客户端。设置环境变量
`USER_KEY=0x<private-key>` 可免去每次传 `--key`。

### 链上子命令

#### `balance` — 查询账户余额

```bash
USER_KEY=0x<key> go run ./cmd/user/ balance \
  [--address 0x<address>]           # 默认 key 对应地址
  [--provider 0x<provider>]         # 设置后显示该 provider 的 nonce 和 earnings
  [--rpc <url>]                     # 默认: https://evmrpc-testnet.0g.ai
  [--chain-id <id>]                 # 默认: 16602
  [--contract 0x<addr>]             # 默认: 0G Galileo 上已部署的合约
```

#### `deposit` — 充值 0G 代币

```bash
USER_KEY=0x<key> go run ./cmd/user/ deposit \
  --amount 0.01 \                   # 单位 0G(如 0.01 = 10^16 neuron)
  [--rpc <url>] [--chain-id <id>] [--contract 0x<addr>]
```

#### `acknowledge` — Acknowledge TEE signer

用户必须先 acknowledge 某 provider 的 TEE signer,该 provider 才能扣其账户。

```bash
USER_KEY=0x<key> go run ./cmd/user/ acknowledge \
  --provider 0x<provider-address> \
  [--revoke]                        # 传此参数则改为撤销
  [--rpc <url>] [--chain-id <id>] [--contract 0x<addr>]
```

---

### API 子命令

多数 API 子命令需要 `--api <0g-sandbox-url>` 和 `USER_KEY` 环境变量。例外:`providers` 直接读链。

#### `providers` — 列出可用 provider

直接读链上 service 注册 —— **无需 `--api`**,无需认证。

```bash
go run ./cmd/user/ providers \
  [--rpc      <rpc-url>] \
  [--chain-id <chain-id>] \
  [--contract <proxy-address>]
```

输出:
```
Found 2 provider(s) on-chain:

[1] 0xB831371eb2703305f1d9F8542163633D0675CEd7
    URL:         http://<provider-host>:8080
    Create fee:  0.0600 0G
    CPU price:   0.000017 0G/CPU/sec  (0.0010 0G/CPU/min)
    Mem price:   0.000008 0G/GB/sec   (0.0005 0G/GB/min)
    TEE signer:  0x3Dc1A35f37FBfDb82900A00d209b4f3a2124E99d (v5)
```

用这里显示的 provider 地址来做 `balance`、`acknowledge`、`deposit`。

#### `create` — 创建沙箱

```bash
USER_KEY=0x<key> go run ./cmd/user/ create \
  --api http://<proxy>:8080 \
  [--snapshot <snapshot-name>] \
  [--name     <display-name>] \
  [--class    small|medium|large] \
  [--cpu      <cores>] \
  [--memory   <gb>] \
  [--disk     <gb>]
```

#### `list` — 列出沙箱

```bash
USER_KEY=0x<key> go run ./cmd/user/ list --api http://<proxy>:8080
```

#### `start` — 启动已停止的沙箱

```bash
USER_KEY=0x<key> go run ./cmd/user/ start \
  --api http://<proxy>:8080 \
  --id <sandbox-id>
```

#### `stop` — 停止沙箱

```bash
USER_KEY=0x<key> go run ./cmd/user/ stop \
  --api http://<proxy>:8080 \
  --id <sandbox-id>
```

#### `delete` — 删除沙箱

```bash
USER_KEY=0x<key> go run ./cmd/user/ delete \
  --api http://<proxy>:8080 \
  --id <sandbox-id>
```

#### `exec` — 执行 shell 命令

```bash
USER_KEY=0x<key> go run ./cmd/user/ exec \
  --api http://<proxy>:8080 \
  --id <sandbox-id> \
  --cmd "python3 -c \"print('hello')\"" \
  [--timeout 30]                    # 秒,默认 30
```

输出:命令的 stdout/stderr。以命令的退出码退出。

#### `toolbox` — 任意 toolbox API 调用

```bash
USER_KEY=0x<key> go run ./cmd/user/ toolbox \
  --api http://<proxy>:8080 \
  --id <sandbox-id> \
  --action <action-path> \          # 如 files, git/status, process/execute
  [--method GET|POST|PUT|DELETE] \  # 默认 GET
  [--body '{"key":"value"}']        # POST/PUT 的 JSON 体
```

**示例:**
```bash
# 列文件
USER_KEY=0x<key> go run ./cmd/user/ toolbox --api http://<proxy>:8080 --id <id> --action files

# Git status
USER_KEY=0x<key> go run ./cmd/user/ toolbox --api http://<proxy>:8080 --id <id> --action git/status

# 执行进程
USER_KEY=0x<key> go run ./cmd/user/ toolbox --api http://<proxy>:8080 --id <id> \
  --action process/execute --method POST --body '{"command":"ls -la","timeout":10}'
```

#### `ssh-access` — 获取临时 SSH 访问令牌

令牌有效期 60 分钟。该令牌用作 **SSH 用户名**(无需密码)。

```bash
USER_KEY=0x<key> go run ./cmd/user/ ssh-access \
  --api http://<proxy>:8080 \
  --id <sandbox-id>
# → 打印: ssh -p 2222 TOKEN@<host>
```

用于直接 SSH 或 rsync 同步:
```bash
SSH_CMD=$(USER_KEY=0x<key> go run ./cmd/user/ ssh-access --api http://<proxy>:8080 --id <id> 2>/dev/null)
PORT=$(echo $SSH_CMD | awk '{print $3}')
USER_HOST=$(echo $SSH_CMD | awk '{print $4}')

# 直接 SSH
ssh -p $PORT -o StrictHostKeyChecking=no $USER_HOST

# rsync 本地目录到沙箱
rsync -avz --delete -e "ssh -p $PORT -o StrictHostKeyChecking=no" \
  ./my-project/ "${USER_HOST}:/home/daytona/project/"
```

---

## 链上合约 API

结算合约是部署在 0G Galileo 测试网(chain ID 16602)上的
`BeaconProxy → UpgradeableBeacon → SandboxServing` 三层栈。

> 合约完整接口、地址、部署/升级见 [`../contracts/README.zh.md`](../contracts/README.zh.md)。

### 关键函数(SandboxServing ABI)

#### `deposit(address recipient, address provider)`

向某用户、指定 provider 的账户充值 0G 代币。
```
payable; msg.value = 充值额(neuron / wei)
```

#### Acknowledge 一个 provider(TappRegistry)

provider 的 TEE trust root 存在 TappRegistry,而非 SandboxServing。用户调用
`tappRegistry.acknowledgeApp(string appId)` 接受,调用
`tappRegistry.revokeAcknowledgement(string appId)` 撤销。某 provider 的当前
`appId` 读自 `sandbox.services(provider).appId`。

当活跃 TEE 节点集合变化,或 SandboxServing 的价格/createFee 被更新时,TappRegistry 会
bump `ackVersion(appId)`,之前的 acknowledgement 自动失效 —— 用户必须重新 acknowledge,
后续 voucher 才能结算。

#### `getBalance(address user, address provider) → (balance, pendingRefund, refundUnlockAt)`

只读。返回某 `(user, provider)` 的余额和退款状态(单位 neuron)。

#### `getLastNonce(address user, address provider) → uint256`

只读。返回某 `(user, provider)` 对的最后结算 nonce。

#### `getProviderEarnings(address provider) → uint256`

只读。返回某 provider 累计赚取的 neuron 总额。

#### `services(address provider) → Service`

只读。返回 provider 注册信息(public mapping 自动 getter):
```solidity
struct Service {
    string  url;
    string  appId;
    uint256 pricePerCPUPerMin;
    uint256 pricePerMemGBPerMin;
    uint256 createFee;
}
```

#### `settleFeesWithTEE(SandboxVoucher[] vouchers, bytes[] signatures)`

由 provider 的 settler 调用。用户不直接调用。

---

## 数据类型与对象

### Sandbox 对象

```json
{
  "id":    "6f3a1b2c-...",
  "state": "started",
  "labels": {
    "daytona-owner": "0x1234...abcd",
    "0g-sealed":     "true",
    "0g-seal-id":    "a3f8c2d1e4b706951234567890abcdef"
  }
}
```

| 字段 | 类型 | 取值 |
|------|------|------|
| `id` | string | UUID |
| `state` | string | `started`、`stopped`、`starting`、`stopping`、`archived`、`error` |
| `labels["daytona-owner"]` | string | owner 钱包地址(hex) |
| `labels["0g-sealed"]` | string | 若以 `sealed: true` 创建则为 `"true"`;否则不存在 |
| `labels["0g-seal-id"]` | string | 32 字符 hex,关联沙箱到其 TEE attestation;非 sealed 沙箱无此项 |

创建时注入的 `SANDBOX_SEAL_KEY` 环境变量**会从所有 API 响应中剥除** —— 它只在容器内部可见。

### 代理 URL

当服务器配置了 `PROXY_DOMAIN` 时,沙箱内的用户自定义服务端口(如 8080、9090)可通过
Daytona 代理 URL 访问:

```
http://<port>-<sandboxId>.<PROXY_DOMAIN>/<path>
```

示例:
- `PROXY_DOMAIN=<your-ip>.nip.io:4000` → `http://8080-<id>.<your-ip>.nip.io:4000/`
- `PROXY_DOMAIN=sandbox.yourdomain.com` → `http://8080-<id>.sandbox.yourdomain.com/`

系统端口(22222/TERMINAL、2280/TOOLBOX、33333/RECORDING)无论 `public` 标志如何都无法通过此 URL 访问。

---

### Provider Info 对象

```json
{
  "address":                 "0x...",
  "url":                     "https://...",
  "app_id":                  "0g-sandbox-provider",
  "price_per_cpu_per_min":   "1000000000000000",
  "price_per_cpu_per_sec":   "16666666666666",
  "price_per_mem_gb_per_min":"500000000000000",
  "price_per_mem_gb_per_sec":"8333333333333",
  "create_fee":              "60000000000000000"
}
```

所有金额单位为 **neuron**(string)。

---

### VoucherSettled 事件

```json
{
  "user":      "0x...",
  "provider":  "0x...",
  "total_fee": "60001200",
  "nonce":     "42",
  "status":    "SUCCESS",
  "tx_hash":   "0x...",
  "block":     7654321,
  "timestamp": 1709500000
}
```

---

### 计费 Session 对象

```json
{
  "sandbox_id":     "6f3a1b2c-...",
  "owner":          "0x...",
  "state":          "started",
  "start_time":     1709490000,
  "last_voucher_at":1709496000,
  "accrued_neuron": "100002000"
}
```

---

## 错误参考

所有错误都返回 JSON:`{ "error": "<message>" }`

### HTTP 状态码

| 码 | 原因 |
|----|------|
| `400 Bad Request` | 缺必填字段或请求体格式错误 |
| `401 Unauthorized` | 缺失/无效认证头、签名过期(`expires_at ≤ now`)、签名时间过于超前(`expires_at > now + 5min`)、nonce 已用过 |
| `402 Payment Required` | 余额不足以创建沙箱,或未 acknowledge TEE signer |
| `403 Forbidden` | 沙箱归属于其他钱包;或仅 provider 端点;或受管端点(`autostop`/`autoarchive`) |
| `500 Internal Server Error` | Redis 错误或意外失败 |
| `502 Bad Gateway` | 上游 Daytona 或链 RPC 错误 |

### 认证错误信息

| `error` 字段 | 原因 |
|--------------|------|
| `missing auth headers` | 三个头中有一个或多个缺失 |
| `invalid X-Signed-Message encoding` | Base64 解码失败 |
| `invalid signed message JSON` | 解码后字节 JSON 解析失败 |
| `request expired` | `expires_at ≤ now` |
| `expires_at too far in future` | `expires_at > now + 5min` |
| `invalid signature` | ECDSA 恢复失败,或恢复出的地址 ≠ `X-Wallet-Address` |
| `nonce already used` | 该 nonce 之前见过(防重放) |

---

## 计费概念

### 代币单位

| 单位 | 值 |
|------|-----|
| 1 neuron | 10⁻¹⁸ 0G(最小单位,类似 wei) |
| 1 0G | 10¹⁸ neuron |

所有 API 金额以 **neuron** 的 `string` 表示(避免 JSON 整数溢出)。

### 计费生命周期

```
用户调用 POST /api/sandbox
  → 代理检查最低余额(CREATE_FEE + 一个 interval 的 compute)
  → 注入 daytona-owner 标签后转发给 Daytona
  → billing.OnCreate() 出 create-fee voucher + 在 Redis 开 compute session

每隔 VOUCHER_INTERVAL_SEC:
  → RunGenerator() 找出所有开着的 session
  → 出 compute voucher:每张 elapsed_sec × COMPUTE_PRICE_PER_SEC neuron

settler.Run() 排空 voucher 队列:
  → 在链上预览结算状态
  → 分批提交 SettleFeesWithTEE()
  → 遇 INSUFFICIENT_BALANCE:向 Redis 写 stop:sandbox:<id> 键

runStopHandler():
  → 从 Redis 读 stop 键
  → 对沙箱调用 Daytona stop
  → 清理 session 和 stop 键
```

### 最低余额

沙箱创建被拒,除非:

```
user_balance ≥ CREATE_FEE + COMPUTE_PRICE_PER_SEC × VOUCHER_INTERVAL_SEC
```

按默认值(`CREATE_FEE=5000000`、`COMPUTE_PRICE_PER_SEC=16667`、`VOUCHER_INTERVAL_SEC=3600`):

```
min_balance = 5_000_000 + 16_667 × 3_600 = 65_001_200 neuron ≈ 6.5 × 10⁻¹¹ 0G
```

### 结算状态码

| 码 | 名称 | 含义 |
|----|------|------|
| `0` | `SUCCESS` | 已结算;余额已扣 |
| `1` | `INSUFFICIENT_BALANCE` | 余额过低;沙箱将被自动停止 |
| `2` | `PROVIDER_MISMATCH` | voucher 的 provider ≠ tx 发送者 |
| `3` | `NOT_ACKNOWLEDGED` | 用户未 acknowledge 该 provider 的 TEE signer |
| `4` | `INVALID_NONCE` | nonce ≤ 最后结算 nonce(必须严格递增) |
| `5` | `INVALID_SIGNATURE` | TEE 签名验证失败 |

### Voucher 结构(EIP-712)

Voucher 由 enclave 内的 TEE key 签名:

```solidity
SandboxVoucher {
    address user,
    address provider,
    bytes32 usageHash,   // keccak256(sandboxID, periodStart, periodEnd, elapsedSec)
    uint256 nonce,       // per-(user,provider) 计数器,严格递增
    uint256 totalFee     // 收费,单位 neuron
}
```

| 字段 | 说明 |
|------|------|
| `user` | 被扣费的钱包地址 |
| `provider` | provider 钱包地址(从 voucher 识别,不校验 `msg.sender`) |
| `usageHash` | 不透明用量指纹:`keccak256(sandboxID ‖ periodStart ‖ periodEnd ‖ elapsedSec)` |
| `nonce` | 每 `(user, provider)` 对严格递增;启动时从链上 seed |
| `totalFee` | compute voucher 为 `elapsedSec × COMPUTE_PRICE_PER_SEC`;create voucher 为 `CREATE_FEE` |

domain separator 使用:
```
name    = "SandboxServing"
version = "1"
chainId = <chain ID>
verifyingContract = <结算合约地址>
```

用户从不直接构造或验证 voucher —— 代理自动处理。
