# Application identities

For an AgenticID-attestor deployment, configure:

```dotenv
PROVIDER_KEY_SOURCE=kms
TLS_ENABLED=true
BACKEND_TAPP_SOCKET=/run/tapp/tapp.sock
ATTESTOR_APP_ID=your-attestor-app-id
```

Claim tapp with the intended Scan URL and TLS SPKI pin. Scan must support typed
application identities, and tapp must support `GetAppSigner`, `GetAppTlsCert`,
and `ResolveAppIdentity`. No separate Scan pin or attestor EOA is configured in
sandbox. `TAPP_REGISTRY` and `CHAIN_ID` define the discovery scope.

The provider uses the stable secp256k1 application signer under material
`70726f7669646572` (hex `provider`). This preserves the existing KMS provider
address, balances, voucher nonces, and service registration. Node keys continue
to sign node attestations and provider endorsements. The measured identity flag
comes from tapp's explicit evidence result, not merely a returned address.
Periodic secret re-fetches are unnecessary: tapp restores measured application
identities from this boot's event log.

`TLS_ENABLED=true` makes billing terminate TLS itself with the P-256 certificate
returned by tapp. A frontend must pass this TLS connection through, or use the
same attested app key, for callers to match the Scan-provided pin. Register the
HTTPS business URL in SandboxServing; the node's registry `teeUrl` is the
management endpoint and does not substitute for that URL.

`ATTESTOR_APP_ID` adds the discovered stable app signer as an application admin
and, with `PROVIDER_KEY_SOURCE=kms`, publishes the attestor's TLS pins at
`GET /api/attestor-tls` (see below). Without a KMS provider the server still
starts and logs a warning, and the endpoint answers 503; without `TLS_ENABLED=true`
it also only logs a warning. Human operators may still use `ADMIN_ADDRESSES`; the
sandbox app owner retains its existing administration rights. An attestor's rotating
node signer is not automatically an admin. Discovery errors, expired results, or a
failed current registry-instance check do not grant application-admin permissions
or produce pins.

A verified current node with only its node identity, or TLS but no `app_signer`, may be a standby. Discovery
validates its metadata but excludes its TLS pin and lease from service identity.
At least one current node must declare a valid stable app signer; all nonempty
signer declarations must agree. Invalid, expired or non-production observations
still fail discovery, including those from a standby.

The first successful lookup stores the authorized attestor registration epoch
and owner in Redis under `identity:attestor:<chain>:<registry-lowercase>:<appId>:instance`.
Both are checked against one live registry read on every application-admin check
and every attestor TLS statement refresh.
An app name removed and registered again needs deliberate operator
re-authorization, even if its KMS key is unchanged. To authorize that new
instance, verify the intended deployment, remove this one authorization record
from the provider's Redis, and let a fresh verified lookup establish the new
epoch. Keep Redis persistence enabled to preserve authorization across restarts.

The registry's epoch is a timestamp. A remove/re-register by the same owner in
the same block is indistinguishable until the registry supplies a unique
registration counter. A different owner is rejected even with the same timestamp.

The sandbox provider does not initiate HTTP calls to the attestor. Components that
do call it retain their configured business URL as a locator and authenticate
TLS using the discovered app pins; sealed containers obtain those pins from the
signed statement below. No new business-URL registry is introduced.

## Sealed container certificate (v3)

Creating a sealed workload requires `PROVIDER_KEY_SOURCE=kms`. The stable app signer
(`ProviderKey()`), not the rotating node signer, signs `SANDBOX_SEAL_ATTESTATION`. Each
container still receives its own random secp256k1 key in `SANDBOX_SEAL_KEY`. Node keys
remain node-registration and KMS provider-endorsement identities.

The provider obtains its registration instance from the chain when creating the
container and rejects creation if that instance or KMS service binding is unavailable
or invalid. Caller-supplied issuer fields cannot replace this scope.

The certificate is compact JSON in this field order:

```json
{"version":3,"chain_id":16602,"registry_address":"0x<TappRegistry>","app_id":"<provider app id>","registered_at":1792108800,"seal_id":"<64 lowercase hex, no 0x>","pubkey":"0x<33-byte compressed secp256k1>","image_hash":"sha256:<64 lowercase hex>","ts":1793318445,"signature":"0x<65-byte r||s||v>"}
```

The signature is EIP-712:

- Domain: `EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)`
  with name `0G Sandbox ImageAttestation`, version `3`, `chainId = chain_id` and
  `verifyingContract = registry_address`.
- Type: `ImageAttestation(string appId,uint64 registeredAt,bytes32 sealId,bytes containerPubkey,bytes32 imageHash,uint64 issuedAt)`
  with `appId = app_id` (exact UTF-8 bytes), `registeredAt = registered_at`, `sealId` = the
  32 `seal_id` bytes, `containerPubkey` = the 33 raw `pubkey` bytes, `imageHash` = the 32
  bytes after `sha256:` and `issuedAt = ts`.
- `ProviderKey()` signs `keccak256(0x1901 ‖ domainSeparator ‖ structHash)` raw, with no
  `personal_sign` prefix. V is 27/28; verifiers accept 27/28 and 0/1.

`internal/proxy/testdata/image-attestation-v3.json` is a known-answer vector shared
byte-identical with the attestor and the sealed runtime.

On a sealed create the provider first removes every caller env key that starts with
`SANDBOX_`, plus `ATTESTOR_TLS_DESCRIPTOR`, `ATTESTOR_ALLOW_UNPINNED_DEV` and
`TEE_SIGNER_ADDRESS`. It then writes `SANDBOX_SEAL_KEY`, `SANDBOX_SEAL_ATTESTATION` and,
when its `PROXY_DOMAIN` is set, `SANDBOX_PROXY_DOMAIN`. Other caller env, such as
`ATTESTOR_URL`, `SEAL_BOOT_TICKET` and `SEAL_SECRET_ENV`, passes through unchanged.

The certificate attests only the image digest, so the container must run the image's
own entrypoint. A sealed create reads the entrypoint from the image's own config and
refuses a snapshot whose Daytona record names a different one, or that Daytona builds
from a Dockerfile (`buildInfo`). Snapshots themselves are not restricted: a non-sealed
sandbox may still use one that overrides its entrypoint.

A normal reboot changes the node signer but retains the app signer and registration
instance, so a stored certificate remains usable. Re-registering the same appId creates
a different instance even if KMS returns the same key; its epoch must not inherit an
earlier certificate.

No earlier certificate version is issued or accepted. Containers that hold one are
recreated at rollout; deploy the updated sandbox, attestor and sealed image together.

## Attestor TLS statement (`GET /api/attestor-tls`)

Sealed containers authenticate the attestor's TLS endpoint with SPKI pins signed by
this provider. The endpoint is public and needs no authentication:

- It is served only when `ATTESTOR_APP_ID` is set and `PROVIDER_KEY_SOURCE=kms`.
  Otherwise every request answers `503 {"error":"attestor tls unavailable"}`.
- Every response carries `Cache-Control: no-store`.
- Serving a cached statement takes no rate-limit token. Only requests that wait for a
  running refresh share one token bucket: 20 requests/s, burst 50. A request beyond it
  does not wait: it gets the cached statement while that is still servable, and
  otherwise `429` with `Retry-After: 1`.

The body is compact JSON in this field order, with no trailing newline:

```json
{"version":3,"chain_id":16602,"registry_address":"0x<TappRegistry>","tls_spki_sha256":["<64 lowercase hex, no 0x>"],"issued_at":1793318460,"expires_at":1793318748,"signature":"0x<65-byte r||s||v>"}
```

- `tls_spki_sha256` holds 1–16 entries, strictly ascending and unique: the discovered
  attestor app's pins, after the registration-instance and Redis checks above. Standby
  pins are excluded, as in discovery.
- `issued_at < expires_at <= issued_at + 300`. `expires_at` is the discovered lease end,
  capped at 300 seconds after `issued_at`.
- EIP-712 domain: name `0G Sandbox AttestorTLS`, version `3`, `chainId = chain_id`,
  `verifyingContract = registry_address`. Type:
  `AttestorTLS(bytes32[] tlsSpkiSha256,uint64 issuedAt,uint64 expiresAt)`; the array is
  encoded as keccak256 of its concatenated 32-byte elements. `ProviderKey()` signs the
  digest raw, as for the certificate, so the signer recovered from a container's
  certificate also verifies this statement.
- `internal/proxy/testdata/attestor-tls-v3.json` is the shared known-answer vector.

The provider signs one statement per identity resolution and serves the same bytes to
every caller:

- While `now < expires_at - 30` it serves the cached statement at once.
- From `expires_at - 60` a background refresh signs the next statement. Refreshes run
  one at a time, at most one attempt every 5 seconds, and re-resolve the attestor
  identity when less than 60 seconds of its lease remain.
- From `expires_at - 30`, or while no statement exists, a request waits for the running
  refresh, within the limit above.
- If refreshes fail it keeps serving the cached statement until `expires_at - 5`, then
  answers `503` with `Retry-After: 5`.

The statement carries no attestor app id, signer or URL. The attestor relays it
unchanged at its own `GET /attestor-tls?provider=<provider address>`; which attestor the
pins belong to remains this provider's `ATTESTOR_APP_ID` configuration.
