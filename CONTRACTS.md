# 0G Sandbox — Contract Registry

Network: **0G Galileo Testnet** (chain ID 16602)
Explorer: https://chainscan-galileo.0g.ai
Deployer/Owner: `0xB831371eb2703305f1d9F8542163633D0675CEd7`

---

## Dev Contract

> 用于本地开发和集成测试。数据可随时重置。

| Component | Address |
|-----------|---------|
| **Proxy** (stable) | `0x2024eB0Cc14316fF8Cc425bFB7CC37FD8713E9b3` |
| Beacon | `0xaa77C82Dc6b4243Ff272d88619BD4f23455CCB6E` |

**Upgrade history:**

| Date | Impl | 变更说明 |
|------|------|---------|
| initial | — | 首次部署：per-provider 余额隔离，owner 模型 |
| 2026-03-10 | `0x9a3D6C66e3e6E020D8D40d851Db76D76EBfa93f2` | 移除 `settleFeesWithTEE` 中 `msg.sender == provider` 限制，TEE key 直接签结算 tx，无需 `PROVIDER_PRIVATE_KEY` |

```env
SETTLEMENT_CONTRACT=0x2024eB0Cc14316fF8Cc425bFB7CC37FD8713E9b3
```

---

## Testnet Contract

> 正式测试网部署，用于 provider 注册和真实计费测试。

| Component | Address |
|-----------|---------|
| **Proxy** (stable) | `0xd7e0CD227e602FedBb93c36B1F5bf415398508a4` |
| Beacon | `0xe75F37A353EbCbAA497Ea752a6c910c9d0462382` |
| Implementation | `0x6B789e297bcC3c2F375779f1224b534A4c576445` |

**Deployed:** 2026-03-10
**Provider stake:** 100 0G (`100000000000000000000` neuron)

```env
SETTLEMENT_CONTRACT=0xd7e0CD227e602FedBb93c36B1F5bf415398508a4
```

---

## 设计说明

- **Proxy 地址永不变** — 升级只替换 implementation，proxy 地址是对外稳定地址
- **结算开放** — `settleFeesWithTEE` 任何人可调用，provider 由 voucher 内的 `v.provider` 字段标识，与 `msg.sender` 无关
- **Provider stake 未实现退出机制** — 质押 ETH 目前无法取回，待后续实现 `requestExit` / `withdrawStake`
