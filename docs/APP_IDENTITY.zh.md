# 应用身份

接入 AgenticID attestor 时配置：

```dotenv
PROVIDER_KEY_SOURCE=kms
TLS_ENABLED=true
BACKEND_TAPP_SOCKET=/run/tapp/tapp.sock
ATTESTOR_APP_ID=your-attestor-app-id
```

tapp 在 claim 时配置可信 scan URL 和 TLS pin。scan 需要支持带类型的应用身份，
tapp 需要支持 `GetAppSigner`、`GetAppTlsCert` 和 `ResolveAppIdentity`。
sandbox 不再单独配置 scan pin 或 attestor EOA；`CHAIN_ID` 和 `TAPP_REGISTRY`
确定身份查询范围。

provider 使用 material `70726f7669646572`（hex `provider`）派生的稳定
secp256k1 app signer，保持已有 KMS provider 的地址、余额、voucher nonce 和
服务注册。节点 key 继续用于节点证明和 provider 背书。是否进入 evidence 以
tapp 的明确返回结果为准，不能根据非空地址推断。tapp 从本次启动的 eventlog
恢复已度量身份，因此不再定期重复获取 secret。

`TLS_ENABLED=true` 让 billing 使用 tapp 返回的 P-256 证书直接终止 TLS。
外部入口应透传这条 TLS 连接，或使用同一 attested app key。SandboxServing
应登记 HTTPS 业务 URL；Registry 的 `teeUrl` 是管理入口，不能代替业务 URL。

`ATTESTOR_APP_ID` 对应的稳定 app signer 可以作为应用管理员。人工操作钱包仍可
配置在 `ADMIN_ADDRESSES`，sandbox app owner 的现有管理权限保留。
attestor 的 node signer 不会自动获得管理权限。查询失败、身份过期或注册实例
检查失败时，不授予应用管理权限。

已验证的 current 节点可以仅有 node identity，或先有 TLS、尚无 `app_signer`，作为 standby 保留。
发现时仍验证其 metadata，但不将其 TLS pin 或 lease 纳入服务身份。至少一个 current
节点必须声明合法稳定 app signer，所有非空 signer 声明必须一致。即使来自 standby，
非法、过期或非生产环境的观测仍会导致发现失败。

第一次成功查询后，授权的 attestor 注册 epoch 和 owner 会保存到 Redis：
`identity:attestor:<chain>:<registry-lowercase>:<appId>:instance`。
每次应用管理权限检查都会在一次链上调用中核对 epoch 和 owner。同名 app 注销重注册后，即使
KMS key 未变，也需要操作员明确重新授权：核实目标部署后，移除这一条授权记录，
再由新的已验证查询建立 epoch。Redis 应启用持久化，以保留跨进程重启的授权状态。

Registry 的 epoch 是时间戳；同一 owner 在同一区块内注销重注册，在 Registry 提供
唯一注册序号之前无法区分。即使时间戳相同，换成另一 owner 也会被拒绝。

当前 sandbox 自身不主动向 attestor 发起 HTTP 请求。需要发起请求的组件保留
现有 attestor 业务 URL 作为连接地址，并使用发现的 TLS pin 认证身份；本次不新增
业务 URL 注册合约。

## Sealed workload 证明（v2）

创建 sealed workload 必须使用 `PROVIDER_KEY_SOURCE=kms`。`SANDBOX_SEAL_ATTESTATION`
由稳定 app signer 签署；节点 signer 继续用于节点注册与 KMS provider 背书。每个容器
仍有独立随机生成的 secp256k1 key。

v2 envelope 签入 `chain_id`、`registry_address`、按原始 UTF-8 字节解释的 `app_id`、
`registered_at`、`seal_id`、压缩容器公钥、镜像 hash 与签发时间。创建时从链上取得自身
注册实例；注册实例或 KMS service binding 不可用、无效时拒绝创建。调用者提供的字段
不能替换签发者 scope。

签名 digest 为以下九个 32 字节 ABI word 编码后的 Keccak-256：
`domainHash, chainId, registry, appIdHash, registeredAt, sealId, containerPubkeyHash,
imageHash, issuedAt`。其中 `domainHash = keccak256("0g.sandbox.ImageAttestation.v2")`；
字符串取精确 UTF-8 字节，公钥 hash 取原始 33 字节压缩公钥。整数使用无符号大端 ABI
word，registry 地址左侧补零。不添加 `personal_sign` 包装。

配套 attestor 和 sealed runtime 都需要支持 v2。attestor 通过 Scan 与 SandboxServing
认证 signer 和注册实例，同时保留 framework 镜像白名单校验。正常重启只改变节点
signer，app signer 和注册实例不变，已保存的 v2 证明因此可以继续使用。同 appId
注销重注册后，即使 KMS key 未变，也按新实例处理，不能继承旧容器证明。

不提供 v1/node 签名回退。已有 v1 容器需要 owner 明确执行 reset 才能取得新证明。
应先保存运行时状态；reset 保留 agent 身份与已提交的 iData/settings，不保留原容器
文件系统。更新时应同时部署新版 sandbox、attestor 与 sealed 镜像。
