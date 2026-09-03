// Manual E2E against a live provider. Not part of `npm test`.
//
//   PROVIDER_URL=https://provider-private-sandbox.0g.ai \
//   USER_KEY=0x<funded-test-key> \
//   [SNAPSHOT=<name>] [SEALED=1] npm run e2e
//
// Flow: info → balance (deposits 0.05 OG if below min_balance) → acknowledge
// if needed → create → exec (skipped when sealed) → preview URL → stop → delete.
import { createSandboxSDK, privateKeySigner } from '../src/index.js';

const providerUrl = process.env.PROVIDER_URL;
const userKey = process.env.USER_KEY as `0x${string}` | undefined;
if (!providerUrl || !userKey) {
  console.error('PROVIDER_URL and USER_KEY are required');
  process.exit(1);
}

const sdk = createSandboxSDK({ providerUrl, signer: privateKeySigner(userKey) });

const info = await sdk.provider.info();
console.log('provider:', info.provider_address, '| sealed_only:', info.sealed_only, '| app:', info.app_id);

const bal = await sdk.chain.balance();
console.log('balance:', bal.balance.toString(), 'neuron');
if (bal.balance < BigInt(info.min_balance)) {
  console.log('depositing 0.05 OG…');
  const tx = await sdk.chain.deposit({ og: 0.05 });
  console.log('deposit tx:', tx.txHash);
}

if (!(await sdk.chain.isAcknowledged())) {
  const review = await sdk.chain.reviewProvider();
  console.log('acknowledging app', review.appId, 'ackVersion', review.ackVersion.toString());
  const tx = await sdk.chain.acknowledge();
  console.log('ack tx:', tx.txHash);
}

const sealed = process.env.SEALED === '1' || info.sealed_only;
const sb = await sdk.sandbox.create({
  name: `sdk-e2e-${Date.now()}`,
  snapshot: process.env.SNAPSHOT || 'ubuntu',
  ...(sealed ? { sealed: true, publicPorts: [8080] } : {}),
});
console.log('created:', sb.id, 'state:', sb.info.state);

try {
  if (!sealed) {
    const res = await sb.exec('echo sdk-e2e-ok && uname -a');
    console.log('exec exit', res.exitCode, '→', res.output.trim());
  } else {
    console.log('sealed sandbox — exec skipped; preview:', sb.previewUrl(8080));
  }
  const listed = await sdk.sandbox.list();
  console.log('list contains sandbox:', listed.some((s) => s.id === sb.id));
  await sb.stop();
  console.log('stopped ✓');
} finally {
  await sb.delete();
  console.log('deleted ✓');
}
