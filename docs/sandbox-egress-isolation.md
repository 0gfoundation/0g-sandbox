# Sandbox Egress Isolation

## Summary

User sandboxes run untrusted, tenant-supplied code. By default a container's
**outbound network** is unrestricted — Docker isolates filesystem, process, and
cgroup resources, but not egress. In the 0G single-host deployment the runner
and every control-plane service share one Docker network (`app-net`,
`172.25.0.0/24`), so a sandbox could reach far beyond the public internet.

This document records the exposure, why it exists, and the mitigation
(a `runner-firewall` sidecar that installs a per-sandbox egress denylist).

## What a sandbox could reach (confirmed on dev)

Probed from inside a running sandbox (`172.20.0.x`, the runner's inner bridge):

| Target | Result | Impact |
|---|---|---|
| GCP metadata `169.254.169.254` | reachable; **live service-account token pulled** (`ya29.…`, default compute SA) | Potential full GCP project takeover |
| Host VM tapp-daemon `:50051` | reachable | TEE key service — the root of the confidentiality model |
| Host VM SSH `:22` | reachable | Host access surface |
| Other project VMs (`:22`) | reachable over `10.128.0.0/9` | Lateral movement |
| Control plane `172.25.0.0/24` | minio `:9000`, redis `:6379` (no password), api `:3000` reachable | Cross-tenant data / billing-state tampering |
| Public internet | reachable | Expected — must stay reachable |

The realistic attacker is the **sandbox** — the only tenant-controlled,
untrusted component. Everything else on `app-net` (api, runner, minio, …) is
operator-owned.

## Root cause

1. **Egress is open by default.** Container network isolation is opt-in; nothing
   restricted sandbox outbound traffic.
2. **Collapsed topology.** Upstream Daytona expects the runner on a separate
   network tier from the control plane, so sandboxes physically cannot reach it.
   The 0G single-host compose co-locates everything on `app-net`, turning the
   whole internal network into reachable surface.
3. **No usable native knob.** Daytona's per-sandbox egress control
   (`networkAllowList`) is a positive allow-list capped at 10 CIDRs — it cannot
   express "allow all public, block the internal ranges" (that needs ~26 CIDRs
   as an allow-list). The block has to be an explicit firewall rule.

## Mitigation: `runner-firewall` sidecar

A sidecar container that shares the runner's network namespace
(`network_mode: "service:runner"`) and installs a denylist in the runner's inner
`DOCKER-USER` chain — the chain every sandbox FORWARD packet transits. It reuses
the runner image so the `iptables-nft` binary matches the runner's backend.

Resulting chain (order matters):

```
-A DOCKER-USER -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT
-A DOCKER-USER -d 192.168.0.0/16 -j DROP
-A DOCKER-USER -d 172.16.0.0/12  -j DROP
-A DOCKER-USER -d 10.0.0.0/8     -j DROP
-A DOCKER-USER -d 169.254.0.0/16 -j DROP
```

The blocked set (`BLOCKED_EGRESS_CIDRS`, default all four RFC1918 + link-local)
is a **denylist** — only four entries, versus the ~26 an allow-list would need.
It covers metadata (`169.254`), host + lateral (`10/8`), control plane + inner
sandbox bridge (`172.16/12`), and the remaining private range (`192.168/16`).

### Why the ESTABLISHED,RELATED ACCEPT is mandatory

Without it, the denylist also breaks **all** sandbox egress, including public.
A sandbox (`172.20.0.x`) opening a connection to a public host receives the
reply addressed back to its own private client address (`172.20.0.x`, inside
`172.16/12`). On the FORWARD path that reply matches `-d 172.16.0.0/12 -j DROP`,
so the SYN-ACK is dropped and the handshake never completes — the sandbox
appears to have no internet at all. The stateful ACCEPT, inserted **above** the
DROPs, lets replies to sandbox-initiated flows back in. NEW connections the
sandbox opens *to* a private destination are still dropped, so the block holds.

This return-path failure is the subtle bug: the block looks like an
"outbound to private" rule, but a naive denylist silently kills legitimate
outbound-to-public too.

### What stays reachable

- **Public internet** — sandbox-initiated flows and their replies.
- **Preview / exposed ports** — external → daytona-proxy → runner → sandbox. The
  final hop is runner-originated (or established return traffic), not a NEW
  forwarded packet to `172.20.0.x`, so `DOCKER-USER` DROP never matches it.
  Verified: a sandbox's `:8080` preview URL loads with the firewall active.
- **Runner's own control-plane connections** (registry/minio/api) — these leave
  via the runner's OUTPUT chain, not FORWARD, so the DROP does not apply.

## Deployment

The sidecar lives inline in the sandbox stack's `docker-compose.yml`
(`runner-firewall` service). It is kept in the base compose rather than a
separate named override because tapp deploys one compose plus the FDE-generated
`docker-compose.override.yml` and cannot load a second named override.

`BLOCKED_EGRESS_CIDRS` is set in the deployment env
(`169.254.0.0/16,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16`). The sidecar
re-asserts the rules every 10s so they self-heal if the runner's own
`DOCKER-USER` reconcile flushes the chain.

## Verification

1. From a sandbox, the metadata token endpoint returns `200` + a `ya29.` token
   **before** the sidecar; **blocked** after.
2. Control plane (`minio:9000`, `redis:6379`, `api:3000`), host, and other VMs:
   reachable before; blocked after.
3. Public egress (`1.1.1.1:443`, DNS, github/npm by name) stays reachable after.
4. Sandbox preview URL (`http://<port>-<id>.<PROXY_DOMAIN>/…`) loads after.

## Residual hardening (defense-in-depth, optional)

The firewall closes the sandbox-reachable path. The following weak configs are
now **unreachable from a sandbox** but remain as belt-and-suspenders items,
valuable only if the firewall is bypassed (e.g. a sandbox escape to the runner
host, where FORWARD rules do not apply):

- **Passwordless redis** — `sandbox-redis` runs `--requirepass "${REDIS_PASSWORD}"`
  with an empty value; setting `REDIS_PASSWORD` is a one-variable change (server
  and the billing client read the same var).
- **Unauthenticated internal registry** — `registry` has no `REGISTRY_AUTH`.
  Enabling it requires an htpasswd file **and** giving the runner's inner dockerd
  credentials to pull, or sandbox creation breaks; do it carefully or defer.
