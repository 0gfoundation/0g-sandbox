import type { HttpClient } from './http.js';

export interface ProviderInfo {
  contract_address: string;
  provider_address: string;
  owner_address?: string;
  app_id?: string;
  chain_id: number;
  rpc_url?: string;
  compute_price_per_sec: string;
  create_fee: string;
  voucher_interval_sec: number;
  min_balance: string;
  sealed_only: boolean;
  [key: string]: unknown;
}

export interface SnapshotInfo {
  name?: string;
  cpu?: number;
  memory?: number;
  disk?: number;
  [key: string]: unknown;
}

export class ProviderInfoApi {
  private cached?: ProviderInfo;

  constructor(private readonly httpClient: HttpClient) {}

  /** GET /api/info — provider address, contract, pricing, sealed_only. Cached. */
  async info(force = false): Promise<ProviderInfo> {
    if (!this.cached || force) {
      this.cached = await this.httpClient.public<ProviderInfo>('GET', '/api/info');
    }
    return this.cached;
  }

  /** GET /api/snapshots — available base snapshots (public). */
  async snapshots(): Promise<SnapshotInfo[]> {
    const res = await this.httpClient.public<SnapshotInfo[] | { items?: SnapshotInfo[] }>('GET', '/api/snapshots');
    return Array.isArray(res) ? res : (res.items ?? []);
  }
}
