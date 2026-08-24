import { describe, expect, it, vi } from 'vitest';
import { Broker } from '../src/broker.js';
import { SandboxSDKError } from '../src/errors.js';
import { privateKeySigner } from '../src/signer.js';

const KEY = '0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80' as const;

const PROVIDERS = [
  { address: '0xAAAA000000000000000000000000000000000001', url: 'https://a.example', app_id: 'x', price_per_cpu_per_min: '5', price_per_cpu_per_sec: '0', price_per_mem_gb_per_min: '0', price_per_mem_gb_per_sec: '0', create_fee: '0', last_indexed_block: 1, updated_at: '' },
  { address: '0xBBBB000000000000000000000000000000000002', url: 'https://b.example', app_id: 'x', price_per_cpu_per_min: '3', price_per_cpu_per_sec: '0', price_per_mem_gb_per_min: '0', price_per_mem_gb_per_sec: '0', create_fee: '0', last_indexed_block: 1, updated_at: '' },
];
const INFO = { contract_address: '0xC0', tapp_registry: '0x2Ce8', chain_id: 16602, rpc_url: 'https://rpc' };
// Which snapshots each provider (by address prefix) reports active.
const SNAPSHOTS: Record<string, { name: string; state: string }[]> = {
  aaaa: [{ name: 'ubuntu', state: 'active' }],
  bbbb: [{ name: 'ubuntu', state: 'active' }, { name: 'openclaw', state: 'active' }, { name: 'stale', state: 'inactive' }],
};

function mockFetch() {
  return vi.fn(async (url: string) => {
    const u = new URL(url);
    const json = (body: unknown) => new Response(JSON.stringify(body), { status: 200 });
    if (u.pathname === '/api/providers') return json(PROVIDERS);
    if (u.pathname === '/api/info') return json(INFO);
    const m = u.pathname.match(/^\/proxy\/0x([0-9a-f]{4})[0-9a-f]+\/api\/snapshots$/);
    if (m) return json(SNAPSHOTS[m[1]] ?? []);
    return new Response('not found', { status: 404 });
  });
}

function broker(fetchFn: ReturnType<typeof mockFetch>) {
  return new Broker({ brokerUrl: 'https://broker.test', signer: privateKeySigner(KEY), fetch: fetchFn as never });
}

describe('Broker.resolveProvider', () => {
  it('explicit provider address wins', async () => {
    const b = broker(mockFetch());
    expect(await b.resolveProvider({ provider: '0xDeAd000000000000000000000000000000000009' })).toBe(
      '0xdead000000000000000000000000000000000009',
    );
  });

  it('no target → first provider', async () => {
    const b = broker(mockFetch());
    expect(await b.resolveProvider()).toBe(PROVIDERS[0].address.toLowerCase());
  });

  it('snapshot on one provider → picks that provider', async () => {
    const b = broker(mockFetch());
    expect(await b.resolveProvider(undefined, { snapshot: 'openclaw' })).toBe(PROVIDERS[1].address.toLowerCase());
  });

  it('snapshot on all providers → first match', async () => {
    const b = broker(mockFetch());
    expect(await b.resolveProvider(undefined, { snapshot: 'ubuntu' })).toBe(PROVIDERS[0].address.toLowerCase());
  });

  it('inactive snapshot is ignored → NO_PROVIDER', async () => {
    const b = broker(mockFetch());
    await expect(b.resolveProvider(undefined, { snapshot: 'stale' })).rejects.toMatchObject({ code: 'NO_PROVIDER' });
  });

  it('unknown snapshot → NO_PROVIDER', async () => {
    const b = broker(mockFetch());
    await expect(b.resolveProvider(undefined, { snapshot: 'nope' })).rejects.toMatchObject({ code: 'NO_PROVIDER' });
  });

  it('strategy is reserved → NOT_IMPLEMENTED', async () => {
    const b = broker(mockFetch());
    await expect(b.resolveProvider({ strategy: { prefer: 'price' } })).rejects.toMatchObject({
      code: 'NOT_IMPLEMENTED',
    });
  });

  it('caches discovery within TTL', async () => {
    const fetchFn = mockFetch();
    const b = broker(fetchFn);
    await b.providers();
    await b.providers();
    const providerCalls = fetchFn.mock.calls.filter((c) => String(c[0]).endsWith('/api/providers'));
    expect(providerCalls.length).toBe(1);
  });
});

describe('Broker error surface', () => {
  it('empty provider list → NO_PROVIDER', async () => {
    const fetchFn = vi.fn(async (url: string) =>
      new URL(url).pathname === '/api/providers'
        ? new Response('[]', { status: 200 })
        : new Response(JSON.stringify(INFO), { status: 200 }),
    );
    const b = broker(fetchFn as never);
    await expect(b.resolveProvider()).rejects.toBeInstanceOf(SandboxSDKError);
  });
});
