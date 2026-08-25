// Regenerates test/vectors.json — the cross-language golden signing vectors.
// Run: npm run vectors
// The Go side (internal/auth/sdk_vectors_test.go) verifies every vector with
// the server's actual Recover implementation.
import { writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { buildAuthHeaders, privateKeySigner } from '../src/signer.js';

// Well-known throwaway key (hardhat account #0) — never fund it.
const KEY = '0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80' as const;
const NONCE = '000102030405060708090a0b0c0d0e0f';
const NOW = 1893456000 - 180; // expires_at pins to 1893456000 (2030-01-01T00:00:00Z)

const cases = [
  { name: 'create-with-payload', action: 'create', resourceId: '', payload: { snapshot: '0g-ubuntu22', cpu: 2, sealed: true, publicPorts: [8080] } },
  { name: 'list-empty-payload', action: 'list', resourceId: '', payload: {} },
  { name: 'toolbox-with-resource', action: 'toolbox', resourceId: 'a1b2c3d4-0000-1111-2222-333344445555', payload: {} },
  { name: 'unicode-payload', action: 'create', resourceId: '', payload: { name: '沙盒-α', env: { GREETING: 'héllo & <world>' } } },
];

const signer = privateKeySigner(KEY);
const vectors = [] as unknown[];
for (const c of cases) {
  const headers = await buildAuthHeaders(signer, c.action, c.resourceId, c.payload, { nonce: NONCE, nowSec: NOW });
  vectors.push({
    name: c.name,
    privateKey: KEY,
    address: signer.address,
    action: c.action,
    resourceId: c.resourceId,
    payload: c.payload,
    nonce: NONCE,
    expiresAt: NOW + 180,
    message: Buffer.from(headers['X-Signed-Message'], 'base64').toString('utf8'),
    messageB64: headers['X-Signed-Message'],
    signature: headers['X-Wallet-Signature'],
  });
}

const out = join(dirname(fileURLToPath(import.meta.url)), 'vectors.json');
writeFileSync(out, JSON.stringify(vectors, null, 2) + '\n');
console.log(`wrote ${vectors.length} vectors to ${out}`);
