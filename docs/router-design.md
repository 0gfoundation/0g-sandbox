# Broker 设计文档

> 状态：设计完成，待实现
> 关联 Issue：https://github.com/0gfoundation/0g-sandbox/issues/25

---

## 1. 定位与职责边界

```
用户/前端
  │
  ├─ GET /api/providers ──────────────────► Broker（Provider 市场）
  │
  └─ POST /api/sandbox ──────────────────► billing proxy（各 provider 独立部署）
                                               │
                                      余额不足时│
                                               ▼
                                          Broker（资金路由）
                                               │
                                               ▼
                                         Payment Layer（HTTP 服务，TBD）
                                               │
                                               ▼
                                    SandboxServing 合约 deposit()
```

| 组件 | 运营方 | 职责 |
|------|--------|------|
| billing proxy | 各 Provider 独立 | 沙箱生命周期、voucher 签发、链上结算 |
| **Broker** | **0G（共享）** | Provider 发现、Payment Layer 资金路由、余额监控 |
| Payment Layer | 外部（TBD） | 统一充值入口，持有用户资金，执行 deposit() |
| SandboxServing 合约 | 共享 | 余额账本、结算、Provider 注册 |

直接往合约充值的现有用户完全不受影响，两种充值方式并存。

---

## 2. 核心数据流

### 2.1 沙箱创建

```
User → POST /api/sandbox → billing proxy

  ① 检查合约余额（现有逻辑不变）
    contract.getBalance(user, provider) >= createFee + 1 voucher 周期消耗？
    ├── 足够 → 跳到 ④（直接充值用户，Broker 不介入）
    └── 不足 → ②

  ② 调 Broker POST /api/session（仅当 BROKER_URL 已配置）
    Broker 内部流程见 §3.1
    ├── 成功 → ③
    └── 失败 / BROKER_URL 未配置 → 拒绝创建，返回 402（现有行为）

  ③ 重新检查合约余额（Payment Layer 刚充值完）
    ├── 足够 → ④
    └── 仍不足 → 拒绝创建（Payment Layer 中用户余额也不足）

  ④ 创建 Daytona 沙箱（现有逻辑不变）

  ⑤ OnCreate 计费 hook（现有逻辑不变）

  ⑥ 若 ② 成功（Broker 参与了本次创建），沙箱已自动注册监控
     （Broker 在 ② 处理时已写入 broker:session:<sandbox_id>）
```

### 2.2 余额监控

```
Broker 后台 goroutine，每 BROKER_MONITOR_INTERVAL_SEC 执行：

  1. 扫描所有 broker:session:* keys，加载 SessionEntry 列表

  2. 按 provider 分组，每组一次批量 RPC：
       contract.balanceOfBatch([user1, user2, ...], provider)
       → 得到每个 user 在该 provider 的当前余额

  3. 按 (user, provider) 聚合：
       total_burn_rate = Σ price_per_sec
                         （该 user 在该 provider 下所有活跃沙箱，来自 SessionEntry）
       voucher_interval = 该组所有沙箱的 voucher_interval_sec（同 provider 下一致）

  4. 对每个 (user, provider)：
       threshold = total_burn_rate × voucher_interval_sec × BROKER_THRESHOLD_INTERVALS
       top_up    = total_burn_rate × voucher_interval_sec × BROKER_TOPUP_INTERVALS

       if balance < threshold:
         → HTTP POST Payment Layer RequestDeposit(user, provider, top_up)
         → 记录日志（含 user, provider, balance, threshold, top_up）
         → 若 Payment Layer 返回错误，记录 warn，不影响其他监控项
```

### 2.3 沙箱停止

```
billing proxy（用户主动 DELETE 或 settler 检测到 INSUFFICIENT_BALANCE）
  → 执行现有停止逻辑（Daytona stop/delete，最终 voucher）
  → 若 BROKER_URL 已配置：
       DELETE /api/session/:sandbox_id  Body: {timestamp, signature}
    Broker：删除 broker:session:<sandbox_id>
```

---

## 3. Broker 内部逻辑

### 3.1 `POST /api/session` 处理流程

```
1. 解析请求体，校验必填字段

2. 从 indexer 获取 provider 的 teeSignerAddress
   - provider 不存在 → 403

3. 验证 TEE 签名
   message = keccak256(sandbox_id | provider_addr | user_addr |
                       cpu | mem_gb | start_time | voucher_interval_sec)
   ecrecover(EIP-191(message), signature) == teeSignerAddress
   - 不匹配 → 401

4. 防重放检查
   EXISTS broker:seen:<sandbox_id>:<start_time>
   - 已存在 → 409（幂等，返回原始结果）

5. 从合约读取该 provider 的单价（不信任 billing proxy 传入的价格）
   (pricePerCPUPerSec, pricePerMemGBPerSec) = GetServicePricing(provider_addr)

6. 计算当前沙箱费率
   price_per_sec = cpu × pricePerCPUPerSec + mem_gb × pricePerMemGBPerSec

7. 查当前合约余额
   balance = contract.getBalance(user_addr, provider_addr)

8. 计算需要充值的金额
   needed = price_per_sec × voucher_interval_sec × BROKER_TOPUP_INTERVALS
   deficit = max(0, needed - balance)

9. 若 deficit > 0：
   → HTTP POST Payment Layer RequestDeposit(user, provider, deficit)
   - Payment Layer 失败 → 返回 502（billing proxy 触发拒绝创建）

10. 写防重放 key
    SET broker:seen:<sandbox_id>:<start_time> "1" EX 604800（7天）

11. 写 session 监控记录
    SET broker:session:<sandbox_id> <SessionEntry JSON>（无 TTL，由 DELETE 清除）

12. 返回 {ok: true, amount_funded: deficit.String()}
```

### 3.2 `DELETE /api/session/:sandbox_id` 处理流程

```
1. 解析请求体中的 {timestamp, signature}

2. 从 broker:session:<sandbox_id> 读取 SessionEntry，获取 provider_addr
   - 不存在 → 404

3. 验证签名
   message = keccak256("deregister" | sandbox_id | timestamp)
   ecrecover(EIP-191(message), signature) == teeSignerAddress
   - 不匹配 → 401

4. timestamp 合法性（防重放）：|now - timestamp| < 300s

5. 删除 broker:session:<sandbox_id>

6. 返回 {ok: true}
```

---

## 4. API 规范

### `GET /healthz`

```json
{"ok": true}
```

### `GET /api/providers`

响应：
```json
[
  {
    "address":                 "0xProvider...",
    "url":                     "https://provider-a.example.com:8080",
    "tee_signer":              "0x...",
    "price_per_cpu_per_min":   "1000",
    "price_per_cpu_per_sec":   "16",
    "price_per_mem_gb_per_min":"500",
    "price_per_mem_gb_per_sec":"8",
    "create_fee":              "5000000",
    "signer_version":          "1",
    "last_indexed_block":      12345678,
    "updated_at":              "2026-03-13T10:00:00Z"
  }
]
```

### `POST /api/session`

请求：
```json
{
  "sandbox_id":           "sb-abc123",
  "provider_addr":        "0xProvider...",
  "user_addr":            "0xUser...",
  "cpu":                  2,
  "mem_gb":               4,
  "start_time":           1710000000,
  "voucher_interval_sec": 3600,
  "signature":            "0x..."
}
```

签名字段（Provider TEE，EIP-191）：
```
keccak256(sandbox_id | provider_addr | user_addr | cpu | mem_gb | start_time | voucher_interval_sec)
```

响应：
```json
{"ok": true, "amount_funded": "12345678"}
```

错误码：
- `401` 签名无效
- `403` Provider 未在合约注册
- `409` 重放请求（sandbox_id + start_time 已处理过）
- `502` Payment Layer 调用失败

### `DELETE /api/session/:sandbox_id`

请求体：
```json
{
  "timestamp": 1710003600,
  "signature": "0x..."
}
```

签名字段：`keccak256("deregister" | sandbox_id | timestamp)`

响应：`{"ok": true}`

错误码：
- `401` 签名无效
- `404` sandbox_id 不在监控中

### `GET /api/monitor`（运维只读）

只在 `BROKER_DEBUG=true` **且** `ADMIN_ADDRESSES` 非空时注册；请求必须带 `ADMIN_ADDRESSES` 中某个钱包的签名（与 billing 相同的三个认证头），否则 `401`/`403`。没有配置管理员时，即使开了 `BROKER_DEBUG` 也不注册（返回 `404`）——它列出所有 sandbox、钱包和价格，不能因为一个调试开关对外公开。

用 Foundry `cast` 调用：

```bash
KEY=0x<admin-key>; BROKER=https://<broker-host>
MSG=$(printf '{"action":"GET:/api/monitor","body_sha256":"0xe3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855","expires_at":%d,"nonce":"%s","payload":{},"resource_id":""}' \
  $(( $(date +%s) + 120 )) "$(openssl rand -hex 16)")
curl -s "$BROKER/api/monitor" \
  -H "X-Wallet-Address: $(cast wallet address --private-key $KEY)" \
  -H "X-Signed-Message: $(printf %s "$MSG" | base64 | tr -d '\n')" \
  -H "X-Wallet-Signature: $(cast wallet sign --private-key $KEY "$MSG")"
```

响应：
```json
{
  "total_sessions": 3,
  "sessions": [
    {
      "sandbox_id":           "sb-abc123",
      "user":                 "0xUser...",
      "provider":             "0xProvider...",
      "cpu":                  2,
      "mem_gb":               4,
      "price_per_sec":        "12345",
      "voucher_interval_sec": 3600,
      "registered_at":        "2026-03-13T10:00:00Z"
    }
  ]
}
```

---

## 5. Redis Schema

```
# Provider 索引
indexer:provider:last_block
  type:  string
  value: "<uint64 block number>"
  TTL:   none

indexer:provider:<lowercase_hex_addr>
  type:  string (JSON → ProviderRecord)
  value: {address, url, tee_signer,
          price_per_cpu_per_min, price_per_cpu_per_sec,
          price_per_mem_gb_per_min, price_per_mem_gb_per_sec,
          create_fee, signer_version,
          last_indexed_block, updated_at}
  TTL:   none

# 防重放
broker:seen:<sandbox_id>:<start_time>
  type:  string
  value: "1"
  TTL:   604800（7天）

# 监控集合
broker:session:<sandbox_id>
  type:  string (JSON → SessionEntry)
  value: {sandbox_id, user, provider,
          cpu, mem_gb, price_per_sec,
          voucher_interval_sec, registered_at}
  TTL:   none（由 DELETE /api/session/:id 显式删除）
```

---

## 6. Payment Layer 接口

```go
// internal/broker/payment.go

type PaymentLayer interface {
    // RequestDeposit 请求 Payment Layer 向 SandboxServing 合约的
    // (user, provider) bucket 充值 amount neuron。
    // Payment Layer 验证 Broker TEE 签名后执行 contract.deposit()。
    RequestDeposit(ctx context.Context,
        user, provider common.Address,
        amount *big.Int) error
}

// NoopPaymentLayer: Payment Layer 就绪前使用，只记日志，永远返回 nil
type NoopPaymentLayer struct{ log *zap.Logger }

// HTTPPaymentLayer: 正式实现，Broker TEE key 对请求签名
// Payment Layer 侧需预先注册 Broker 的 TEE signer 地址
type HTTPPaymentLayer struct {
    url    string
    signer *ecdsa.PrivateKey  // Broker TEE key
    log    *zap.Logger
}
```

HTTP 请求（Broker → Payment Layer）：
```
POST <PAYMENT_LAYER_URL>/deposit
Authorization: Bearer <EIP-191 签名的 token>
Content-Type: application/json

{
  "user":      "0xUser...",
  "provider":  "0xProvider...",
  "amount":    "12345678",
  "nonce":     "uuid-or-random",
  "timestamp": 1710000000
}
```

---

## 7. 配置项

### Broker（`cmd/broker/`）

| 环境变量 | 类型 | 默认值 | 必填 | 说明 |
|----------|------|--------|------|------|
| `BROKER_PORT` | int | `8081` | 否 | 监听端口 |
| `RPC_URL` | string | — | **是** | 链 RPC |
| `SETTLEMENT_CONTRACT` | string | — | **是** | SandboxServing 合约地址 |
| `CHAIN_ID` | int64 | — | **是** | 链 ID |
| `REDIS_ADDR` | string | `redis:6379` | 否 | Redis 地址 |
| `REDIS_PASSWORD` | string | — | 否 | Redis 密码 |
| `BROKER_MONITOR_INTERVAL_SEC` | int64 | `300` | 否 | 余额轮询间隔（独立于 voucher） |
| `BROKER_TOPUP_INTERVALS` | int64 | `3` | 否 | 每次补充 N 个 voucher 周期的金额 |
| `BROKER_THRESHOLD_INTERVALS` | int64 | `2` | 否 | 余额低于 N 个 voucher 周期时触发补充 |
| `PAYMENT_LAYER_URL` | string | — | 否 | 空 = NoopPaymentLayer |
| `MOCK_TEE` | bool | `false` | 否 | 开发模式 |
| `MOCK_APP_PRIVATE_KEY` | string | — | 否 | 开发模式 TEE key |

### billing proxy 新增（`cmd/billing/`）

| 环境变量 | 类型 | 默认值 | 说明 |
|----------|------|--------|------|
| `BROKER_URL` | string | — | 空 = 跳过所有 Broker 调用，行为同现在 |

---

## 8. 文件清单

### 新建

| 文件 | 说明 |
|------|------|
| `internal/indexer/indexer.go` | Provider 事件索引器（轮询 ServiceUpdated，Redis 持久化，内存缓存） |
| `internal/broker/session.go` | `POST /api/session` 和 `DELETE /api/session/:id` 处理（验签、防重放、Payment Layer 调用） |
| `internal/broker/monitor.go` | 余额监控 goroutine（批量查余额、聚合多沙箱消耗、触发补充） |
| `internal/broker/payment.go` | PaymentLayer 接口 + NoopPaymentLayer + HTTPPaymentLayer 实现 |
| `cmd/broker/main.go` | Broker 服务入口（LoadBroker、TEE key、indexer、monitor goroutine、HTTP 路由） |

### 修改

| 文件 | 改动内容 |
|------|----------|
| `internal/chain/client.go` | 新增 `GetServiceUpdatedEvents(fromBlock)`、`GetBalanceBatch(users, provider)` |
| `internal/config/config.go` | 新增 `LoadBroker()`；`ServerConfig` 加 `BrokerURL string` |
| `internal/proxy/handler.go` | 创建沙箱：余额不足时调 `POST /api/session`；停止：调 `DELETE /api/session/:id` |
| `cmd/billing/main.go` | 将 `cfg.Server.BrokerURL` 传入 proxy handler（无其他改动） |

---

## 9. 未解决项

| 项目 | 说明 |
|------|------|
| `HTTPPaymentLayer` 实现 | 依赖 Payment Layer HTTP 接口定义，目前用 NoopPaymentLayer 占位 |
| Payment Layer 注册 Broker TEE signer | 部署 Broker 后手动注册，与 billing proxy 注册 TEE signer 到合约是同一信任模式 |
| 直接充值用户的监控 | 当前不监控（billing proxy settler 兜底），后续可按需扩展 |
| Provider URL 存活检测 | 索引只反映链上状态，不检测 billing proxy URL 是否可达 |
