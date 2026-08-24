import { ChainApi, GALILEO_TESTNET, type ChainConfig } from './chain.js';
import { HttpClient, type FetchLike } from './http.js';
import { ProviderInfoApi } from './provider.js';
import { SandboxApi, type PreviewConfig } from './sandbox.js';
import type { Signer } from './signer.js';

export interface SDKConfig {
  /** Billing proxy base URL, e.g. https://provider-private-sandbox.0g.ai */
  providerUrl: string;
  signer: Signer;
  /** Defaults: 0G Galileo testnet; settlementContract auto-resolved from GET /api/info. */
  chain?: Partial<ChainConfig>;
  /** Fallback proxy domain for previewUrl() on creates without publicPorts. */
  preview?: PreviewConfig;
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

/** Create an SDK bound to a single provider (direct, or via a broker proxy URL). */
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
    sandbox: new SandboxApi(httpClient, cfg.preview),
    signedFetch: httpClient.signed.bind(httpClient),
  };
}
