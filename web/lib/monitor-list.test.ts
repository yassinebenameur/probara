import assert from 'node:assert/strict';
import test from 'node:test';
import {
  formatMonitorListParams,
  getAllMonitors,
  type MonitorSortContext,
  parseMonitorListParams,
  sortMonitorList,
  sortMonitorsByName,
} from './monitor-list';
import { getEffectiveMonitorStatus } from './monitor-utils';
import type { CheckResult, Monitor, MonitorListResponse } from './types';

function monitor(id: number): Monitor {
  return {
    id: `monitor-${id}`,
    tenant_id: 'tenant-1',
    name: `Monitor ${id}`,
    type: 'http',
    interval_seconds: 60,
    timeout_seconds: 10,
    enabled: true,
    consecutive_failures_threshold: 2,
    notification_mode: 'default',
    created_at: '2026-05-21T00:00:00.000Z',
    updated_at: '2026-05-21T00:00:00.000Z',
  };
}

function page(items: Monitor[], pageNumber: number, total: number): MonitorListResponse {
  return {
    items,
    page: pageNumber,
    page_size: 100,
    total,
  };
}

test('getAllMonitors fetches every API page when there are more than 100 monitors', async () => {
  const pages = [
    page(Array.from({ length: 100 }, (_, index) => monitor(index + 1)), 1, 125),
    page(Array.from({ length: 25 }, (_, index) => monitor(index + 101)), 2, 125),
  ];
  const requestedPages: number[] = [];

  const monitors = await getAllMonitors(async (params) => {
    requestedPages.push(params?.page || 1);
    return pages[(params?.page || 1) - 1];
  });

  assert.equal(monitors.length, 125);
  assert.deepEqual(requestedPages, [1, 2]);
  assert.ok(monitors.some((item) => item.id === 'monitor-125'));
});

test('getAllMonitors returns monitors alphabetically by name', async () => {
  const pages = [
    page(
      [
        { ...monitor(1), name: 'Zebra API' },
        { ...monitor(2), name: 'alpha API' },
        { ...monitor(3), name: 'Billing API' },
      ],
      1,
      3,
    ),
  ];

  const monitors = await getAllMonitors(async () => pages[0]);

  assert.deepEqual(
    monitors.map((item) => item.name),
    ['alpha API', 'Billing API', 'Zebra API'],
  );
});

test('sortMonitorsByName orders equal names by id without mutating the input', () => {
  const original = [
    { ...monitor(2), name: 'Shared Name' },
    { ...monitor(1), name: 'shared name' },
    { ...monitor(3), name: 'Alpha' },
  ];

  const sorted = sortMonitorsByName(original);

  assert.deepEqual(
    sorted.map((item) => item.id),
    ['monitor-3', 'monitor-1', 'monitor-2'],
  );
  assert.deepEqual(
    original.map((item) => item.id),
    ['monitor-2', 'monitor-1', 'monitor-3'],
  );
});

test('parseMonitorListParams reads deep links and falls back to defaults', () => {
  assert.deepEqual(parseMonitorListParams(''), {
    status: 'all',
    tags: [],
    unroutedOnly: false,
    sort: { key: 'status', dir: 'asc' },
  });
  assert.deepEqual(parseMonitorListParams('?status=down&tag=prod&tag=eu,db&routing=unrouted&sort=name'), {
    status: 'down',
    tags: ['prod', 'eu', 'db'],
    unroutedOnly: true,
    sort: { key: 'name', dir: 'asc' },
  });
  assert.equal(parseMonitorListParams('?status=degraded').status, 'down');
  assert.equal(parseMonitorListParams('?status=bogus').status, 'all');
  assert.deepEqual(parseMonitorListParams('?tag=a&tag=a,%20b').tags, ['a', 'b']);
});

test('formatMonitorListParams omits defaults, keeps other params and round-trips', () => {
  const defaults = parseMonitorListParams('');
  assert.equal(formatMonitorListParams(defaults, 'status=up&tag=x&keep=1'), 'keep=1');
  const params = {
    status: 'paused' as const,
    tags: ['prod', 'eu'],
    unroutedOnly: true,
    sort: { key: 'name' as const, dir: 'asc' as const },
  };
  const search = formatMonitorListParams(params);
  assert.equal(search, 'status=paused&tag=prod&tag=eu&routing=unrouted&sort=name');
  assert.deepEqual(parseMonitorListParams(search), params);
});

test('sort direction lives in ?dir= only when it differs from the key default', () => {
  const base = { status: 'all' as const, tags: [], unroutedOnly: false };
  assert.equal(formatMonitorListParams({ ...base, sort: { key: 'status', dir: 'asc' } }), '');
  assert.equal(formatMonitorListParams({ ...base, sort: { key: 'status', dir: 'desc' } }), 'dir=desc');
  // Latency starts slowest first.
  assert.equal(formatMonitorListParams({ ...base, sort: { key: 'latency', dir: 'desc' } }), 'sort=latency');
  assert.equal(formatMonitorListParams({ ...base, sort: { key: 'latency', dir: 'asc' } }), 'sort=latency&dir=asc');
  assert.deepEqual(parseMonitorListParams('?sort=latency').sort, { key: 'latency', dir: 'desc' });
  assert.deepEqual(parseMonitorListParams('?sort=last_check&dir=desc').sort, { key: 'last_check', dir: 'desc' });
  assert.deepEqual(parseMonitorListParams('?sort=bogus&dir=sideways').sort, { key: 'status', dir: 'asc' });
  // The bare-key form older callers pass still works.
  assert.equal(formatMonitorListParams({ ...base, sort: 'status' }), '');
  assert.equal(formatMonitorListParams({ ...base, sort: 'name' }, 'sort=success&dir=desc'), 'sort=name');
});

// ── List order ──

function result(id: string, status: CheckResult['status'], minutesAgo: number, latency = 100): CheckResult {
  return {
    id,
    status,
    result_source: 'monitor',
    latency_ms: latency,
    created_at: new Date(Date.UTC(2026, 9, 1, 12, 0) - minutesAgo * 60_000).toISOString(),
  } as unknown as CheckResult;
}

function fixture() {
  const m = (id: string, name: string, type: Monitor['type'] = 'http'): Monitor => ({ ...monitor(0), id, name, type });
  const monitors = {
    groupUp: m('g-up', 'Alpha group', 'group'),
    groupDown: m('g-down', 'Delta group', 'group'),
    member1: m('m1', 'Member one'),
    member2: m('m2', 'Member two'),
    member3: m('m3', 'Member three'),
    bravo: m('s-bravo', 'Bravo standalone'),
    charlie: m('s-charlie', 'charlie standalone'),
  };
  const results: Record<string, CheckResult[]> = {
    'g-up': [result('g1', 'success', 1)],
    // No roll-up result yet: last check falls back to its members.
    'g-down': [],
    m1: [result('a', 'success', 2, 50)],
    m2: [result('b', 'success', 3, 80)],
    // A Down member hides inside the second group.
    m3: Array.from({ length: 5 }, (_, i) => result(`c${i}`, 'failure', i)),
    // Down standalone monitor.
    's-bravo': Array.from({ length: 5 }, (_, i) => result(`d${i}`, 'failure', 10 + i)),
    's-charlie': [result('e', 'success', 30, 400)],
  };
  const members: Record<string, Monitor[]> = {
    'g-up': [monitors.member1, monitors.member2],
    'g-down': [monitors.member1, monitors.member3],
  };
  const ctx: MonitorSortContext = {
    statusOf: (item) => getEffectiveMonitorStatus(item, results[item.id] ?? []),
    resultsOf: (item) => results[item.id] ?? [],
    membersOf: (id) => members[id] ?? [],
  };
  const top = [monitors.groupUp, monitors.groupDown, monitors.bravo, monitors.charlie];
  return { monitors, ctx, top };
}

const names = (items: Monitor[]) => items.map((item) => item.name);

test('status sort: a Down standalone monitor sorts above an all-Up group; groups rank by their worst member', () => {
  const { ctx, top } = fixture();
  assert.deepEqual(names(sortMonitorList(top, { key: 'status', dir: 'asc' }, ctx)), [
    'Bravo standalone',
    'Delta group',
    'Alpha group',
    'charlie standalone',
  ]);
  // Reversed, best first; ties still read A→Z.
  assert.deepEqual(names(sortMonitorList(top, { key: 'status', dir: 'desc' }, ctx)), [
    'Alpha group',
    'charlie standalone',
    'Bravo standalone',
    'Delta group',
  ]);
});

test('name sort interleaves groups and monitors alphabetically, either direction', () => {
  const { ctx, top } = fixture();
  assert.deepEqual(names(sortMonitorList(top, { key: 'name', dir: 'asc' }, ctx)), [
    'Alpha group',
    'Bravo standalone',
    'charlie standalone',
    'Delta group',
  ]);
  assert.deepEqual(names(sortMonitorList(top, { key: 'name', dir: 'desc' }, ctx)), [
    'Delta group',
    'charlie standalone',
    'Bravo standalone',
    'Alpha group',
  ]);
});

test('success, latency and last-check sorts follow the displayed values, missing values last', () => {
  const { ctx, top, monitors } = fixture();
  // Success: Bravo 0% first, then the 100% rows by name; Delta has no checks of its own.
  assert.deepEqual(names(sortMonitorList(top, { key: 'success', dir: 'asc' }, ctx)), [
    'Bravo standalone',
    'Alpha group',
    'charlie standalone',
    'Delta group',
  ]);
  // Latency: groups and a monitor without a successful check have none, so they trail.
  assert.deepEqual(names(sortMonitorList(top, { key: 'latency', dir: 'desc' }, ctx)), [
    'charlie standalone',
    'Alpha group',
    'Bravo standalone',
    'Delta group',
  ]);
  assert.deepEqual(names(sortMonitorList(top, { key: 'latency', dir: 'asc' }, ctx)).slice(0, 1), ['charlie standalone']);
  // Last check: stalest first. Alpha shows its own roll-up check (1 min ago);
  // Delta has none yet, so it counts its most recent member check (m3, just now).
  assert.deepEqual(names(sortMonitorList(top, { key: 'last_check', dir: 'asc' }, ctx)), [
    'charlie standalone',
    'Bravo standalone',
    'Alpha group',
    'Delta group',
  ]);
  // Members inside a group use the same order.
  assert.deepEqual(
    names(sortMonitorList([monitors.member1, monitors.member3], { key: 'status', dir: 'asc' }, ctx)),
    ['Member three', 'Member one']
  );
});
