import { describe, expect, it } from 'vitest';
import { toNeuron } from '../src/chain.js';
import { validateCreateOptions } from '../src/sandbox.js';

describe('toNeuron', () => {
  it('passes bigint through unchanged', () => {
    expect(toNeuron(123n)).toBe(123n);
  });
  it('converts { og } to neuron (1 0G = 1e18)', () => {
    expect(toNeuron({ og: 1 })).toBe(1_000_000_000_000_000_000n);
    expect(toNeuron({ og: 0.5 })).toBe(500_000_000_000_000_000n);
  });
  it('accepts string og to avoid float precision loss', () => {
    expect(toNeuron({ og: '0.000000000000000001' })).toBe(1n);
  });
});

describe('validateCreateOptions', () => {
  it('accepts valid options', () => {
    expect(() => validateCreateOptions({ snapshot: 'x', publicPorts: [8080, 3000] })).not.toThrow();
    expect(() => validateCreateOptions({ sealed: true, publicPorts: [8080] })).not.toThrow();
    expect(() => validateCreateOptions({})).not.toThrow();
  });
  it('rejects bad sealId', () => {
    expect(() => validateCreateOptions({ sealId: 'xyz' })).toThrowError(/64 hex/);
  });
  it('rejects too many public ports', () => {
    expect(() => validateCreateOptions({ publicPorts: Array.from({ length: 17 }, (_, i) => 1000 + i) })).toThrowError(
      /at most 16/,
    );
  });
  it('rejects system ports', () => {
    expect(() => validateCreateOptions({ publicPorts: [22222] })).toThrowError(/system port/);
  });
  it('rejects out-of-range ports', () => {
    expect(() => validateCreateOptions({ publicPorts: [70000] })).toThrowError(/invalid port/);
  });
  it('requires 8080 for sealed with publicPorts', () => {
    expect(() => validateCreateOptions({ sealed: true, publicPorts: [3000] })).toThrowError(/must expose port 8080/);
  });
});
