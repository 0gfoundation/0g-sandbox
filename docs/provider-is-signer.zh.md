# V2 设计:provider = TEE signer

> 状态:设计稿,待评审。分支 `feat/provider-is-signer`。
> 前置结论(已确认):机器重启会换 TEE key,服务重启不会,频率不高。

## 一句话

砍掉独立的 provider 钱包:**记账身份(provider)直接用节点的 TEE signer 地址**,
管理与提现归 appOwner。voucher 的签名者就是收款人,"signer↔provider 绑定"问题
从"需要校验"变成"根本不存在"。

## 身份模型

| 身份 | key 在哪 | 干什么 | 生命周期 |
|---|---|---|---|
| **owner**(appOwner) | 冷钱包 | 注册/注销服务、提现、授权 admin | 永久 |
| **provider = signer** | TEE enclave 内,不可导出 | 签 voucher、付结算 gas、余额/nonce/earnings 的账本 key | 跟机器走,重启机器即轮换 |

- 概念和命名全部保留:voucher 的 `provider` 字段、`services`、`providerEarnings`、
  `(user, provider)` 余额桶都不改名,只是 provider 的值 = TEE 地址。
- EIP-712 voucher 结构**不变**(字段类型一致,typehash 不动)。

## 合约改动(SandboxServing)

破坏性变更,**新部署 proxy**,不走 upgradeTo(余额账本 key 的语义变了)。

1. `addOrUpdateService(signer, url, prices…)` — **owner 调**:
   `require(msg.sender == tap.getAppInfo(appId).owner)` 且 `signer` 是该 appId
   的在册 TappRegistry 节点。appId 由 service 绑定,set-once 语义保留。
2. `removeService(signer)` — **owner 调**(取代 provider 自签的 deregisterService;
   旧 key 重启即失,必须由 owner 能注销,否则僵尸 service 永远清不掉,
   broker 会继续向死桶充值)。
3. `withdrawEarnings(signer)` — **owner 调**,收益打给 owner。
4. 结算验签:`recovered == v.provider && tap.getNode(appId, v.provider).addedAt != 0`
   (appId 取自 `services[v.provider].appId`)。
5. **删除** #56 的 `authorizeProviders / authorizeProvider / revokeProvider`
   (委托钱包不存在了)。
6. **不做** declareRotation / migrateBalance(已决定:轮换低频,用户走退款通道;
   以后有需要再加,beacon 增量升级即可)。
7. 用户侧 deposit / requestRefund / withdrawRefund / 2h LOCK_TIME 全部不变。

## Go 侧(billing server)

- `internal/chain`:重新 abigen;voucher 的 provider 字段和启动时的 service 查询
  改用 **TEE key 自己的地址**(不再读 `PROVIDER_ADDRESS` env)。
- `internal/settler`:
  - Redis 队列键 `voucher:<signer>`(自动派生);
  - **新增暂停逻辑**:自己的 signer 不在 TappRegistry 在册节点时,不提交结算、
    只攒队列(现有 IsActiveNode 检查+告警之上加一个闸),堵住
    "服务已起、add-node 未上链"窗口的 DLQ 损失。
- `internal/config` / `auth`:`OWNER_ADDRESS` 取代 `PROVIDER_ADDRESS`;
  **owner 恒为 admin**,`ADMIN_ADDRESSES` 是追加而非替换。
- `/api/info` 暴露 `provider`(=当前 signer)和 `owner`。

## CLI

- `cmd/provider`:
  - `register`/`removeService`/`withdraw` 改 owner key 签名,`register` 加 `--signer`;
  - 删 `authorize-provider` / `revoke-provider`;
  - 新增 `rotate`:机器重启后一条命令走完
    「等旧队列排空 → add-node-onchain 新 signer → （用户走退款迁移）→
    旧队列确认清零后 remove-node-onchain 旧 signer」。
- `cmd/user`:deposit/balance/acknowledge 的目标地址从 `/api/info` 自动取当前 signer;
  文档明确"充值是充给某台节点的"。

## broker

- provider 当不透明地址用的逻辑照跑;
- indexer/monitor:识别 service 被 owner `removeService` 后停止对旧桶 top-up;
- broker 栈合约同样新部署换地址。

## 轮换 runbook(机器重启后)

```
1. 服务用新 key 起来(settler 检测到 signer 不在册 → 自动暂停提交,只攒账)
2. add-node-onchain 新 signer(新旧节点并存,两边的账都合法)
   → settler 检测到在册,放闸,新旧账都开始结
3. 等旧 signer 的队列排空(/api/queue/summary 清零)
4. remove-node-onchain 旧 signer(质押锁 1 天后 withdraw)
   + owner removeService(旧 signer) 清掉僵尸 service
5. 用户对旧桶余额走退款(requestRefund → 2h → withdrawRefund → 充到新地址)
```

## 已知取舍(评审确认过)

- **gas 浮存陪葬**:TEE 地址的剩余 gas 随 key 丢失,小额勤充。
- **用户轮换体验**:3 笔 tx + 2h 锁 + 服务中断;低频可接受,不为它加合约面。
- **appId 转手**:新 owner 可提旧 owner 任期内 earnings(与现状同性质,文档标注)。
- **结算边界仍是 appId 级**:同 appId 多节点互签在合约上可行,靠 attested 代码防;
  但 v2 下 payee=签名者,互签只能把钱签给"自己作为 payee"的桶,危害面比 #56 模型更小。

## 存量清算 / 上线顺序

1. 部署 v2 proxy(cmd/deploy),`.env` 换 `SETTLEMENT_CONTRACT`;
2. TappRegistry 不动:appId、节点、ack 全部沿用,**用户不用重新 ack**;
3. 老合约进入只退不进:停新建 → 结清最后一批 voucher → 通知用户退款;
4. sandbox 栈与 broker 栈各自走一遍 1-3。
