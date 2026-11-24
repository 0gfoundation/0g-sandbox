# 卡住的结算交易恢复

[English](SETTLEMENT_RECOVERY.md)

settler 广播每笔结算交易后，一直等到它有 receipt 或 provider 的已确认 nonce 越过它。
它从不替换交易：替换交易若和原交易都上链，同一段用量会被扣两次。因此一笔永远不上链的
结算交易（例如出价低于链接受的价格，或被挤出 mempool）会堵住之后所有结算。

`billing cancel-pending-settlement` 用节点的 KMS provider 密钥签一笔零金额自转账，
腾出卡住的 nonce。它上链后，settler 判定原交易已丢弃，把其 voucher 重新入队并结算。

## 迹象

- 告警 `settler_tx_failure`（critical）："Settlement tx stuck pending …"，交易在
  `tx` 字段。连续 12 次、每 5 秒一次轮询后触发。
- 该交易的 `eth_getTransactionReceipt` 返回 null，provider 的已确认 nonce
  （`eth_getTransactionCount <provider> latest`）不变。

## 参数

| 参数 | 取值 |
|------|------|
| `--chain-id` | 节点的 `CHAIN_ID` |
| `--provider` | 节点的 provider 地址（`/api/info` 的 `provider_address`） |
| `--tx-hash` | 卡住的交易（告警的 `tx`，或 settler 日志的 `tx` 字段） |
| `--nonce` | provider 的已确认 nonce，十进制 |
| `--max-fee-per-gas-wei` | 高于原交易的 fee cap，且不低于 base fee 加 tip |
| `--tip-wei` | 可选；高于原交易的 tip。不给时用 RPC 建议的 tip；建议值低于链接受的最低 tip 时给出 |
| `--broadcast` | 签名并发送。不给时只打印计划 |

## 保护条件

以下全部成立时才会签名：

- 节点通过 Tapp socket 使用 KMS provider 密钥（`PROVIDER_KEY_SOURCE=kms`，没有
  `MOCK_TEE`），且 `--chain-id` 与节点配置一致。
- Redis 里 provider 的待定结算记录（`settler:pending-tx:<provider>`）是同一笔交易和
  nonce。签名前查一次，发送前再查一次。
- 原交易没有 receipt，provider 的已确认 nonce 等于 `--nonce`。
- provider 地址上没有代码。
- 若 RPC 仍能查到原交易：它是 provider 发往结算合约的 `settleFeesWithTEE` 调用，且
  fee cap 和 tip 都低于这笔作废交易的。
- 经证明的 KMS 签名者就是 provider。
- provider 余额够付 fee cap 下的 21000 gas。

## 在 TEE 节点上运行

provider 密钥只在节点内部可用，所以命令以节点 compose 里的一次性服务运行，镜像与
`sandbox` 相同。按本次事故的值加两个服务：

```yaml
x-settlement-recovery: &settlement-recovery
  image: ${SANDBOX_IMAGE}
  group_add: ["0"]
  environment:
    DAYTONA_API_URL: http://api:3000
    DAYTONA_ADMIN_KEY: ${DAYTONA_ADMIN_KEY}
    REDIS_ADDR: sandbox-redis:6379
    REDIS_PASSWORD: ${REDIS_PASSWORD}
    SETTLEMENT_CONTRACT: ${SETTLEMENT_CONTRACT}
    CHAIN_ID: ${CHAIN_ID}
    RPC_URL: ${RPC_URL}
    BACKEND_APP_NAME: ${BACKEND_APP_NAME}
    BACKEND_TAPP_SOCKET: /run/tapp/tapp.sock
    PROVIDER_KEY_SOURCE: ${PROVIDER_KEY_SOURCE}
  volumes:
    - /run/tapp/tapp.sock:/run/tapp/tapp.sock
  depends_on:
    sandbox-redis:
      condition: service_healthy
  networks: [app-net]
  restart: "no"

services:
  settlement-recovery-preview:
    <<: *settlement-recovery
    profiles: [settlement-recovery-preview]
    command:
      - cancel-pending-settlement
      - --chain-id=<chain id>
      - --provider=<provider address>
      - --tx-hash=<stuck transaction>
      - --nonce=<confirmed nonce>
      - --max-fee-per-gas-wei=<wei>

  settlement-recovery-broadcast:
    <<: *settlement-recovery
    profiles: [settlement-recovery-broadcast]
    command:
      - cancel-pending-settlement
      - --chain-id=<chain id>
      - --provider=<provider address>
      - --tx-hash=<stuck transaction>
      - --nonce=<confirmed nonce>
      - --max-fee-per-gas-wei=<wei>
      - --broadcast
```

1. compose 属于 app 的度量，加服务就是一次 app 更新（`updateApp`）加节点重启，会打断
   正在运行的沙箱。两个 profile 只在点名时启动。
2. 启动一次 `settlement-recovery-preview`（tapp-cli `start-service`）。日志打印
   `Cancellation plan: … fee_cap_wei=… tip_wei=… max_cost_wei=…` 和
   `Dry run: cancellation not signed or broadcast.`
3. 核对计划后，启动一次 `settlement-recovery-broadcast`。日志打印
   `Broadcast cancellation <hash>`。
4. 先等这笔作废交易的 receipt，再做别的。之后 settler 判定原交易已丢弃（第二次轮询确认），
   把这批重新入队并结算。
5. 节点下一次计划更新时删掉这两个服务。
