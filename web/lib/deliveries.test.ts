import assert from 'node:assert/strict';
import test from 'node:test';
import { deliveryAlertSubject, deliveryEventLabel, deliveryPathLabel, deliveryRecipients } from './deliveries';
import { alertFocusHref } from './alert-links';
import { deliveryFailed, deliveryOutcomeLabel, deliveryOutcomeTone } from './status-tones';

test('deliveryEventLabel names known events and humanizes new ones', () => {
  assert.equal(deliveryEventLabel('created'), 'Triggered');
  assert.equal(deliveryEventLabel('suspected_cause'), 'Suspected cause');
  assert.equal(deliveryEventLabel('reopened'), 'Reopened');
});

test('deliveryRecipients only describes fan-out deliveries', () => {
  assert.equal(deliveryRecipients({ recipients_total: null, recipients_delivered: null }), null);
  assert.equal(deliveryRecipients({ recipients_total: 3, recipients_delivered: 2 }), '2 of 3 recipients');
  assert.equal(deliveryRecipients({ recipients_total: 1, recipients_delivered: 1 }), '1 of 1 recipient');
});

test('delivery outcomes: only failures raise the channel badge', () => {
  assert.equal(deliveryFailed('failed'), true);
  assert.equal(deliveryFailed('permanent_failure'), true);
  assert.equal(deliveryFailed('dropped_ack'), false);
  assert.equal(deliveryFailed(undefined), false);
  assert.equal(deliveryOutcomeTone('permanent_failure'), 'danger');
  assert.equal(deliveryOutcomeTone('failed'), 'warning');
  assert.equal(deliveryOutcomeTone('dropped_superseded'), 'neutral');
  assert.equal(deliveryOutcomeLabel('mystery'), 'mystery');
  assert.equal(deliveryPathLabel('async'), 'Worker');
});

test('alertFocusHref links the focused alert view', () => {
  assert.equal(alertFocusHref('abc'), '/alerts?alert=abc');
});

test('deliveryAlertSubject prefers the monitor', () => {
  assert.equal(deliveryAlertSubject({ monitor_name: 'API', alert_kind: 'availability' }), 'API');
  assert.equal(deliveryAlertSubject({ monitor_name: null, alert_kind: 'mesh_edge' }), 'mesh edge alert');
});
