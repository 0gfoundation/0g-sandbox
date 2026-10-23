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

`ATTESTOR_APP_ID` adds the discovered stable app signer as an application admin.
Human operators may still use `ADMIN_ADDRESSES`; the sandbox app owner retains
its existing administration rights. An attestor's rotating node signer is not
automatically an admin. Discovery errors, expired results, or a failed current
registry-instance check do not grant application-admin permissions.

A verified current node with only its node identity, or TLS but no `app_signer`, may be a standby. Discovery
validates its metadata but excludes its TLS pin and lease from service identity.
At least one current node must declare a valid stable app signer; all nonempty
signer declarations must agree. Invalid, expired or non-production observations
still fail discovery, including those from a standby.

The first successful lookup stores the authorized attestor registration epoch
and owner in Redis under `identity:attestor:<chain>:<registry-lowercase>:<appId>:instance`.
Both are checked against one live registry read on every application-admin check.
An app name removed and registered again needs deliberate operator
re-authorization, even if its KMS key is unchanged. To authorize that new
instance, verify the intended deployment, remove this one authorization record
from the provider's Redis, and let a fresh verified lookup establish the new
epoch. Keep Redis persistence enabled to preserve authorization across restarts.

The registry's epoch is a timestamp. A remove/re-register by the same owner in
the same block is indistinguishable until the registry supplies a unique
registration counter. A different owner is rejected even with the same timestamp.

The current sandbox does not initiate HTTP calls to attestor. Components that
do call it retain their configured business URL as a locator and authenticate
TLS using the discovered app pin. No new business-URL registry is introduced.
