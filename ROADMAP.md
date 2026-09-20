# 0G Sandbox Roadmap — H2 2026

## Objective

Grow 0G Sandbox from a **Daytona-dependent vibe-coding sandbox** into a **self-owned confidential service-deployment platform** — and use the **sealed (attested) capability** to package and sell 0G Tapp's TEE compute to external customers through Sandbox.

In one line: **Sandbox expands from "an isolated place to write code" into "a place to run real services", becoming the commercial front door for Tapp's confidential compute.**

Three phases, built sequentially:

1. **Own the stack** (M1–M4) — upgrade to the latest open-source Daytona, fork and self-maintain it, strip unused components, and complete the multi-tenant contract capability.
2. **Expand the form factor** (M3–M6) — grow from a vibe sandbox into a service-deployment environment; combined with sealed, package Tapp's capabilities for external sale.
3. **Expand billing** (M4–M6) — beyond on-chain vouchers, add subscriptions, stablecoin payment, and full 0g-pay integration.

> **Sequencing principle:** decisions that are expensive to reverse (fork baseline version, contract structure) are locked first. Everything else is additive.

---

## Current Gaps (why we need this roadmap)

| Area | Risk | Impact |
|------|------|--------|
| **Daytona dependency** | Low version, poor stability, and upstream has gone closed-source | Cannot self-patch; long-term at the mercy of upstream |
| **Redundant components** | Components like dex are unused (0G authenticates via EIP-191, not OIDC) | Extra attack surface, image size, and maintenance burden |
| **Product positioning** | Currently positioned only as a vibe-coding sandbox | No form factor for service deployment or for selling Tapp's capabilities outward |
| **Contract** | One provider wallet can bind only one service | A single operator cannot run multiple nodes / price tiers |
| **Single billing model** | Only on-chain vouchers/deposit | High user barrier; no subscription or fiat path |
| **Claude environment** | Not yet integrated | Cannot directly host Claude/agent workloads |

---

## Milestones

| Milestone | Theme | Deliverables | Status |
|-----------|-------|-------------|--------|
| **M1** | Lock the baseline | Upgrade to latest open-source Daytona, freeze as the fork baseline · multi-service contract design finalized | Planned |
| **M2** | Self-own | Internal fork + own build/release pipeline · multi-service contract shipped | Planned |
| **M3** | Slim down | Strip dex and other unused components; solidify "our Daytona" | Planned |
| **M4** | Deployment form factor | Extend from ephemeral sandboxes to long-running service deployment (lifecycle / public endpoints) · Claude environment end-to-end | Planned |
| **M5** | Sell via sealed | Package Tapp's TEE compute as an external product via sealed attested deployment · subscription + stablecoin billing | Planned |
| **M6** | Commercial loop | Full 0g-pay integration (backend already wired, complete the frontend) · open confidential service deployment to external customers | Planned |

---

## Strategic Choices

**Own the stack before expanding.** Daytona is the platform's foundation, and upstream has gone closed-source. We first make "we can patch it and cut our own releases" solid (M1–M3); every later capability then rests on reliable ground.

**Lock what's expensive to reverse.** Which open-source version becomes the fork baseline, and how the contract is restructured (beacon upgrade + state migration), are decisions that are hard to walk back — so they go first. Form factor, billing methods, and the Claude environment are additive; they can come later and run in parallel.

**Sealed is the differentiator, and the commercial exit.** A plain sandbox is a crowded market; what's genuinely unique is the *provable confidential execution* that sealed provides. Making it the default form for service deployment turns Sandbox into the channel for selling Tapp's confidential compute — this is the commercial through-line of M4–M6.

**Multi-service independently unblocks operations.** One provider binding multiple services is decoupled from the Daytona workflow and can proceed in parallel with the upgrade. It directly removes the "one key, multiple nodes / price tiers" blocker and is a prerequisite for selling to multiple external customers.

---

## Scope

**Committed (6 months):** Daytona upgrade · fork self-maintenance · strip dex · multi-service contract · service-deployment form factor · sell-via-sealed · Claude sandbox environment · subscription/stablecoin billing · full 0g-pay integration (complete the frontend)

**Designed now, may extend beyond H2:** finer-grained agent/service billing models · component slim-down phase 2 (beyond dex) · self-serve deployment portal for external customers
