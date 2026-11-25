import { sha256, stringToBytes } from 'viem';
import { describe, expect, it } from 'vitest';
import { HttpClient } from '../src/http.js';
import { privateKeySigner } from '../src/signer.js';

const KEY = '0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80' as const;
const PROVIDER = '0x47a8C0Ca5cCEC440b17fd859Dee6a72438aCc31e';

// Records every fetch; answers /api/info with the provider address.
function recordingFetch() {
  const calls: { url: string; init?: RequestInit }[] = [];
  const fetchFn = async (url: string, init?: RequestInit) => {
    calls.push({ url, init });
    const body = url.endsWith('/api/info') ? JSON.stringify({ provider_address: PROVIDER }) : '{}';
    return new Response(body, { status: 200 });
  };
  return { calls, fetchFn };
}

function signedMessage(init?: RequestInit): Record<string, unknown> {
  const headers = init?.headers as Record<string, string>;
  return JSON.parse(Buffer.from(headers['X-Signed-Message'], 'base64').toString('utf8'));
}

describe('HttpClient.signed request binding', () => {
  it('signs METHOD:path?query and the hash of the exact body sent', async () => {
    const { calls, fetchFn } = recordingFetch();
    const client = new HttpClient('https://broker.example/proxy/0xabc/', privateKeySigner(KEY), fetchFn);
    await client.signed('post', '/api/toolbox/sb-1/toolbox/files', {
      resourceId: 'sb-1',
      body: { name: '沙盒-α' },
      query: { path: '/home dir' },
    });

    const call = calls.at(-1)!;
    const msg = signedMessage(call.init);
    // The URL carries the path the provider sees after the relay strips /proxy/<addr>.
    expect(call.url).toBe('https://broker.example/proxy/0xabc/api/toolbox/sb-1/toolbox/files?path=%2Fhome+dir');
    expect(call.init?.method).toBe('POST');
    expect(msg.action).toBe('POST:/api/toolbox/sb-1/toolbox/files?path=%2Fhome+dir');
    expect(msg.body_sha256).toBe(sha256(stringToBytes(call.init?.body as string)));
    expect(msg.provider).toBe(PROVIDER);
    expect(msg.resource_id).toBe('sb-1');
  });

  it('hashes zero bytes when there is no body', async () => {
    const { calls, fetchFn } = recordingFetch();
    const client = new HttpClient('https://p.example', privateKeySigner(KEY), fetchFn);
    await client.signed('GET', '/api/sandbox');

    const call = calls.at(-1)!;
    const msg = signedMessage(call.init);
    expect(call.init?.body).toBeUndefined();
    expect(msg.action).toBe('GET:/api/sandbox');
    expect(msg.body_sha256).toBe(sha256(new Uint8Array()));
  });
});
