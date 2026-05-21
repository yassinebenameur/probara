import assert from 'node:assert/strict';
import test from 'node:test';
import { getAllMonitors, sortMonitorsByName } from './monitor-list';
import type { Monitor, MonitorListResponse } from './types';

function monitor(id: number): Monitor {
  return {
    id: `monitor-${id}`,
    tenant_id: 'tenant-1',
    name: `Monitor ${id}`,
    type: 'http',
    interval_seconds: 60,
    timeout_seconds: 10,
    enabled: true,
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
