import { describe, expect, it } from 'vitest';
import type { Address } from 'viem';
import { ChainApi } from '../src/chain.js';
import { privateKeySigner } from '../src/signer.js';

const PROVIDER = '0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC' as Address;
const SETTLEMENT = '0x5991A2dF15A8F6A256D3Ec51E99254Cd3fb576A9' as Address;
const REGISTRY = '0x2Ce80374318B1d7Fb3345724457a182E0ad165c9' as Address;
const APP = '0g-sandbox-provider';

interface Chain {
  kmsEpoch: bigint;
  /** undefined = the contract predates isActiveProvider (the call reverts) */
  isActiveProvider?: boolean;
  nodeAddedAt: bigint;
}

/** A ChainApi whose contract reads answer from `chain` instead of an RPC. */
function apiOn(chain: Chain): { api: ChainApi; calls: string[] } {
  const calls: string[] = [];
  const api = new ChainApi(
    privateKeySigner('0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d'),
    { rpcUrl: 'http://unused', chainId: 16602, settlementContract: SETTLEMENT, tappRegistry: REGISTRY },
    async () => ({ provider_address: PROVIDER, contract_address: SETTLEMENT }),
  );
  const readContract = async ({ functionName }: { functionName: string }) => {
    calls.push(functionName);
    switch (functionName) {
      case 'services':
        return ['https://sandbox.example', APP, 1n, 2n, 3n];
      case 'getAppInfo':
        return { composeHash: '0x', volumesHash: '0x', imageHashes: [], owner: '0x0000000000000000000000000000000000000001', registeredAt: 7n };
      case 'kmsBindings':
        return [chain.kmsEpoch, '0x00'];
      case 'isActiveProvider':
        if (chain.isActiveProvider === undefined) throw new Error('execution reverted');
        return chain.isActiveProvider;
      case 'getNode':
        return { teeUrl: 'https://node.example', addedAt: chain.nodeAddedAt, stakeAmount: 0n };
      case 'getNodeList':
        return [];
      case 'getAckVersion':
        return 1n;
      default:
        throw new Error(`unexpected read ${functionName}`);
    }
  };
  (api as unknown as { publicClient: { readContract: typeof readContract } }).publicClient = { readContract };
  return { api, calls };
}

describe('reviewProvider', () => {
  it('accepts a KMS provider that is no TappRegistry node', async () => {
    const { api } = apiOn({ kmsEpoch: 7n, isActiveProvider: true, nodeAddedAt: 0n });
    const review = await api.reviewProvider(PROVIDER);
    expect(review.kmsProvider).toBe(true);
    expect(review.appId).toBe(APP);
  });

  it('accepts an active node-signer provider', async () => {
    const { api } = apiOn({ kmsEpoch: 0n, isActiveProvider: true, nodeAddedAt: 1n });
    expect((await api.reviewProvider(PROVIDER)).kmsProvider).toBe(false);
  });

  it('refuses a provider whose key cannot settle', async () => {
    const { api } = apiOn({ kmsEpoch: 7n, isActiveProvider: false, nodeAddedAt: 0n });
    await expect(api.reviewProvider(PROVIDER)).rejects.toMatchObject({ code: 'TRUST_MISMATCH' });
  });

  it('falls back to node membership on a contract without isActiveProvider', async () => {
    const active = apiOn({ kmsEpoch: 0n, nodeAddedAt: 1n });
    await expect(active.api.reviewProvider(PROVIDER)).resolves.toMatchObject({ kmsProvider: false });
    expect(active.calls).toContain('getNode');

    const gone = apiOn({ kmsEpoch: 0n, nodeAddedAt: 0n });
    await expect(gone.api.reviewProvider(PROVIDER)).rejects.toMatchObject({ code: 'TRUST_MISMATCH' });
  });
});
