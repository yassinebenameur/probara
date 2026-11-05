import assert from 'node:assert/strict';
import test from 'node:test';
import { copyProxyRequestHeaders } from './proxy-headers';

const incoming = () =>
  new Headers({
    'x-forwarded-for': '6.6.6.6',
    'x-real-ip': '6.6.6.6',
    connection: 'keep-alive',
    host: 'ui.example',
    cookie: 'admin_session=abc',
    'content-type': 'application/json',
  });

test('drops client-settable IP headers and hop-by-hop headers by default', () => {
  const out = copyProxyRequestHeaders(incoming(), false);
  assert.equal(out.get('x-forwarded-for'), null);
  assert.equal(out.get('x-real-ip'), null);
  assert.equal(out.get('connection'), null);
  assert.equal(out.get('host'), null);
  assert.equal(out.get('cookie'), 'admin_session=abc');
  assert.equal(out.get('content-type'), 'application/json');
});

test('passes forwarding headers through only when an appending proxy is declared', () => {
  const out = copyProxyRequestHeaders(incoming(), true);
  assert.equal(out.get('x-forwarded-for'), '6.6.6.6');
  assert.equal(out.get('x-real-ip'), '6.6.6.6');
  assert.equal(out.get('host'), null);
});
