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
