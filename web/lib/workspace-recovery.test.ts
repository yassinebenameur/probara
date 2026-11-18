import assert from 'node:assert/strict';
import test from 'node:test';

// Browser globals the client reads: storage, window events and fetch.
const store = new Map<string, string>();
const localStorageShim = {
  getItem: (k: string) => store.get(k) ?? null,
  setItem: (k: string, v: string) => void store.set(k, v),
  removeItem: (k: string) => void store.delete(k),
};
const windowShim = new EventTarget();
Object.assign(globalThis, { window: windowShim, localStorage: localStorageShim });

// eslint-disable-next-line @typescript-eslint/no-require-imports
const { apiRequest } = require('./api/core') as typeof import('./api/core');
// eslint-disable-next-line @typescript-eslint/no-require-imports
const tenant = require('./tenant') as typeof import('./tenant');

type Call = { path: string; tenant: string | null };

/**
 * A fake API: workspaces `gone` and `live`, until `gone` is deleted. A request
 * scoped to a workspace that does not exist answers 404 workspace_not_found,
 * as the auth middleware does, whatever the route.
 */
function fakeServer() {
  const workspaces = new Set(['gone', 'live']);
  const calls: Call[] = [];
  const json = (status: number, body: unknown) =>
    new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } });
  const fetchMock = async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const path = url.replace(/^.*\/api/, '');
    const headers = (init?.headers ?? {}) as Record<string, string>;
    const scoped = headers['X-Tenant-ID'] ?? null;
    calls.push({ path, tenant: scoped });
    if (scoped && !workspaces.has(scoped)) {
      return json(404, { error: 'workspace_not_found', message: 'workspace not found' });
    }
    if (path.startsWith('/v1/tenants')) {
      return json(200, { items: [...workspaces].sort().map((id) => ({ id, name: id })) });
    }
    return json(200, { items: [] });
  };
  Object.assign(globalThis, { fetch: fetchMock });
  return { calls, remove: (id: string) => workspaces.delete(id) };
}

test('a deleted selected workspace is dropped once and the switcher settles on a live one, with no loop', async () => {
  const server = fakeServer();
  tenant.setSelectedTenantId('gone');
  await apiRequest('GET', '/v1/monitors'); // validates and works while it exists
  assert.equal(tenant.getSelectedTenantId(), 'gone');

  // The switcher stand-in: on the list-changed event it reloads the list
  // and settles the selection, as TenantSwitcher does.
  let listReloads = 0;
  let settling: Promise<void> = Promise.resolve();
  windowShim.addEventListener(tenant.TENANTS_CHANGED_EVENT, () => {
    listReloads++;
    settling = apiRequest<{ items: { id: string }[] }>('GET', '/v1/tenants').then((list) => {
      tenant.selectTenant(list.items[0].id);
    });
  });

  server.remove('gone');
  const before = server.calls.length;
  // Two requests in flight when the workspace goes.
  const results = await Promise.allSettled([apiRequest('GET', '/v1/monitors'), apiRequest('GET', '/v1/alerts')]);
  await settling;

  for (const r of results) {
    assert.equal(r.status, 'rejected');
    assert.equal((r as PromiseRejectedResult).reason.error, 'workspace_not_found');
  }
  assert.equal(listReloads, 1, 'one deletion reloads the list exactly once');
  assert.equal(tenant.getSelectedTenantId(), 'live');
  const after = server.calls.slice(before);
  assert.deepEqual(
    after.filter((c) => c.path.startsWith('/v1/tenants')).map((c) => c.tenant),
    [null],
    'the workspace list is fetched once, without X-Tenant-ID'
  );
  assert.equal(after.length, 3, 'two failed requests and one list reload, nothing more');

  // The next request is scoped to the live workspace and works.
  await apiRequest('GET', '/v1/monitors');
  assert.equal(server.calls[server.calls.length - 1].tenant, 'live');
  assert.equal(listReloads, 1);
});
