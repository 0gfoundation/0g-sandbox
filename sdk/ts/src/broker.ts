// Broker interaction — provider discovery + CORS-free routing.
//
// The broker's user-facing surface is small: GET /api/info (chain config),
// GET /api/providers (on-chain-indexed provider list, stale nodes dropped),
// and ANY /proxy/:providerAddr/* (server-side reverse proxy to the provider,
// so browsers avoid CORS). Session endpoints are billing-proxy-to-broker
// (TEE-signed), not for users.

import { mapHttpError, SandboxSDKError } from './errors.js';
import type { FetchLike } from './http.js';

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

export class BrokerApi {
  constructor(
    private readonly brokerUrl: string,
    private readonly fetchFn: FetchLike = (...args) => globalThis.fetch(...args),
  ) {}

  /** GET /api/info — the chain config this broker indexes (contract, registry, chainId, RPC). */
  async info(): Promise<BrokerInfo> {
    return this.get<BrokerInfo>('/api/info');
  }

  /** GET /api/providers — registered providers, indexed from on-chain ServiceUpdated events. */
  async providers(): Promise<ProviderEntry[]> {
    return this.get<ProviderEntry[]>('/api/providers');
  }

  /**
   * providerUrl for createSandboxSDK that routes every call through the
   * broker's reverse proxy (`/proxy/<addr>`) instead of hitting the provider
   * directly — same-origin from the broker page, no CORS.
   */
  proxyUrl(providerAddress: string): string {
    return `${this.brokerUrl.replace(/\/+$/, '')}/proxy/${providerAddress.toLowerCase()}`;
  }

  private async get<T>(path: string): Promise<T> {
    let res: Response;
    try {
      res = await this.fetchFn(this.brokerUrl.replace(/\/+$/, '') + path, { method: 'GET' });
    } catch (err) {
      throw new SandboxSDKError('API_ERROR', `broker request failed: ${(err as Error).message}`, undefined, err);
    }
    const text = await res.text();
    if (!res.ok) throw mapHttpError(res.status, text);
    return JSON.parse(text) as T;
  }
}

/** One-shot provider discovery via a broker. */
export async function discoverProviders(brokerUrl: string, fetchFn?: FetchLike): Promise<ProviderEntry[]> {
  return new BrokerApi(brokerUrl, fetchFn).providers();
}
