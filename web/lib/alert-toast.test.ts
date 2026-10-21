import assert from 'node:assert/strict';
import test from 'node:test';
import { alertSubject, alertToast, listSubjects, summarizeAlertBurst } from './alert-toast';
import type { Alert } from './types';

function alert(overrides: Partial<Alert>): Alert {
  return {
    id: 'a1',
    tenant_id: 't',
    status: 'active',
    triggered_at: '2026-10-21T10:00:00Z',
    failure_count: 3,
    kind: 'availability',
    created_at: '2026-10-21T10:00:00Z',
    updated_at: '2026-10-21T10:00:00Z',
    ...overrides,
  } as Alert;
}

test('wording follows the alert kind', () => {
  assert.equal(alertToast('created', alert({ monitor_name: 'Orders API' }))?.message, 'Orders API is down');
  assert.equal(
    alertToast('created', alert({ kind: 'tls_expiry', monitor_name: 'Shop', metric_value: 6.4 }))?.message,
    'Shop certificate expires in 6d'
  );
  assert.equal(
    alertToast('created', alert({ kind: 'latency_anomaly', monitor_name: 'Search', observed_latency_ms: 812.4 }))?.message,
    'Search latency is anomalous (812 ms)'
  );
  assert.match(
    alertToast('created', alert({ kind: 'host_metric', monitor_name: 'web-03', metric_name: 'system.cpu.utilization', metric_value: 0.93 }))
      ?.message ?? '',
    /^web-03 CPU at 93\.0%$/
  );
  const mesh = alert({ kind: 'mesh_edge', source_location_name: 'Paris', target_location_name: 'Frankfurt' });
  assert.equal(alertSubject(mesh), 'Paris → Frankfurt');
  assert.equal(alertToast('created', mesh)?.message, 'Paris → Frankfurt path is down');
});

test('severity: outages are errors, softer kinds are warnings', () => {
  assert.equal(alertToast('created', alert({ monitor_name: 'x' }))?.type, 'error');
  assert.equal(alertToast('created', alert({ kind: 'tls_expiry', monitor_name: 'x' }))?.type, 'warning');
  // A stream payload without a kind reads "is down", so it is an error too.
  const noKind = alertToast('created', alert({ kind: undefined, monitor_name: 'Storefront' }));
  assert.equal(noKind?.message, 'Storefront is down');
  assert.equal(noKind?.type, 'error');
});

test('resolves never claim the target is back up; acks get no toast', () => {
  const resolved = alertToast('resolved', alert({ monitor_name: 'Orders API', status: 'resolved' }));
  assert.equal(resolved?.message, 'Orders API: downtime alert closed');
  assert.doesNotMatch(resolved?.message ?? '', /back up/);
  assert.equal(alertToast('acknowledged', alert({})), null);
});

test('dependency-suppressed alerts group under their root cause', () => {
  const dependent = alert({
    monitor_name: 'Checkout',
    suppression_reason: 'dependency',
    root_cause_monitor_id: 'db',
    root_cause_monitor_name: 'Orders DB primary',
  });
  const toast = alertToast('created', dependent);
  assert.equal(toast?.group, 'alert-dependents:db');
  assert.equal(toast?.title, 'Likely caused by Orders DB primary');
  const burst = summarizeAlertBurst('created', [dependent, { ...dependent, monitor_name: 'Cart' }]);
  assert.equal(burst.title, 'Likely caused by Orders DB primary');
  assert.equal(burst.message, '2 dependent alerts: Checkout and Cart');
});

test('bursts become one counted summary', () => {
  const names = ['A', 'B', 'C', 'D', 'E'];
  const burst = summarizeAlertBurst('created', names.map((n) => alert({ monitor_name: n })));
  assert.deepEqual(burst, { title: '5 alerts triggered', message: 'A, B and 3 more' });
  assert.deepEqual(summarizeAlertBurst('created', [alert({ monitor_name: 'A' })]), {
    title: 'Alert triggered',
    message: 'A is down',
  });
  assert.equal(summarizeAlertBurst('resolved', [alert({}), alert({})]).title, '2 alerts resolved');
});

test('listSubjects', () => {
  assert.equal(listSubjects([]), '');
  assert.equal(listSubjects(['A']), 'A');
  assert.equal(listSubjects(['A', 'B', 'C']), 'A, B and C');
  assert.equal(listSubjects(['A', 'A', 'B']), 'A and B');
  assert.equal(listSubjects(['A', 'B', 'C', 'D']), 'A, B and 2 more');
});
