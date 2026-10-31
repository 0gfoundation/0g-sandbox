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

`ATTESTOR_APP_ID` 对应的稳定 app signer 可以作为应用管理员；在
`PROVIDER_KEY_SOURCE=kms` 时，还会通过 `GET /api/attestor-tls` 发布 attestor 的 TLS pin
（见下文）。未使用 KMS provider 时，服务仍会启动并记录警告，该端点返回 503；未设置
`TLS_ENABLED=true` 时同样只记录警告。人工操作钱包仍可配置在 `ADMIN_ADDRESSES`，
sandbox app owner 的现有管理权限保留。attestor 的 node signer 不会自动获得管理权限。
查询失败、身份过期或注册实例检查失败时，不授予应用管理权限，也不产生 pin。

已验证的 current 节点可以仅有 node identity，或先有 TLS、尚无 `app_signer`，作为 standby 保留。
发现时仍验证其 metadata，但不将其 TLS pin 或 lease 纳入服务身份。至少一个 current
节点必须声明合法稳定 app signer，所有非空 signer 声明必须一致。即使来自 standby，
非法、过期或非生产环境的观测仍会导致发现失败。

第一次成功查询后，授权的 attestor 注册 epoch 和 owner 会保存到 Redis：
`identity:attestor:<chain>:<registry-lowercase>:<appId>:instance`。
每次应用管理权限检查以及每次刷新 attestor TLS 声明，都会在一次链上调用中核对 epoch 和 owner。同名 app 注销重注册后，即使
KMS key 未变，也需要操作员明确重新授权：核实目标部署后，移除这一条授权记录，
再由新的已验证查询建立 epoch。Redis 应启用持久化，以保留跨进程重启的授权状态。

Registry 的 epoch 是时间戳；同一 owner 在同一区块内注销重注册，在 Registry 提供
唯一注册序号之前无法区分。即使时间戳相同，换成另一 owner 也会被拒绝。

sandbox provider 自身不主动向 attestor 发起 HTTP 请求。需要发起请求的组件保留
现有 attestor 业务 URL 作为连接地址，并使用发现的 TLS pin 认证身份；sealed 容器
从下文的签名声明中取得这些 pin。本次不新增业务 URL 注册合约。

## Sealed 容器证书（v3）

创建 sealed workload 必须使用 `PROVIDER_KEY_SOURCE=kms`。`SANDBOX_SEAL_ATTESTATION`
由稳定 app signer（`ProviderKey()`）签署，而不是轮换的节点 signer。每个容器仍在
`SANDBOX_SEAL_KEY` 中获得独立随机生成的 secp256k1 key。节点 key 继续用于节点注册与
KMS provider 背书。

创建时从链上取得自身注册实例；注册实例或 KMS service binding 不可用、无效时拒绝创建。
调用者提供的字段不能替换签发者 scope。

证书为紧凑 JSON，字段顺序如下：

```json
{"version":3,"chain_id":16602,"registry_address":"0x<TappRegistry>","app_id":"<provider app id>","registered_at":1792108800,"seal_id":"<64 位小写 hex，无 0x>","pubkey":"0x<33 字节压缩 secp256k1 公钥>","image_hash":"sha256:<64 位小写 hex>","ts":1793318445,"signature":"0x<65 字节 r||s||v>"}
```

签名采用 EIP-712：

- Domain：`EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)`，
  name 为 `0G Sandbox ImageAttestation`，version 为 `3`，`chainId = chain_id`，
  `verifyingContract = registry_address`。
- Type：`ImageAttestation(string appId,uint64 registeredAt,bytes32 sealId,bytes containerPubkey,bytes32 imageHash,uint64 issuedAt)`，
  其中 `appId = app_id`（精确 UTF-8 字节），`registeredAt = registered_at`，`sealId` 为
  `seal_id` 的 32 字节，`containerPubkey` 为 `pubkey` 的 33 字节原始值，`imageHash` 为
  `sha256:` 之后的 32 字节，`issuedAt = ts`。
- `ProviderKey()` 直接签署 `keccak256(0x1901 ‖ domainSeparator ‖ structHash)`，不添加
  `personal_sign` 前缀。V 为 27/28；验证方同时接受 27/28 与 0/1。

`internal/proxy/testdata/image-attestation-v3.json` 是与 attestor、sealed runtime
逐字节一致的共享 known-answer 向量。

sealed 创建时，调用者 env 中有键不是合法变量名（`[A-Za-z_][A-Za-z0-9_]*`）时 provider
拒绝创建，因为运行时按 `KEY=VALUE` 拼接条目。随后从调用者 env 中删除所有以 `SANDBOX_` 开头的键，以及
`ATTESTOR_TLS_DESCRIPTOR`、`ATTESTOR_ALLOW_UNPINNED_DEV` 和 `TEE_SIGNER_ADDRESS`，
再写入 `SANDBOX_SEAL_KEY`、`SANDBOX_SEAL_ATTESTATION`，以及在其 `PROXY_DOMAIN` 已设置时
写入 `SANDBOX_PROXY_DOMAIN`。其余调用者 env（如 `ATTESTOR_URL`、`SEAL_BOOT_TICKET`、
`SEAL_SECRET_ENV`）原样透传：provider 无法区分 attestor 的 create 与其他调用者的 create。
所有返回 sandbox 的响应都会从其 env 中删除 `SANDBOX_SEAL_KEY` 和 `SEAL_BOOT_TICKET`，
owner 与其他调用者都读不回私钥或 attestor 的启动票据；`SANDBOX_SEAL_ATTESTATION` 保留，
attestor 把首次 provision 绑定到其中认证的公钥。

证书只证明镜像 digest，因此容器必须运行镜像自身的 entrypoint。sealed 创建时从镜像自身
的 config 读取 entrypoint，Daytona 记录中的 entrypoint 与之不同，或 snapshot 由 Dockerfile
构建（`buildInfo`）时拒绝创建。snapshot 本身不受限制：非 sealed 沙箱仍可使用覆盖了
entrypoint 的 snapshot。

证书认证的是 Daytona 实际启动的镜像。Daytona 从不运行 snapshot 的 `imageName`：创建
snapshot 时它把该镜像复制到内部 registry，作为记录的 `ref`
（`<registry>/daytona/daytona-<hash>:daytona`），runner 启动的是 `ref`。因此 sealed 创建
还会读取 `ref`，只有 `ref` 与 `imageName` 被认证的 manifest 具有相同的镜像 config digest
（index 取其 `linux/amd64` 镜像）时才放行，否则 **400** 拒绝。config 按未压缩 digest
列出每一层，Docker 拉取时会校验，因此 config digest 相同即文件系统与 entrypoint 相同；
不比较 manifest digest，因为 runner 拉取后重新推送可能改变它。尚无 `ref` 的 snapshot，
或镜像无法读取的 snapshot，同样拒绝。

内部 registry 中的镜像不能通过 provider API 修改。`POST /api/registry/pull` 拒绝
（**409**）把已存在的 tag 指向另一个 manifest（再次导入同一 manifest 不做任何改动），
并拒绝（**400**）写入 Daytona 与 snapshot 注册使用的引用：名为 `daytona-*` 的仓库和
以 `d-` 开头的 tag。

正常重启只改变节点 signer，app signer 和注册实例不变，已保存的证书因此可以继续使用。
同 appId 注销重注册后，即使 KMS key 未变，也按新实例处理，不能继承旧证书。

不再签发或接受更早版本的证书。持有旧证书的容器在上线时重新创建；应同时部署新版
sandbox、attestor 与 sealed 镜像。

## Attestor TLS 声明（`GET /api/attestor-tls`）

sealed 容器使用本 provider 签名的 SPKI pin 认证 attestor 的 TLS 端点。该端点公开，
无需认证：

- 仅在设置了 `ATTESTOR_APP_ID` 且 `PROVIDER_KEY_SOURCE=kms` 时提供；否则所有请求返回
  `503 {"error":"attestor tls unavailable"}`。
- 所有响应都带 `Cache-Control: no-store`。
- 提供缓存的声明不消耗限流令牌。只有等待进行中刷新的请求共享一个令牌桶：每秒 20 个
  请求，突发 50。超出的请求不等待：缓存的声明仍可提供时返回它，否则返回 `429` 和
  `Retry-After: 1`。

响应体为紧凑 JSON，字段顺序如下，末尾无换行：

```json
{"version":3,"chain_id":16602,"registry_address":"0x<TappRegistry>","tls_spki_sha256":["<64 位小写 hex，无 0x>"],"issued_at":1793318460,"expires_at":1793318748,"signature":"0x<65 字节 r||s||v>"}
```

- `tls_spki_sha256` 含 1–16 项，严格升序且不重复：即通过上文注册实例与 Redis 检查后，
  所发现 attestor app 的 pin。与发现逻辑一致，不含 standby 的 pin。
- `issued_at < expires_at <= issued_at + 300`。`expires_at` 取发现结果的 lease 结束时间，
  最多为 `issued_at` 之后 300 秒。
- EIP-712 domain：name 为 `0G Sandbox AttestorTLS`，version 为 `3`，`chainId = chain_id`，
  `verifyingContract = registry_address`。Type：
  `AttestorTLS(bytes32[] tlsSpkiSha256,uint64 issuedAt,uint64 expiresAt)`；数组编码为其
  各 32 字节元素拼接后的 keccak256。与证书相同，由 `ProviderKey()` 直接签署 digest，
  因此从容器证书恢复出的 signer 也用于验证此声明。
- `internal/proxy/testdata/attestor-tls-v3.json` 是共享的 known-answer 向量。

provider 每次身份解析只签一份声明，并向所有调用者返回相同字节：

- `now < expires_at - 30` 时立即返回缓存的声明。
- 从 `expires_at - 60` 起，后台刷新签发下一份声明。刷新同一时间只有一个，最多每 5 秒
  尝试一次；attestor 身份 lease 剩余不足 60 秒时重新解析。
- 从 `expires_at - 30` 起，或尚无任何声明时，请求在上述限额内等待进行中的刷新。
- 刷新失败时继续返回缓存的声明直到 `expires_at - 5`，之后返回 `503` 和 `Retry-After: 5`。

声明不包含 attestor 的 app id、signer 或 URL。attestor 在自己的
`GET /attestor-tls?provider=<provider 地址>` 原样转发该声明；这些 pin 属于哪个 attestor，
仍由本 provider 的 `ATTESTOR_APP_ID` 配置决定。
