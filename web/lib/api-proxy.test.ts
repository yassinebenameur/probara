import assert from 'node:assert/strict';
import test from 'node:test';
import { browserHost, copyHeaders, isSameOriginWrite, vouchForSameOriginWrite } from './api-proxy';

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
  return { method, incoming };
}

test('copyHeaders forwards the browser host as X-Forwarded-Host, never a client value unless trusted', () => {
  const incoming = new Headers({ host: 'localhost:3000', 'x-forwarded-host': 'evil.example, other' });
  assert.equal(copyHeaders(incoming, false, false).get('x-forwarded-host'), 'localhost:3000');
  assert.equal(copyHeaders(incoming, false, true).get('x-forwarded-host'), 'evil.example');
  assert.equal(browserHost(new Headers({ host: 'probara-frontend:3000' }), true), 'probara-frontend:3000', 'opt-in without the header: Host');
});

test('the UI proxy vouches for writes from its own origin, on whatever host it is reached', () => {
  // Port-forward on any port, 127.0.0.1, a LAN address, and an ingress or the
  // release edge proxy, which keep the browser's Host.
  for (const host of ['localhost:3000', 'localhost:18080', '127.0.0.1:3000', '192.168.1.20:3000', 'probara.example.com']) {
    const w = write(host, { origin: `http://${host}`, 'sec-fetch-site': 'same-origin', referer: `http://${host}/login` });
    assert.ok(isSameOriginWrite(w.method, w.incoming, false), host);
    const out = copyHeaders(w.incoming, false, false);
    vouchForSameOriginWrite(w.method, w.incoming, out, false);
    assert.equal(out.get('origin'), null, host);
    assert.equal(out.get('referer'), null, host);
    assert.equal(out.get('sec-fetch-site'), null, host);
    assert.equal(out.get('cookie'), 'admin_access=abc', 'everything else is forwarded');
  }
  // TLS at the ingress (Host kept, X-Forwarded-Proto: https).
  const tls = write('probara.example.com', { 'x-forwarded-proto': 'https', origin: 'https://probara.example.com' });
  assert.ok(isSameOriginWrite(tls.method, tls.incoming, false));
  // Only Sec-Fetch-Site, or only a Referer, from the UI itself.
  assert.ok(isSameOriginWrite('DELETE', write('localhost:3000', { 'sec-fetch-site': 'same-origin' }).incoming, false));
  assert.ok(isSameOriginWrite('PATCH', write('localhost:3000', { referer: 'http://localhost:3000/monitors' }).incoming, false));
});

test('a client-set X-Forwarded-Host never makes the proxy vouch', () => {
  // A same-origin script on another origin, reaching the frontend through
  // something that forwards client headers: Host is the frontend's internal
  // name, X-Forwarded-Host the attacker's own host.
  const viaForwarder = write('probara-frontend:3000', {
    'x-forwarded-host': 'status.example.com',
    origin: 'https://status.example.com',
    'sec-fetch-site': 'same-origin',
  });
  assert.ok(!isSameOriginWrite(viaForwarder.method, viaForwarder.incoming, false));
  const out = copyHeaders(viaForwarder.incoming, false, false);
  vouchForSameOriginWrite(viaForwarder.method, viaForwarder.incoming, out, false);
  assert.equal(out.get('origin'), 'https://status.example.com', 'kept for the API to refuse');
  assert.equal(out.get('x-forwarded-host'), 'probara-frontend:3000', 'the client value is not passed on');
  // Same, with the browser's Host but a forged forwarded host.
  const forged = write('localhost:3000', { 'x-forwarded-host': 'localhost:3001', origin: 'http://localhost:3001' });
  assert.ok(!isSameOriginWrite(forged.method, forged.incoming, false));
});

test('with API_PROXY_TRUST_FORWARDED_HOST only the forwarded host counts', () => {
  const behind = write('probara-frontend:3000', {
    'x-forwarded-host': 'probara.example.com',
    'x-forwarded-proto': 'https',
    origin: 'https://probara.example.com',
  });
  assert.ok(!isSameOriginWrite(behind.method, behind.incoming, false), 'not without the opt-in');
  assert.ok(isSameOriginWrite(behind.method, behind.incoming, true));
  // Host matching the Origin is not enough once the forwarded host is trusted.
  const hostOnly = write('probara.example.com', { 'x-forwarded-host': 'probara-edge', origin: 'http://probara.example.com' });
  assert.ok(!isSameOriginWrite(hostOnly.method, hostOnly.incoming, true));
});

test('the UI proxy forwards any other write unchanged, for the API to judge', () => {
  const attacks: Record<string, ReturnType<typeof write>> = {
    'status page on another port': write('localhost:3000', { origin: 'http://localhost:3001', 'sec-fetch-site': 'same-site' }),
    'sibling subdomain': write('app.example.com', { origin: 'https://status.example.com', 'sec-fetch-site': 'same-site' }),
    'cross-site page': write('app.example.com', { origin: 'https://evil.example', 'sec-fetch-site': 'cross-site' }),
    'opaque origin': write('localhost:3000', { origin: 'null', 'sec-fetch-site': 'cross-site' }),
    'same-site fetch metadata without Origin': write('localhost:3000', { 'sec-fetch-site': 'same-site' }),
    'foreign Referer only': write('localhost:3000', { referer: 'http://localhost:3001/public/status/x' }),
    'http page against an HTTPS UI': write('probara.example.com', {
      'x-forwarded-proto': 'https',
      origin: 'http://probara.example.com',
    }),
  };
  for (const [name, w] of Object.entries(attacks)) {
    assert.ok(!isSameOriginWrite(w.method, w.incoming, false), name);
    const out = copyHeaders(w.incoming, false, false);
    vouchForSameOriginWrite(w.method, w.incoming, out, false);
    assert.ok(
      out.get('origin') !== null || out.get('sec-fetch-site') !== null || out.get('referer') !== null,
      `${name}: headers kept`
    );
  }
  // Reads are never touched, and a client sending none of the headers is left as it is.
  const read = write('localhost:3000', { origin: 'http://localhost:3000' }, 'GET');
  assert.ok(!isSameOriginWrite(read.method, read.incoming, false));
  const curl = write('localhost:3000', {});
  assert.ok(!isSameOriginWrite(curl.method, curl.incoming, false));
});
