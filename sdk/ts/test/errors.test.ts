import { describe, expect, it } from 'vitest';
import { mapHttpError } from '../src/errors.js';
import { Sandbox } from '../src/sandbox.js';

describe('mapHttpError', () => {
  const cases: [number, string, string][] = [
    [402, '{"error":"insufficient balance"}', 'INSUFFICIENT_BALANCE'],
    [403, '{"error":"TEE signer not acknowledged"}', 'NOT_ACKNOWLEDGED'],
    [400, '{"error":"this provider only accepts sealed sandboxes; set \\"sealed\\": true"}', 'SEALED_ONLY'],
    [403, '{"error":"sealed sandbox: SSH access not allowed"}', 'SEALED_FORBIDDEN'],
    [400, '{"error":"Total CPU limit exceeded. Maximum allowed: 12"}', 'QUOTA_EXCEEDED'],
    [502, '{"error":"publicPorts is not supported by this provider\'s Daytona backend; the sandbox was not started"}', 'PUBLIC_PORTS_UNSUPPORTED'],
    [401, '{"error":"missing auth headers"}', 'UNAUTHORIZED'],
    [403, '{"error":"forbidden"}', 'FORBIDDEN'],
    [404, 'not found', 'NOT_FOUND'],
    [500, '{"error":"upstream error"}', 'API_ERROR'],
    [502, 'plain text gateway error', 'API_ERROR'],
  ];
  for (const [status, body, code] of cases) {
    it(`${status} ${code}`, () => {
      const err = mapHttpError(status, body);
      expect(err.code).toBe(code);
      expect(err.httpStatus).toBe(status);
    });
  }

  it('keeps raw body in details', () => {
    const err = mapHttpError(500, '{"error":"upstream error","hint":"x"}');
    expect((err.details as { hint: string }).hint).toBe('x');
  });

  it('prefers a server-provided stable code over text matching', () => {
    // Message text says "insufficient balance" but code says QUOTA_EXCEEDED — code wins.
    const err = mapHttpError(400, '{"code":"QUOTA_EXCEEDED","error":"insufficient balance"}');
    expect(err.code).toBe('QUOTA_EXCEEDED');
  });

  it('ignores an unknown server code and falls back to text', () => {
    const err = mapHttpError(402, '{"code":"WEIRD_CODE","error":"insufficient balance"}');
    expect(err.code).toBe('INSUFFICIENT_BALANCE');
  });
});

describe('previewUrl derivation', () => {
  const make = (urls?: Record<string, string>) =>
    new Sandbox(undefined as never, { id: 'abc', preview_urls: urls });

  it('returns exact entry', () => {
    expect(make({ '8080': 'http://8080-abc.1.2.3.4.nip.io:4000' }).previewUrl(8080)).toBe(
      'http://8080-abc.1.2.3.4.nip.io:4000',
    );
  });

  it('derives sibling port from known entry', () => {
    expect(make({ '8080': 'https://8080-abc.sandbox.example.com' }).previewUrl(3000)).toBe(
      'https://3000-abc.sandbox.example.com',
    );
  });

  it('throws without any entries', () => {
    expect(() => make().previewUrl(8080)).toThrowError(/preview URL unavailable/);
  });
});
