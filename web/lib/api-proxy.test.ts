import assert from 'node:assert/strict';
import test from 'node:test';
import { copyHeaders } from './api-proxy';

const incoming = () =>
  new Headers({
    'x-forwarded-for': '6.6.6.6',
    'x-real-ip': '6.6.6.6',
    connection: 'keep-alive',
    host: 'ui.example',
    cookie: 'admin_access=abc',
    'content-type': 'application/json',
  });

test('copyHeaders drops client-settable IP headers and hop-by-hop headers by default', () => {
  const out = copyHeaders(incoming());
  assert.equal(out.get('x-forwarded-for'), null);
  assert.equal(out.get('x-real-ip'), null);
  assert.equal(out.get('connection'), null);
  assert.equal(out.get('host'), null);
  assert.equal(out.get('cookie'), 'admin_access=abc');
  assert.equal(out.get('content-type'), 'application/json');
});

test('copyHeaders passes the IP headers only when an outer proxy is declared', () => {
  const out = copyHeaders(incoming(), true);
  assert.equal(out.get('x-forwarded-for'), '6.6.6.6');
  assert.equal(out.get('x-real-ip'), '6.6.6.6');
  assert.equal(out.get('host'), null);
});
