import assert from 'node:assert/strict';
import test from 'node:test';
import {
  deliveryAlertSubject,
  deliveryDetail,
  deliveryEventLabel,
  deliveryFilterOutcomes,
  deliveryOutcomeLabel,
  deliveryReasonHint,
  deliveryReasonLabel,
} from './deliveries';
import { deliveryOutcomeTone } from './status-tones';

test('drops are neutral and never worded as failures', () => {
  for (const reason of ['resolved', 'unacknowledged', 'muted', 'muted_requeued', 'reopened', 'earlier_episode', 'channel_inactive', 'channel_changed']) {
    assert.equal(deliveryOutcomeTone('dropped', reason), 'neutral', reason);
    assert.equal(deliveryOutcomeLabel({ outcome: 'dropped', reason }), 'Not sent');
    const label = deliveryReasonLabel(reason) ?? '';
    assert.doesNotMatch(label, /fail|error/i, reason);
    assert.ok(deliveryReasonHint(reason), `${reason} has an explanation`);
  }
});

test('failures: amber while retried, red when rejected or out of retries', () => {
  assert.equal(deliveryOutcomeTone('delivered'), 'success');
  assert.equal(deliveryOutcomeTone('failed'), 'warning');
  assert.equal(deliveryOutcomeTone('failed', 'retries_exhausted'), 'danger');
  assert.equal(deliveryOutcomeTone('permanent_failure', 'egress_blocked'), 'danger');
  assert.equal(deliveryOutcomeLabel({ outcome: 'failed', reason: null }), 'Failed, retrying');
  assert.equal(deliveryOutcomeLabel({ outcome: 'failed', reason: 'retries_exhausted' }), 'Failed, gave up');
  assert.equal(deliveryOutcomeLabel({ outcome: 'permanent_failure', reason: null }), 'Rejected');
});

test('every failure reason is explained, unknown ones render as sent', () => {
  for (const reason of ['egress_blocked', 'invalid_config', 'plugin_missing', 'retries_exhausted']) {
    assert.ok(deliveryReasonHint(reason), reason);
  }
  assert.equal(deliveryReasonLabel('some_new_cause'), 'some new cause');
  assert.equal(deliveryReasonLabel(null), null);
});

test('events, subjects and details', () => {
  assert.equal(deliveryEventLabel({ event_type: 'created', test: false }), 'Page');
  assert.equal(deliveryEventLabel({ event_type: 'created', test: true }), 'Test notification');
  assert.equal(deliveryEventLabel({ event_type: 'brand_new', test: false }), 'Brand new');
  const none = { source_location_name: null, target_location_name: null };
  assert.equal(deliveryAlertSubject({ ...none, monitor_name: 'API', alert_kind: 'availability', test: false }), 'API');
  assert.equal(deliveryAlertSubject({ ...none, monitor_name: null, alert_kind: 'mesh_edge', test: false }), 'mesh edge alert');
  assert.equal(
    deliveryAlertSubject({ monitor_name: null, alert_kind: 'location_offline', test: false, source_location_name: 'Paris', target_location_name: null }),
    'Paris offline'
  );
  assert.equal(
    deliveryAlertSubject({ monitor_name: null, alert_kind: 'mesh_edge', test: false, source_location_name: 'Paris', target_location_name: 'Oslo' }),
    'Mesh: Paris → Oslo'
  );
  assert.equal(
    deliveryDetail({ recipient: '********4321', provider_status: 401, error: 'twilio returned status 401' }),
    '********4321 · HTTP 401 · twilio returned status 401'
  );
  assert.equal(deliveryDetail({ recipient: null, provider_status: null, error: null }), '');
});

test('outcome filters', () => {
  assert.deepEqual(deliveryFilterOutcomes('all'), []);
  assert.deepEqual(deliveryFilterOutcomes('failures'), ['failed', 'permanent_failure']);
  assert.deepEqual(deliveryFilterOutcomes('dropped'), ['dropped']);
});

test('a relapse notice is labelled as one', () => {
  assert.equal(deliveryEventLabel({ event_type: 'relapsed', test: false }), 'Relapse notice');
});
