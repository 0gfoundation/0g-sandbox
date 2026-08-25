import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { recoverMessageAddress } from 'viem';
import { describe, expect, it } from 'vitest';
import { buildAuthHeaders, privateKeySigner } from '../src/signer.js';

const vectors = JSON.parse(
  readFileSync(join(dirname(fileURLToPath(import.meta.url)), 'vectors.json'), 'utf8'),
) as {
  name: string;
  privateKey: `0x${string}`;
  address: string;
  action: string;
  resourceId: string;
  payload: unknown;
  nonce: string;
  expiresAt: number;
  message: string;
  messageB64: string;
  signature: `0x${string}`;
}[];

describe('golden signing vectors', () => {
  for (const v of vectors) {
    it(v.name, async () => {
      const signer = privateKeySigner(v.privateKey);
      expect(signer.address).toBe(v.address);

      const headers = await buildAuthHeaders(signer, v.action, v.resourceId, v.payload, {
        nonce: v.nonce,
        nowSec: v.expiresAt - 180,
      });

      // Byte-exact message reproduction (base64 of the same JSON string).
      expect(headers['X-Signed-Message']).toBe(v.messageB64);
      expect(Buffer.from(headers['X-Signed-Message'], 'base64').toString('utf8')).toBe(v.message);
      // RFC 6979 deterministic signature — stable across runs and libraries.
      expect(headers['X-Wallet-Signature']).toBe(v.signature);

      // Independent verification: recover the signer from message + signature.
      const recovered = await recoverMessageAddress({
        message: { raw: Buffer.from(headers['X-Signed-Message'], 'base64') },
        signature: headers['X-Wallet-Signature'],
      });
      expect(recovered.toLowerCase()).toBe(v.address.toLowerCase());
    });
  }

  it('message JSON has alphabetical keys and expected fields', async () => {
    const signer = privateKeySigner(vectors[0].privateKey);
    const headers = await buildAuthHeaders(signer, 'list', '', {}, { nonce: '00'.repeat(16), nowSec: 1000 });
    const msg = Buffer.from(headers['X-Signed-Message'], 'base64').toString('utf8');
    expect(msg).toBe(
      `{"action":"list","expires_at":1180,"nonce":"${'00'.repeat(16)}","payload":{},"resource_id":""}`,
    );
  });

  it('random nonce is 32 hex chars and V is 27/28', async () => {
    const signer = privateKeySigner(vectors[0].privateKey);
    const headers = await buildAuthHeaders(signer, 'list', '', {});
    const msg = JSON.parse(Buffer.from(headers['X-Signed-Message'], 'base64').toString('utf8'));
    expect(msg.nonce).toMatch(/^[0-9a-f]{32}$/);
    const sig = headers['X-Wallet-Signature'];
    expect(sig).toMatch(/^0x[0-9a-f]{130}$/);
    const v = parseInt(sig.slice(-2), 16);
    expect([27, 28]).toContain(v);
  });
});
