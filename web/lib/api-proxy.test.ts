import assert from 'node:assert/strict';
import test from 'node:test';
import { copyHeaders, isSameOriginWrite, vouchForSameOriginWrite } from './api-proxy';

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

// What the UI proxy receives for a write, and whether it vouches for it.
function write(host: string, headers: Record<string, string>, method = 'POST') {
  const incoming = new Headers({ host, cookie: 'admin_access=abc', 'content-type': 'application/json', ...headers });
  return { method, incoming, url: `http://${host}/api/v1/auth/login` };
}

test('the UI proxy vouches for writes from its own origin, on whatever host it is reached', () => {
  for (const host of ['localhost:3000', 'localhost:18080', '127.0.0.1:3000', '192.168.1.20:3000']) {
    const w = write(host, { origin: `http://${host}`, 'sec-fetch-site': 'same-origin', referer: `http://${host}/login` });
    assert.ok(isSameOriginWrite(w.method, w.incoming, w.url), host);
    const out = copyHeaders(w.incoming);
    vouchForSameOriginWrite(w.method, w.incoming, out, w.url);
    assert.equal(out.get('origin'), null, host);
    assert.equal(out.get('referer'), null, host);
    assert.equal(out.get('sec-fetch-site'), null, host);
    assert.equal(out.get('cookie'), 'admin_access=abc', 'everything else is forwarded');
  }
  // Behind an ingress or the release edge proxy: internal Host, browser host forwarded.
  const behind = write('probara-frontend:3000', {
    'x-forwarded-host': 'probara.example.com',
    'x-forwarded-proto': 'https',
    origin: 'https://probara.example.com',
  });
  assert.ok(isSameOriginWrite(behind.method, behind.incoming, behind.url));
  // Only Sec-Fetch-Site, or only a Referer, from the UI itself.
  assert.ok(isSameOriginWrite('DELETE', write('localhost:3000', { 'sec-fetch-site': 'same-origin' }).incoming, 'http://localhost:3000/api/x'));
  assert.ok(isSameOriginWrite('PATCH', write('localhost:3000', { referer: 'http://localhost:3000/monitors' }).incoming, 'http://localhost:3000/api/x'));
});

test('the UI proxy forwards any other write unchanged, for the API to judge', () => {
  const attacks: Record<string, ReturnType<typeof write>> = {
    'status page on another port': write('localhost:3000', { origin: 'http://localhost:3001', 'sec-fetch-site': 'same-site' }),
    'sibling subdomain': write('app.example.com', { origin: 'https://status.example.com', 'sec-fetch-site': 'same-site' }),
    'cross-site page': write('app.example.com', { origin: 'https://evil.example', 'sec-fetch-site': 'cross-site' }),
    'opaque origin': write('localhost:3000', { origin: 'null', 'sec-fetch-site': 'cross-site' }),
    'same-site fetch metadata without Origin': write('localhost:3000', { 'sec-fetch-site': 'same-site' }),
    'foreign Referer only': write('localhost:3000', { referer: 'http://localhost:3001/public/status/x' }),
    'http page against an HTTPS UI': write('probara-frontend:3000', {
      'x-forwarded-host': 'probara.example.com',
      'x-forwarded-proto': 'https',
      origin: 'http://probara.example.com',
    }),
  };
  for (const [name, w] of Object.entries(attacks)) {
    assert.ok(!isSameOriginWrite(w.method, w.incoming, w.url), name);
    const out = copyHeaders(w.incoming);
    vouchForSameOriginWrite(w.method, w.incoming, out, w.url);
    assert.ok(
      out.get('origin') !== null || out.get('sec-fetch-site') !== null || out.get('referer') !== null,
      `${name}: headers kept`
    );
  }
  // Reads are never touched, and a client sending none of the headers is left as it is.
  const read = write('localhost:3000', { origin: 'http://localhost:3000' }, 'GET');
  assert.ok(!isSameOriginWrite(read.method, read.incoming, read.url));
  const curl = write('localhost:3000', {});
  assert.ok(!isSameOriginWrite(curl.method, curl.incoming, curl.url));
});
