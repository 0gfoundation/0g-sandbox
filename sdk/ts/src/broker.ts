// Broker — a routing hub in front of many providers.
//
// The broker lets a user work against providers through ONE endpoint: it
// exposes discovery (GET /api/info chain config, GET /api/providers indexed
// list) and a reverse proxy (ANY /proxy/:providerAddr/* → the provider,
// same-origin so browsers avoid CORS). Session endpoints are
// billing-proxy-to-broker (TEE-signed), not for users.
//
// The Broker class resolves WHICH provider each operation targets, then reuses
// a normal per-provider SandboxSDK routed through /proxy/<addr>. Provider
// selection is the one seam that broker-side smart routing will later fill.

import { createSandboxSDK, type SandboxSDK } from './client.js';
import { mapHttpError, SandboxSDKError } from './errors.js';
import type { FetchLike } from './http.js';
import type { Amount, Balance, ProviderReview, TxReceipt } from './chain.js';
import type { CreateOptions, PreviewConfig, Sandbox, SandboxInfo } from './sandbox.js';
import type { Signer } from './signer.js';

export interface BrokerInfo {
  contract_address: string;
  tapp_registry: string;
  app_id?: string;
  chain_id: number;
  rpc_url: string;
  [key: string]: unknown;
}

export interface ProviderEntry {
  address: string;
  url: string;
  app_id: string;
  price_per_cpu_per_min: string;
  price_per_cpu_per_sec: string;
  price_per_mem_gb_per_min: string;
  price_per_mem_gb_per_sec: string;
  create_fee: string;
  last_indexed_block: number;
  updated_at: string;
}

/**
 * Reserved for future broker-side smart routing (price priority, load, …).
 * Passing a strategy today throws NOT_IMPLEMENTED — snapshot-aware selection
 * already happens implicitly when a create names a snapshot without a provider.
 */
export interface SelectStrategy {
  prefer?: 'price';
  [key: string]: unknown;
}

/** Which provider an operation targets. Both fields optional (see resolve rules). */
export interface Target {
  /** Explicit provider address. */
  provider?: string;
  /** Reserved — not yet honored. */
  strategy?: SelectStrategy;
}

export interface BrokerConfig {
  brokerUrl: string;
  signer: Signer;
  /** Fallback proxy domain for previewUrl() (per-provider; imperfect across providers). */
  preview?: PreviewConfig;
  fetch?: FetchLike;
  /** Discovery cache TTL in ms (providers + snapshots). Default 30s. */
  cacheTtlMs?: number;
}

const DEFAULT_TTL = 30_000;

export class Broker {
  readonly sandbox: BrokerSandboxApi;
  readonly chain: BrokerChainApi;

  private readonly brokerUrl: string;
  private readonly fetchFn: FetchLike;
  private readonly ttl: number;
  private readonly sdkCache = new Map<string, SandboxSDK>();
  private infoCache?: { at: number; value: BrokerInfo };
  private providersCache?: { at: number; value: ProviderEntry[] };
  private snapshotCache = new Map<string, { at: number; value: string[] }>();

  constructor(private readonly cfg: BrokerConfig) {
    this.brokerUrl = cfg.brokerUrl.replace(/\/+$/, '');
    this.fetchFn = cfg.fetch ?? ((...args) => globalThis.fetch(...args));
    this.ttl = cfg.cacheTtlMs ?? DEFAULT_TTL;
    this.sandbox = new BrokerSandboxApi(this);
    this.chain = new BrokerChainApi(this);
  }

  // ── Discovery ─────────────────────────────────────────────────────────────

  /** GET /api/info — chain config this broker indexes (contract, registry, chainId, RPC). */
  async info(): Promise<BrokerInfo> {
    if (this.infoCache && Date.now() - this.infoCache.at < this.ttl) return this.infoCache.value;
    const value = await this.getJson<BrokerInfo>('/api/info');
    this.infoCache = { at: Date.now(), value };
    return value;
  }

  /** GET /api/providers — providers indexed from on-chain ServiceUpdated events. */
  async providers(): Promise<ProviderEntry[]> {
    if (this.providersCache && Date.now() - this.providersCache.at < this.ttl) return this.providersCache.value;
    const value = await this.getJson<ProviderEntry[]>('/api/providers');
    this.providersCache = { at: Date.now(), value };
    return value;
  }

  // ── Provider resolution (the routing seam) ──────────────────────────────────

  /**
   * Resolve which provider an operation targets:
   *  - target.strategy given → NOT_IMPLEMENTED (reserved for broker-side routing)
   *  - target.provider given → that address
   *  - omitted + create names a snapshot → a provider that has it active
   *  - omitted otherwise → the first indexed provider
   */
  async resolveProvider(target?: Target, createOpts?: CreateOptions): Promise<string> {
    if (target?.strategy) {
      throw new SandboxSDKError(
        'NOT_IMPLEMENTED',
        'strategy-based routing is not available yet; pass an explicit provider address',
      );
    }
    if (target?.provider) return target.provider.toLowerCase();

    const providers = await this.providers();
    if (providers.length === 0) {
      throw new SandboxSDKError('NO_PROVIDER', 'broker has no registered providers');
    }

    const snapshot = createOpts?.snapshot;
    if (!snapshot) return providers[0].address.toLowerCase();

    // Snapshot-aware default: pick a provider that actually has it active.
    const { matches, failed } = await this.providersWithSnapshot(snapshot, providers);
    if (matches.length > 0) return matches[0].address.toLowerCase();
    // No match. Distinguish "genuinely nobody has it" from "we couldn't reach
    // some providers" — the latter is an operational problem the caller must see,
    // not a silent "no such snapshot".
    if (failed.length > 0) {
      throw new SandboxSDKError(
        'NO_PROVIDER',
        `no reachable provider has an active snapshot "${snapshot}"; ${failed.length} of ${providers.length} provider(s) could not be probed`,
        undefined,
        { failed: failed.map((f) => ({ provider: f.provider, error: f.error })) },
      );
    }
    throw new SandboxSDKError('NO_PROVIDER', `no provider has an active snapshot named "${snapshot}"`);
  }

  /** A per-provider SandboxSDK routed through the broker's reverse proxy. Cached. */
  async sdkFor(address: string): Promise<SandboxSDK> {
    const key = address.toLowerCase();
    const cached = this.sdkCache.get(key);
    if (cached) return cached;
    const info = await this.info();
    const sdk = createSandboxSDK({
      providerUrl: `${this.brokerUrl}/proxy/${key}`,
      signer: this.cfg.signer,
      chain: {
        rpcUrl: info.rpc_url,
        chainId: info.chain_id,
        settlementContract: info.contract_address as `0x${string}`,
        tappRegistry: info.tapp_registry as `0x${string}`,
      },
      preview: this.cfg.preview,
      providerAddress: key,
      fetch: this.fetchFn,
    });
    this.sdkCache.set(key, sdk);
    return sdk;
  }

  private async providersWithSnapshot(
    name: string,
    providers: ProviderEntry[],
  ): Promise<{ matches: ProviderEntry[]; failed: { provider: string; error: string }[] }> {
    const matches: ProviderEntry[] = [];
    const failed: { provider: string; error: string }[] = [];
    await Promise.all(
      providers.map(async (p) => {
        try {
          if ((await this.activeSnapshots(p.address)).includes(name)) matches.push(p);
        } catch (err) {
          // A probe failure means "couldn't tell", NOT "doesn't have it" —
          // record it so resolveProvider can report it instead of silently
          // dropping the provider.
          failed.push({ provider: p.address, error: (err as Error).message });
        }
      }),
    );
    return { matches, failed };
  }

  private async activeSnapshots(address: string): Promise<string[]> {
    const key = address.toLowerCase();
    const cached = this.snapshotCache.get(key);
    if (cached && Date.now() - cached.at < this.ttl) return cached.value;
    // Let errors propagate — the caller distinguishes unreachable from empty.
    // Only successful probes are cached, so a transient failure isn't pinned
    // for the whole TTL.
    const sdk = await this.sdkFor(address);
    const snaps = await sdk.provider.snapshots();
    const names = snaps
      .filter((s) => (s.state ? s.state === 'active' : true))
      .map((s) => s.name)
      .filter((n): n is string => typeof n === 'string');
    this.snapshotCache.set(key, { at: Date.now(), value: names });
    return names;
  }

  private async getJson<T>(path: string): Promise<T> {
    let res: Response;
    try {
      res = await this.fetchFn(this.brokerUrl + path, { method: 'GET' });
    } catch (err) {
      throw new SandboxSDKError('API_ERROR', `broker request failed: ${(err as Error).message}`, undefined, err);
    }
    const text = await res.text();
    if (!res.ok) throw mapHttpError(res.status, text);
    return JSON.parse(text) as T;
  }
}

// ── Operation facades ─────────────────────────────────────────────────────────

class BrokerSandboxApi {
  constructor(private readonly broker: Broker) {}

  /** Create a sandbox. The returned Sandbox is pinned to the resolved provider. */
  async create(opts?: CreateOptions, target?: Target): Promise<Sandbox> {
    const addr = await this.broker.resolveProvider(target, opts);
    return (await this.broker.sdkFor(addr)).sandbox.create(opts);
  }

  async list(target?: Target): Promise<SandboxInfo[]> {
    const addr = await this.broker.resolveProvider(target);
    return (await this.broker.sdkFor(addr)).sandbox.list();
  }

  /**
   * Get a sandbox. Requires target.provider — a bare id is meaningless without
   * knowing which provider it lives on (use the `provider` on a Sandbox returned
   * by create()). Defaulting to "first provider" would just 404 confusingly.
   */
  async get(id: string, target: Target): Promise<Sandbox> {
    if (!target?.provider) {
      throw new SandboxSDKError('NO_PROVIDER', 'broker.sandbox.get requires an explicit { provider } — a sandbox id lives on one provider');
    }
    const addr = await this.broker.resolveProvider(target);
    return (await this.broker.sdkFor(addr)).sandbox.get(id);
  }
}

class BrokerChainApi {
  constructor(private readonly broker: Broker) {}

  // Reads may fall back to the default provider (first / snapshot-aware).
  private async api(target?: Target) {
    return (await this.broker.sdkFor(await this.broker.resolveProvider(target))).chain;
  }

  // Writes require an explicit provider: defaulting to "first indexed provider"
  // would silently move funds/acks to a provider the caller never chose, and
  // list order is not stable. Reads and create can default safely; money can't.
  private async writeApi(op: string, target?: Target) {
    if (!target?.provider) {
      throw new SandboxSDKError(
        'NO_PROVIDER',
        `broker.chain.${op} requires an explicit { provider } — refusing to default to an arbitrary provider for an on-chain write`,
      );
    }
    return this.api(target);
  }

  async providerAddress(target?: Target) {
    return (await this.api(target)).providerAddress();
  }
  async balance(target?: Target): Promise<Balance> {
    return (await this.api(target)).balance();
  }
  async isAcknowledged(target?: Target): Promise<boolean> {
    return (await this.api(target)).isAcknowledged();
  }
  async reviewProvider(target?: Target): Promise<ProviderReview> {
    return (await this.api(target)).reviewProvider();
  }
  async deposit(amount: Amount, target?: Target): Promise<TxReceipt> {
    return (await this.writeApi('deposit', target)).deposit(amount);
  }
  async requestRefund(amount: Amount, target?: Target): Promise<TxReceipt> {
    return (await this.writeApi('requestRefund', target)).requestRefund(amount);
  }
  async withdrawRefund(target?: Target): Promise<TxReceipt> {
    return (await this.writeApi('withdrawRefund', target)).withdrawRefund();
  }
  async acknowledge(target?: Target): Promise<TxReceipt> {
    return (await this.writeApi('acknowledge', target)).acknowledge();
  }
  async revokeAcknowledgement(target?: Target): Promise<TxReceipt> {
    return (await this.writeApi('revokeAcknowledgement', target)).revokeAcknowledgement();
  }
}

/** One-shot provider discovery via a broker (no signer needed). */
export async function discoverProviders(brokerUrl: string, fetchFn?: FetchLike): Promise<ProviderEntry[]> {
  const url = brokerUrl.replace(/\/+$/, '') + '/api/providers';
  const fn = fetchFn ?? ((...args: Parameters<FetchLike>) => globalThis.fetch(...args));
  const res = await fn(url, { method: 'GET' });
  const text = await res.text();
  if (!res.ok) throw mapHttpError(res.status, text);
  return JSON.parse(text) as ProviderEntry[];
}
