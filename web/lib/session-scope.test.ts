import assert from 'node:assert/strict';
import test from 'node:test';

// Browser globals the client reads: storage, window events and location, fetch.
const store = new Map<string, string>();
const localStorageShim = {
  getItem: (k: string) => store.get(k) ?? null,
  setItem: (k: string, v: string) => void store.set(k, v),
  removeItem: (k: string) => void store.delete(k),
};
const windowShim = Object.assign(new EventTarget(), { location: { href: '/settings' } });
Object.assign(globalThis, { window: windowShim, localStorage: localStorageShim });

// eslint-disable-next-line @typescript-eslint/no-require-imports
const { apiRequest, isPlatformPath } = require('./api/core') as typeof import('./api/core');
// eslint-disable-next-line @typescript-eslint/no-require-imports
const tenant = require('./tenant') as typeof import('./tenant');

type Call = { path: string; tenant: string | null };

/**
 * A fake API for a member who belongs to no workspace. `session` is what the
 * access cookie does (live, or expired so every call answers 401), `refresh`
 * what POST /auth/refresh answers.
 */
function fakeServer() {
  const state = { session: 'live' as 'live' | 'expired', refresh: 200 };
  const calls: Call[] = [];
  const json = (status: number, body: unknown, headers: Record<string, string> = {}) =>
    new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json', ...headers } });
  const fetchMock = async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input).replace(/^.*\/api/, '');
    const headers = (init?.headers ?? {}) as Record<string, string>;
    calls.push({ path, tenant: headers['X-Tenant-ID'] ?? null });
    if (path === '/v1/auth/refresh') {
      if (state.refresh === 200) state.session = 'live';
      if (state.refresh === 429) {
        return json(429, { error: 'rate_limited', message: 'too many requests' }, { 'retry-after': '0' });
      }
      if (state.refresh === 403) {
        return json(403, { error: 'cross_origin_refused', message: 'origin http://localhost:3001 is not the operator UI; set APP_BASE_URL' });
      }
      return json(state.refresh, {});
    }
    if (state.session === 'expired') return json(401, { error: 'unauthorized', message: 'expired' });
    if (path.startsWith('/v1/tenants')) return json(200, { items: [] });
    return json(200, { items: [{ id: 'session-1' }] });
  };
  Object.assign(globalThis, { fetch: fetchMock });
  return { calls, state };
}

test('platform paths are recognised by segment', () => {
  for (const p of ['/v1/account/sessions', '/v1/users?page=1', '/v1/users/u1/sessions', '/v1/tenants', '/v1/oauth/grants/g1', '/v1/openapi.json']) {
    assert.ok(isPlatformPath(p), p);
  }
  for (const p of ['/v1/monitors', '/v1/usersettings', '/v1/oauth/authorize/decision', '/v1/auth-context']) {
    assert.ok(!isPlatformPath(p), p);
  }
});

test('a member with no workspace can still manage their own sessions', async () => {
  const server = fakeServer();
  tenant.setSelectedTenantId('stale');

  await assert.rejects(apiRequest('GET', '/v1/monitors'), (err: { error?: string }) => err.error === 'no_workspace');
  assert.equal(server.calls.filter((c) => c.path === '/v1/monitors').length, 0, 'a workspace-scoped call is not sent');

  const before = server.calls.length;
  const sessions = await apiRequest<{ items: unknown[] }>('GET', '/v1/account/sessions');
  assert.equal(sessions.items.length, 1);
  await apiRequest('POST', '/v1/account/sessions/revoke-others');
  const sent = server.calls.slice(before);
  assert.deepEqual(
    sent.map((c) => c.path),
    ['/v1/account/sessions', '/v1/account/sessions/revoke-others'],
    'no workspace lookup before a platform call'
  );
  assert.ok(sent.every((c) => c.tenant === null), 'platform calls never carry X-Tenant-ID');
});

test('a rate-limited refresh keeps the session; a refused one signs out', async () => {
  const server = fakeServer();
  server.state.session = 'expired';
  server.state.refresh = 429;

  const before = server.calls.length;
  await assert.rejects(
    apiRequest('GET', '/v1/account/sessions'),
    (err: { error?: string }) => err.error === 'rate_limited'
  );
  const refreshes = server.calls.slice(before).filter((c) => c.path === '/v1/auth/refresh').length;
  assert.equal(refreshes, 4, 'one refresh and three retries');
  assert.equal(windowShim.location.href, '/settings', 'not sent to the sign-in page');

  // Once the address has budget again, the next call refreshes and goes through.
  server.state.refresh = 200;
  const sessions = await apiRequest<{ items: unknown[] }>('GET', '/v1/account/sessions');
  assert.equal(sessions.items.length, 1);
  assert.equal(windowShim.location.href, '/settings');

  // A refresh the API refuses (401) is a lost session.
  server.state.session = 'expired';
  server.state.refresh = 401;
  await assert.rejects(apiRequest('GET', '/v1/account/sessions'));
  assert.equal(windowShim.location.href, '/login');
});

test('a refresh refused for its origin is a configuration error, not a sign-out', async () => {
  const server = fakeServer();
  windowShim.location.href = '/settings';
  server.state.session = 'expired';
  server.state.refresh = 403;
  const logged: unknown[] = [];
  const original = console.error;
  console.error = (...args: unknown[]) => void logged.push(args.join(' '));
  try {
    await assert.rejects(
      apiRequest('GET', '/v1/account/sessions'),
      (err: { error?: string; message?: string }) =>
        err.error === 'cross_origin_refused' && /APP_BASE_URL/.test(err.message ?? '')
    );
  } finally {
    console.error = original;
  }
  assert.equal(windowShim.location.href, '/settings', 'not sent to the sign-in page');
  assert.equal(logged.length, 1, 'reported, not silent');
});
