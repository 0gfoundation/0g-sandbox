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

## Sealed workload attestations (v2)

Creating a sealed workload requires `PROVIDER_KEY_SOURCE=kms`. The stable app signer,
not the rotating node signer, signs `SANDBOX_SEAL_ATTESTATION`. Each container still
receives its own random secp256k1 key. Node keys remain node-registration and KMS
provider-endorsement identities.

The version-2 envelope signs `chain_id`, `registry_address`, exact UTF-8 `app_id`,
`registered_at`, `seal_id`, compressed container public key, image hash and issue time.
The provider obtains its registration instance from the chain when creating the
container and rejects creation if that instance or KMS service binding is unavailable
or invalid. Caller-supplied issuer fields cannot replace this scope.

The raw signing digest is Keccak-256 of nine ABI-encoded 32-byte words:
`domainHash, chainId, registry, appIdHash, registeredAt, sealId, containerPubkeyHash,
imageHash, issuedAt`, where `domainHash = keccak256("0g.sandbox.ImageAttestation.v2")`.
The two string hashes use exact UTF-8 bytes and the public-key hash uses the raw
33-byte compressed key. Integers are unsigned big-endian ABI words; the registry
address is left-padded to one word. No `personal_sign` wrapper is added.

The matching attestor and sealed runtime must support v2. The attestor authenticates
the signer and issuer instance through Scan plus SandboxServing, preserving the
framework-image allowlist. A normal reboot changes the node signer but retains the
app signer and registration instance, so the stored v2 proof remains usable.
Re-registering the same appId creates a different instance even if KMS returns the
same key; its epoch must not inherit an earlier container proof.

There is no v1/node-signature fallback. Existing v1 containers require an explicit
owner-authorized reset to receive a new proof. Save runtime state first; reset retains
the agent identity and committed iData/settings, rather than retaining the original
container filesystem. Deploy the updated sandbox, attestor and sealed image together.
