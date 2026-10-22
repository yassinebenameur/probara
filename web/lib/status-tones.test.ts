import assert from 'node:assert/strict';
import test from 'node:test';
import {
  alertStatusTone,
  checkResultLabel,
  checkResultTone,
  DEFAULT_LOSS_MAX_PCT,
  DEFAULT_LOSS_WARN_PCT,
  DEFAULT_MOS_MIN,
  DEFAULT_MOS_WARN,
  DEFAULT_UTILIZATION_ALERT,
  failedChecksTone,
  incidentSeverityLabel,
  incidentSeverityTone,
  incidentStateTone,
  lossTone,
  mosTone,
  thresholdTone,
  uptimeTone,
  UPTIME_TONE_TEXT,
  utilizationTone,
} from './status-tones';
import type { MetricRule } from './types';

test('uptimeTone: default target keeps the 99 / 95 split', () => {
  assert.equal(uptimeTone(100), 'good');
  assert.equal(uptimeTone(99), 'good');
  assert.equal(uptimeTone(98.5), 'warn');
  assert.equal(uptimeTone(95), 'warn');
  assert.equal(uptimeTone(36.36), 'bad');
  assert.equal(uptimeTone(null), 'none');
  assert.equal(UPTIME_TONE_TEXT[uptimeTone(36.36)], 'text-rose-300');
});

test('uptimeTone follows an SLA target', () => {
  assert.equal(uptimeTone(99.95, 99.9), 'good');
  assert.equal(uptimeTone(99.5, 99.9), 'warn');
  assert.equal(uptimeTone(95.5, 99.9), 'bad');
});

test('alert, incident and check tones', () => {
  assert.equal(alertStatusTone('active'), 'danger');
  assert.equal(alertStatusTone('acknowledged'), 'warning');
  assert.equal(alertStatusTone('resolved'), 'success');
  assert.equal(incidentStateTone('monitoring'), 'warning');
  assert.equal(incidentStateTone('investigating'), 'danger');
  assert.equal(incidentSeverityTone('high'), 'danger');
  assert.equal(incidentSeverityTone('low'), 'neutral');
  assert.equal(incidentSeverityLabel('medium'), 'Medium');
  assert.equal(checkResultTone('failure'), 'danger');
  assert.equal(checkResultLabel('failure'), 'Failed');
  assert.equal(checkResultLabel('weird'), 'weird');
});

test('failedChecksTone: a bucket is toned by its failed checks, not by an availability target', () => {
  assert.equal(failedChecksTone(0, 60), 'good');
  // One flaky check in sixty, and one location of three failing every check,
  // are warnings, never a breach-red bar under a 100% availability headline.
  assert.equal(failedChecksTone(1, 60), 'warn');
  assert.equal(failedChecksTone(30, 90), 'warn');
  assert.equal(failedChecksTone(1, 600), 'warn');
  // Half or more failed is bad; a single failed check is 1 of 1.
  assert.equal(failedChecksTone(45, 90), 'bad');
  assert.equal(failedChecksTone(30, 30), 'bad');
  assert.equal(failedChecksTone(1, 1), 'bad');
  assert.equal(failedChecksTone(0, 0), 'none');
});

test('utilizationTone follows the monitor\'s own metric rule', () => {
  const rules: MetricRule[] = [
    { metric_name: 'system.filesystem.utilization', operator: '>=', threshold: 0.85, attribute_filters: { mountpoint: '/var/lib/postgresql' } },
    { metric_name: 'system.memory.utilization', operator: '>=', threshold: 0.92, attribute_filters: { state: 'used' } },
  ];
  const pg = { mountpoint: '/var/lib/postgresql' };
  assert.equal(utilizationTone(0.839, 'system.filesystem.utilization', pg, rules), 'warn');
  assert.equal(utilizationTone(0.85, 'system.filesystem.utilization', pg, rules), 'bad');
  assert.equal(utilizationTone(0.7, 'system.filesystem.utilization', pg, rules), 'good');
  // 92% rule: 81% is clear of it, 83% is within the 10-point band.
  assert.equal(utilizationTone(0.81, 'system.memory.utilization', { state: 'used' }, rules), 'good');
  assert.equal(utilizationTone(0.83, 'system.memory.utilization', { state: 'used' }, rules), 'warn');
  assert.equal(utilizationTone(0.83, 'system.memory.utilization', undefined, rules), 'warn');
  assert.equal(utilizationTone(0.7, 'system.memory.utilization', { state: 'used' }, rules), 'good');
  assert.equal(utilizationTone(0.93, 'system.memory.utilization', { state: 'used' }, rules), 'bad');
});

test('utilizationTone: a rule for another mount does not apply; the default stands in', () => {
  const rules: MetricRule[] = [
    { metric_name: 'system.filesystem.utilization', operator: '>=', threshold: 0.5, attribute_filters: { mountpoint: '/data' } },
  ];
  assert.equal(utilizationTone(0.6, 'system.filesystem.utilization', { mountpoint: '/' }, rules), 'good');
  assert.equal(utilizationTone(0.6, 'system.filesystem.utilization', { mountpoint: '/data' }, rules), 'bad');
  assert.equal(utilizationTone(DEFAULT_UTILIZATION_ALERT, 'system.cpu.utilization', {}, null), 'bad');
  assert.equal(utilizationTone(0.85, 'system.cpu.utilization', {}, []), 'warn');
  assert.equal(utilizationTone(0.25, 'system.cpu.utilization', {}, []), 'good');
  assert.equal(utilizationTone(null, 'system.cpu.utilization', {}, []), 'none');
});

test('utilizationTone handles <= rules and takes the worst of several', () => {
  const low: MetricRule[] = [{ metric_name: 'm', operator: '<=', threshold: 0.2 }];
  assert.equal(utilizationTone(0.1, 'm', {}, low), 'bad');
  assert.equal(utilizationTone(0.25, 'm', {}, low), 'warn');
  assert.equal(utilizationTone(0.5, 'm', {}, low), 'good');
  const both: MetricRule[] = [
    { metric_name: 'm', operator: '>=', threshold: 0.95 },
    { metric_name: 'm', operator: '>=', threshold: 0.6 },
  ];
  assert.equal(utilizationTone(0.65, 'm', {}, both), 'bad');
});

test('mosTone judges a MOS as the worker does: below the minimum fails, below the warning warns', () => {
  assert.equal(mosTone(4.2, 4.0, 3.6), 'good');
  assert.equal(mosTone(4.0, 4.0, 3.6), 'good');
  assert.equal(mosTone(3.9, 4.0, 3.6), 'warn');
  assert.equal(mosTone(3.59, 4.0, 3.6), 'bad');
  // Without thresholds of its own, the default bands stand in.
  assert.equal(mosTone(DEFAULT_MOS_WARN - 0.01), 'warn');
  assert.equal(mosTone(DEFAULT_MOS_MIN - 0.01), 'bad');
  assert.equal(mosTone(4.37), 'good');
  // One threshold of its own: only it applies.
  assert.equal(mosTone(3.0, 3.5), 'warn');
  assert.equal(mosTone(3.9, undefined, 3.0), 'good');
  assert.equal(mosTone(undefined), 'none');
});

test('lossTone judges loss as the worker does: above the maximum fails, above the warning warns', () => {
  assert.equal(lossTone(0.5, 1, 2), 'good');
  assert.equal(lossTone(1, 1, 2), 'good');
  assert.equal(lossTone(1.5, 1, 2), 'warn');
  assert.equal(lossTone(2.01, 1, 2), 'bad');
  assert.equal(lossTone(DEFAULT_LOSS_WARN_PCT + 0.01), 'warn');
  assert.equal(lossTone(DEFAULT_LOSS_MAX_PCT + 0.01), 'bad');
  assert.equal(lossTone(0), 'good');
  assert.equal(lossTone(3, undefined, 2), 'bad');
  assert.equal(lossTone(10, 5), 'warn');
  assert.equal(lossTone(null), 'none');
});

test('thresholdTone judges jitter and round trip by the monitor\'s own warn and max only', () => {
  assert.equal(thresholdTone(34, 20, 30), 'bad');
  assert.equal(thresholdTone(30, 20, 30), 'warn');
  assert.equal(thresholdTone(20, 20, 30), 'good');
  assert.equal(thresholdTone(600, undefined, 500), 'bad');
  assert.equal(thresholdTone(600), 'good');
  assert.equal(thresholdTone(undefined, 1, 2), 'none');
});
