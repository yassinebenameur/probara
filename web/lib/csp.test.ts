import assert from 'node:assert/strict';
import test from 'node:test';
import { CSP_HEADER, buildWebCsp, newNonce, originOf, statusPageFrameOrigins } from './csp';

test('buildWebCsp uses the nonce with strict-dynamic and forbids framing', () => {
  const policy = buildWebCsp({ nonce: 'abc', dev: false, statusPageOrigins: ['https://status.example.com'] });
  assert.match(policy, /script-src 'self' 'nonce-abc' 'strict-dynamic'(;|$)/);
  assert.match(policy, /frame-ancestors 'none'/);
  // Status pages on any https: host can be previewed.
  assert.match(policy, /frame-src 'self' https:(;|$)/);
  assert.match(policy, /connect-src 'self'(;|$)/);
  assert.match(policy, /object-src 'none'/);
  assert.doesNotMatch(policy, /unsafe-eval/);
  // A nonce in style-src would disable 'unsafe-inline' for style attributes.
  assert.match(policy, /style-src 'self' 'unsafe-inline'(;|$)/);
});

test('buildWebCsp allows eval only in development, plain-http preview hosts and a direct API origin', () => {
  const policy = buildWebCsp({
    nonce: 'n',
    dev: true,
    statusPageOrigins: ['http://localhost:8082'],
    apiOrigin: 'https://api.example.com',
  });
  assert.match(policy, /frame-src 'self' https: http:\/\/localhost:8082/);
  assert.match(policy, /'strict-dynamic' 'unsafe-eval'/);
  assert.match(policy, /connect-src 'self' https:\/\/api\.example\.com/);
});

test('the policy ships report-only', () => {
  assert.equal(CSP_HEADER, 'Content-Security-Policy-Report-Only');
});

test('statusPageFrameOrigins mirrors the status page URL resolution', () => {
  assert.deepEqual(statusPageFrameOrigins('https://status.example.com/base/', 'ops.example.com', 'https:'), [
    'https://status.example.com',
  ]);
  assert.deepEqual(statusPageFrameOrigins(undefined, 'localhost', 'http:'), [
    'http://localhost:8082',
    'http://127.0.0.1:8082',
  ]);
  assert.deepEqual(statusPageFrameOrigins('', 'ops.example.com', 'https:'), []);
  assert.deepEqual(statusPageFrameOrigins('/relative', 'ops.example.com', 'https:'), []);
});

test('originOf accepts only absolute http(s) URLs', () => {
  assert.equal(originOf('https://api.example.com/v1'), 'https://api.example.com');
  assert.equal(originOf('/api'), null);
  assert.equal(originOf('javascript:alert(1)'), null);
  assert.equal(originOf(undefined), null);
});

test('newNonce is fresh base64', () => {
  const a = newNonce();
  const b = newNonce();
  assert.notEqual(a, b);
  assert.match(a, /^[A-Za-z0-9+/]{22}==$/);
});
