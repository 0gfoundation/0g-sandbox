import {
  createPublicClient,
  createWalletClient,
  custom,
  defineChain,
  http,
  parseEther,
  type Address,
  type Chain,
  type Hex,
  type PublicClient,
} from 'viem';
import { sandboxServingAbi, tappRegistryAbi } from './abi.js';
import { SandboxSDKError } from './errors.js';
import type { Signer } from './signer.js';

export interface ChainConfig {
  rpcUrl: string;
  chainId: number;
  /** SandboxServing beacon proxy. Defaults to `contract_address` from GET /api/info. */
  settlementContract?: Address;
  /** TappRegistry proxy. Defaults to the 0G Galileo testnet deployment. */
  tappRegistry?: Address;
}

export const GALILEO_TESTNET = {
  rpcUrl: 'https://evmrpc-testnet.0g.ai',
  chainId: 16602,
  tappRegistry: '0x2Ce80374318B1d7Fb3345724457a182E0ad165c9' as Address,
};

export interface Balance {
  balance: bigint;
  pendingRefund: bigint;
  refundUnlockAt: bigint;
}

export interface TxReceipt {
  txHash: Hex;
  blockNumber: bigint;
}

/** Neuron amount (bigint) or 0G convenience form, e.g. `{ og: 0.5 }`. */
export type Amount = bigint | { og: number | string };

export interface ProviderReview {
  provider: Address;
  url: string;
  appId: string;
  createFee: bigint;
  pricePerCPUPerMin: bigint;
  pricePerMemGBPerMin: bigint;
  appOwner: Address;
  registeredAt: bigint;
  ackVersion: bigint;
  composeHash: Hex;
  volumesHash: Hex;
  imageHashes: string[];
  nodes: { signer: Address; teeUrl: string }[];
}

export class ChainApi {
  private readonly chain: Chain;
  private readonly publicClient: PublicClient;
  private settlement?: Address;
  private registry?: Address;
  private cachedProvider?: Address;

  constructor(
    private readonly signer: Signer,
    private readonly cfg: ChainConfig,
    /** Resolves { provider_address, contract_address } from GET /api/info. */
    private readonly resolveInfo: () => Promise<{ provider_address: string; contract_address: string }>,
  ) {
    this.chain = defineChain({
      id: cfg.chainId,
      name: `0G (chain ${cfg.chainId})`,
      nativeCurrency: { name: '0G', symbol: '0G', decimals: 18 },
      rpcUrls: { default: { http: [cfg.rpcUrl] } },
    });
    this.publicClient = createPublicClient({ chain: this.chain, transport: http(cfg.rpcUrl) });
    this.settlement = cfg.settlementContract;
    this.registry = cfg.tappRegistry;
  }

  /** The provider's ledger address (= its TEE signer), from GET /api/info; cached. */
  async providerAddress(): Promise<Address> {
    if (!this.cachedProvider) {
      const info = await this.resolveInfo();
      this.cachedProvider = info.provider_address as Address;
      if (!this.settlement) this.settlement = info.contract_address as Address;
    }
    return this.cachedProvider;
  }

  async balance(provider?: Address): Promise<Balance> {
    const [balance, pendingRefund, refundUnlockAt] = await this.publicClient.readContract({
      address: await this.settlementAddress(),
      abi: sandboxServingAbi,
      functionName: 'getBalance',
      args: [this.signer.address, provider ?? (await this.providerAddress())],
    });
    return { balance, pendingRefund, refundUnlockAt };
  }

  async deposit(amount: Amount, provider?: Address): Promise<TxReceipt> {
    return this.write('deposit', [this.signer.address, provider ?? (await this.providerAddress())], toNeuron(amount));
  }

  async requestRefund(amount: Amount, provider?: Address): Promise<TxReceipt> {
    return this.write('requestRefund', [provider ?? (await this.providerAddress()), toNeuron(amount)]);
  }

  async withdrawRefund(provider?: Address): Promise<TxReceipt> {
    return this.write('withdrawRefund', [provider ?? (await this.providerAddress())]);
  }

  async isAcknowledged(provider?: Address): Promise<boolean> {
    return this.publicClient.readContract({
      address: await this.settlementAddress(),
      abi: sandboxServingAbi,
      functionName: 'isTEEAcknowledged',
      args: [this.signer.address, provider ?? (await this.providerAddress())],
    });
  }

  /**
   * Read-only review of a provider before acknowledging: commercial terms
   * (SandboxServing.services) + trust root (TappRegistry). Throws
   * TRUST_MISMATCH when the provider is not an active node of its appId —
   * an ack against it would be meaningless (same guard as cmd/user).
   */
  async reviewProvider(provider?: Address): Promise<ProviderReview> {
    const prov = provider ?? (await this.providerAddress());
    const [url, appId, pricePerCPUPerMin, pricePerMemGBPerMin, createFee] = await this.publicClient.readContract({
      address: await this.settlementAddress(),
      abi: sandboxServingAbi,
      functionName: 'services',
      args: [prov],
    });
    if (!appId) {
      throw new SandboxSDKError('TRUST_MISMATCH', `provider ${prov} has not registered a service (no appId)`);
    }
    const registry = this.registryAddress();
    const appInfo = await this.publicClient.readContract({
      address: registry,
      abi: tappRegistryAbi,
      functionName: 'getAppInfo',
      args: [appId],
    });
    if (appInfo.owner === '0x0000000000000000000000000000000000000000') {
      throw new SandboxSDKError('TRUST_MISMATCH', `app ${appId} is not registered in TappRegistry`);
    }
    const provNode = await this.publicClient.readContract({
      address: registry,
      abi: tappRegistryAbi,
      functionName: 'getNode',
      args: [appId, prov],
    });
    if (provNode.addedAt === 0n) {
      throw new SandboxSDKError(
        'TRUST_MISMATCH',
        `provider ${prov} is not an active TappRegistry node of app ${appId} — refusing to acknowledge`,
      );
    }
    const [nodeAddrs, ackVersion] = await Promise.all([
      this.publicClient.readContract({ address: registry, abi: tappRegistryAbi, functionName: 'getNodeList', args: [appId] }),
      this.publicClient.readContract({ address: registry, abi: tappRegistryAbi, functionName: 'getAckVersion', args: [appId] }),
    ]);
    const nodes = await Promise.all(
      nodeAddrs.map(async (signer) => {
        const n = await this.publicClient.readContract({
          address: registry,
          abi: tappRegistryAbi,
          functionName: 'getNode',
          args: [appId, signer],
        });
        return { signer, teeUrl: n.teeUrl };
      }),
    );
    return {
      provider: prov,
      url,
      appId,
      createFee,
      pricePerCPUPerMin,
      pricePerMemGBPerMin,
      appOwner: appInfo.owner,
      registeredAt: appInfo.registeredAt,
      ackVersion,
      composeHash: appInfo.composeHash,
      volumesHash: appInfo.volumesHash,
      imageHashes: appInfo.imageHashes.map(hexToUtf8),
      nodes,
    };
  }

  /** Runs reviewProvider trust checks, then writes the ack to TappRegistry. */
  async acknowledge(provider?: Address): Promise<TxReceipt> {
    const review = await this.reviewProvider(provider);
    return this.writeTo(this.registryAddress(), tappRegistryAbi, 'acknowledgeApp', [review.appId]);
  }

  async revokeAcknowledgement(provider?: Address): Promise<TxReceipt> {
    const review = await this.reviewProvider(provider);
    return this.writeTo(this.registryAddress(), tappRegistryAbi, 'revokeAcknowledgement', [review.appId]);
  }

  // ── internals ───────────────────────────────────────────────────────────────

  private async settlementAddress(): Promise<Address> {
    if (!this.settlement) await this.providerAddress(); // resolves contract from /api/info
    if (!this.settlement) {
      throw new SandboxSDKError('CHAIN_ERROR', 'settlement contract address unavailable (set chain.settlementContract)');
    }
    return this.settlement;
  }

  private registryAddress(): Address {
    if (!this.registry) {
      throw new SandboxSDKError('CHAIN_ERROR', 'TappRegistry address unavailable (set chain.tappRegistry)');
    }
    return this.registry;
  }

  private async write(functionName: string, args: unknown[], value?: bigint): Promise<TxReceipt> {
    return this.writeTo(await this.settlementAddress(), sandboxServingAbi, functionName, args, value);
  }

  private async pollReceipt(hash: Hex, timeoutMs: number) {
    const deadline = Date.now() + timeoutMs;
    let lastErr: unknown;
    while (Date.now() < deadline) {
      try {
        return await this.publicClient.getTransactionReceipt({ hash });
      } catch (err) {
        lastErr = err; // "not found" OR transient RPC error — keep polling
      }
      await sleep(2_000);
    }
    throw new SandboxSDKError(
      'CHAIN_ERROR',
      `transaction ${hash} not confirmed within ${timeoutMs / 1000}s`,
      undefined,
      lastErr,
    );
  }

  private async writeTo(
    address: Address,
    abi: readonly unknown[],
    functionName: string,
    args: unknown[],
    value?: bigint,
  ): Promise<TxReceipt> {
    const cap = this.signer.txCapability;
    if (!cap) {
      throw new SandboxSDKError(
        'SIGNER_NO_TX',
        'this signer can only personal_sign; on-chain calls need privateKeySigner/fromViemAccount/fromEip1193',
      );
    }
    const walletClient =
      cap.kind === 'viem-account'
        ? createWalletClient({ account: cap.account, chain: this.chain, transport: http(this.cfg.rpcUrl) })
        : createWalletClient({ account: this.signer.address, chain: this.chain, transport: custom(cap.provider) });
    try {
      const txHash = await walletClient.writeContract({
        address,
        abi: abi as never,
        functionName: functionName as never,
        args: args as never,
        value,
      } as never);
      // Manual receipt polling: evmrpc-testnet.0g.ai is load-balanced and can
      // error (not just return null) on pending-tx receipt lookups, which
      // defeats viem's waitForTransactionReceipt retry accounting. Poll
      // ourselves, tolerating any lookup error until the deadline.
      const receipt = await this.pollReceipt(txHash, 180_000);
      if (receipt.status !== 'success') {
        throw new SandboxSDKError('CHAIN_ERROR', `transaction reverted: ${txHash}`, undefined, receipt);
      }
      return { txHash, blockNumber: receipt.blockNumber };
    } catch (err) {
      if (err instanceof SandboxSDKError) throw err;
      throw new SandboxSDKError('CHAIN_ERROR', (err as Error).message ?? String(err), undefined, err);
    }
  }
}

function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}

export function toNeuron(amount: Amount): bigint {
  if (typeof amount === 'bigint') return amount;
  return parseEther(String(amount.og));
}

function hexToUtf8(hex: Hex): string {
  const clean = hex.slice(2);
  const bytes = new Uint8Array(clean.length / 2);
  for (let i = 0; i < bytes.length; i++) bytes[i] = parseInt(clean.slice(i * 2, i * 2 + 2), 16);
  return new TextDecoder().decode(bytes);
}
