import { ChainApi, GALILEO_TESTNET, type ChainConfig } from './chain.js';
import { HttpClient, type FetchLike } from './http.js';
import { ProviderInfoApi } from './provider.js';
import { SandboxApi } from './sandbox.js';
import { buildAuthHeaders, type Signer } from './signer.js';

export interface SDKConfig {
  /** Billing proxy base URL, e.g. https://provider-private-sandbox.0g.ai */
  providerUrl: string;
  signer: Signer;
  /** Defaults: 0G Galileo testnet; settlementContract auto-resolved from GET /api/info. */
  chain?: Partial<ChainConfig>;
  /** Injectable fetch (tests / custom agents). */
  fetch?: FetchLike;
}

export interface SandboxSDK {
  provider: ProviderInfoApi;
  chain: ChainApi;
  sandbox: SandboxApi;
  /** Escape hatch: wallet-signed request to any billing-proxy route. */
  signedFetch: HttpClient['signed'];
}

export function createSandboxSDK(cfg: SDKConfig): SandboxSDK {
  const httpClient = new HttpClient(cfg.providerUrl, cfg.signer, cfg.fetch);
  const provider = new ProviderInfoApi(httpClient);
  const chainCfg: ChainConfig = {
    rpcUrl: cfg.chain?.rpcUrl ?? GALILEO_TESTNET.rpcUrl,
    chainId: cfg.chain?.chainId ?? GALILEO_TESTNET.chainId,
    settlementContract: cfg.chain?.settlementContract,
    tappRegistry: cfg.chain?.tappRegistry ?? GALILEO_TESTNET.tappRegistry,
  };
  const chain = new ChainApi(cfg.signer, chainCfg, async () => {
    const info = await provider.info();
    return { provider_address: info.provider_address, contract_address: info.contract_address };
  });
  return {
    provider,
    chain,
    sandbox: new SandboxApi(httpClient),
    signedFetch: httpClient.signed.bind(httpClient),
  };
}

export { buildAuthHeaders, privateKeySigner, fromViemAccount, fromEip1193 } from './signer.js';
export type { Signer, AuthHeaders, SignOptions, Eip1193Provider } from './signer.js';
export { SandboxSDKError, mapHttpError } from './errors.js';
export type { ErrorCode } from './errors.js';
export { ChainApi, GALILEO_TESTNET, toNeuron } from './chain.js';
export type { ChainConfig, Balance, TxReceipt, Amount, ProviderReview } from './chain.js';
export { SandboxApi, Sandbox } from './sandbox.js';
export type { CreateOptions, SandboxInfo, ExecResult, ExecOptions } from './sandbox.js';
export { ProviderInfoApi } from './provider.js';
export type { ProviderInfo, SnapshotInfo } from './provider.js';
export { HttpClient } from './http.js';
export type { FetchLike, SignedRequestOptions } from './http.js';
