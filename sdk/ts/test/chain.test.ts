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
    expect(() => validateCreateOptions({ snapshot: 'x', sealed: true, publicPorts: [8080] })).not.toThrow();
    expect(() => validateCreateOptions({ snapshot: 'x' })).not.toThrow();
  });
  it('requires a snapshot (custom cpu/memory unsupported)', () => {
    expect(() => validateCreateOptions({} as never)).toThrowError(/snapshot is required/);
    expect(() => validateCreateOptions({ snapshot: '' })).toThrowError(/snapshot is required/);
  });
  it('rejects bad sealId', () => {
    expect(() => validateCreateOptions({ snapshot: 'x', sealId: 'xyz' })).toThrowError(/64 hex/);
  });
  it('does NOT enforce server policy (port count, system ports) — server is authority', () => {
    // These are valid *formats*; the server may or may not accept them, but the
    // SDK must not pre-reject on drift-prone policy.
    expect(() => validateCreateOptions({ snapshot: 'x', publicPorts: Array.from({ length: 20 }, (_, i) => 1000 + i) })).not.toThrow();
    expect(() => validateCreateOptions({ snapshot: 'x', publicPorts: [22222] })).not.toThrow();
  });
  it('rejects out-of-range ports', () => {
    expect(() => validateCreateOptions({ snapshot: 'x', publicPorts: [70000] })).toThrowError(/invalid port/);
  });
  it('requires 8080 for sealed with publicPorts', () => {
    expect(() => validateCreateOptions({ snapshot: 'x', sealed: true, publicPorts: [3000] })).toThrowError(/must expose port 8080/);
  });
});
