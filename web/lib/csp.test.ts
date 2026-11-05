import assert from 'node:assert/strict';
import test from 'node:test';
import { buildWebCsp, cspHeaderName, hstsHeader, newNonce, originOf, statusPageFrameOrigins } from './csp';

test('buildWebCsp uses the nonce with strict-dynamic and forbids framing', () => {
  const policy = buildWebCsp({ nonce: 'abc', dev: false, statusPageOrigins: ['https://status.example.com'] });
  assert.match(policy, /script-src 'self' 'nonce-abc' 'strict-dynamic'(;|$)/);
  assert.match(policy, /frame-ancestors 'none'/);
  assert.match(policy, /frame-src 'self' https:\/\/status\.example\.com/);
  assert.match(policy, /connect-src 'self'(;|$)/);
  assert.doesNotMatch(policy, /unsafe-eval/);
  // A nonce in style-src would disable 'unsafe-inline' for style attributes.
  assert.match(policy, /style-src 'self' 'unsafe-inline'(;|$)/);
});

test('buildWebCsp allows eval only in development and a direct API origin', () => {
  const policy = buildWebCsp({ nonce: 'n', dev: true, statusPageOrigins: [], apiOrigin: 'https://api.example.com' });
  assert.match(policy, /'strict-dynamic' 'unsafe-eval'/);
  assert.match(policy, /connect-src 'self' https:\/\/api\.example\.com/);
});

test('cspHeaderName is report-only until enforced', () => {
  assert.equal(cspHeaderName(false), 'Content-Security-Policy-Report-Only');
  assert.equal(cspHeaderName(true), 'Content-Security-Policy');
});

test('statusPageFrameOrigins mirrors the status page URL resolution', () => {
  assert.deepEqual(statusPageFrameOrigins('https://status.example.com/base/', 'ops.example.com', 'https:'), ['https://status.example.com']);
  assert.deepEqual(statusPageFrameOrigins(undefined, 'localhost', 'http:'), ['http://localhost:8082', 'http://127.0.0.1:8082']);
  assert.deepEqual(statusPageFrameOrigins('', 'ops.example.com', 'https:'), []);
  assert.deepEqual(statusPageFrameOrigins('/relative', 'ops.example.com', 'https:'), []);
});

test('originOf accepts only absolute http(s) URLs', () => {
  assert.equal(originOf('https://api.example.com/v1'), 'https://api.example.com');
  assert.equal(originOf('/api'), null);
  assert.equal(originOf('javascript:alert(1)'), null);
  assert.equal(originOf(undefined), null);
});

test('hstsHeader needs HTTPS and a positive max-age', () => {
  assert.equal(hstsHeader('31536000', true), 'max-age=31536000; includeSubDomains');
  assert.equal(hstsHeader('31536000', false), null);
  assert.equal(hstsHeader('0', true), null);
  assert.equal(hstsHeader(undefined, true), null);
  assert.equal(hstsHeader('nope', true), null);
});

test('newNonce is fresh base64', () => {
  const a = newNonce();
  const b = newNonce();
  assert.notEqual(a, b);
  assert.match(a, /^[A-Za-z0-9+/]{22}==$/);
});
