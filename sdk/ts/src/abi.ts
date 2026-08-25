// Minimal ABI subsets — must stay in sync with contracts/abi/*.json.

export const sandboxServingAbi = [
  {
    type: 'function',
    name: 'deposit',
    stateMutability: 'payable',
    inputs: [
      { name: 'recipient', type: 'address' },
      { name: 'provider', type: 'address' },
    ],
    outputs: [],
  },
  {
    type: 'function',
    name: 'getBalance',
    stateMutability: 'view',
    inputs: [
      { name: 'user', type: 'address' },
      { name: 'provider', type: 'address' },
    ],
    outputs: [
      { name: 'balance', type: 'uint256' },
      { name: 'pendingRefund', type: 'uint256' },
      { name: 'refundUnlockAt', type: 'uint256' },
    ],
  },
  {
    type: 'function',
    name: 'requestRefund',
    stateMutability: 'nonpayable',
    inputs: [
      { name: 'provider', type: 'address' },
      { name: 'amount', type: 'uint256' },
    ],
    outputs: [],
  },
  {
    type: 'function',
    name: 'withdrawRefund',
    stateMutability: 'nonpayable',
    inputs: [{ name: 'provider', type: 'address' }],
    outputs: [],
  },
  {
    type: 'function',
    name: 'services',
    stateMutability: 'view',
    inputs: [{ name: 'provider', type: 'address' }],
    outputs: [
      { name: 'url', type: 'string' },
      { name: 'appId', type: 'string' },
      { name: 'pricePerCPUPerMin', type: 'uint256' },
      { name: 'pricePerMemGBPerMin', type: 'uint256' },
      { name: 'createFee', type: 'uint256' },
    ],
  },
  {
    type: 'function',
    name: 'getLastNonce',
    stateMutability: 'view',
    inputs: [
      { name: 'user', type: 'address' },
      { name: 'provider', type: 'address' },
    ],
    outputs: [{ name: '', type: 'uint256' }],
  },
  {
    type: 'function',
    name: 'isTEEAcknowledged',
    stateMutability: 'view',
    inputs: [
      { name: 'user', type: 'address' },
      { name: 'provider', type: 'address' },
    ],
    outputs: [{ name: '', type: 'bool' }],
  },
] as const;

export const tappRegistryAbi = [
  {
    type: 'function',
    name: 'acknowledgeApp',
    stateMutability: 'nonpayable',
    inputs: [{ name: 'appId', type: 'string' }],
    outputs: [],
  },
  {
    type: 'function',
    name: 'revokeAcknowledgement',
    stateMutability: 'nonpayable',
    inputs: [{ name: 'appId', type: 'string' }],
    outputs: [],
  },
  {
    type: 'function',
    name: 'isAcknowledged',
    stateMutability: 'view',
    inputs: [
      { name: 'user', type: 'address' },
      { name: 'appId', type: 'string' },
    ],
    outputs: [{ name: '', type: 'bool' }],
  },
  {
    type: 'function',
    name: 'getAppInfo',
    stateMutability: 'view',
    inputs: [{ name: 'appId', type: 'string' }],
    outputs: [
      {
        name: '',
        type: 'tuple',
        components: [
          { name: 'composeHash', type: 'bytes' },
          { name: 'volumesHash', type: 'bytes' },
          { name: 'imageHashes', type: 'bytes[]' },
          { name: 'owner', type: 'address' },
          { name: 'registeredAt', type: 'uint256' },
        ],
      },
    ],
  },
  {
    type: 'function',
    name: 'getNodeList',
    stateMutability: 'view',
    inputs: [{ name: 'appId', type: 'string' }],
    outputs: [{ name: '', type: 'address[]' }],
  },
  {
    type: 'function',
    name: 'getNode',
    stateMutability: 'view',
    inputs: [
      { name: 'appId', type: 'string' },
      { name: 'signer', type: 'address' },
    ],
    outputs: [
      {
        name: '',
        type: 'tuple',
        components: [
          { name: 'teeUrl', type: 'string' },
          { name: 'addedAt', type: 'uint256' },
          { name: 'stakeAmount', type: 'uint256' },
        ],
      },
    ],
  },
  {
    type: 'function',
    name: 'getAckVersion',
    stateMutability: 'view',
    inputs: [{ name: 'appId', type: 'string' }],
    outputs: [{ name: '', type: 'uint256' }],
  },
] as const;
