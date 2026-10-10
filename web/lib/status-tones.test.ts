import assert from 'node:assert/strict';
import test from 'node:test';
import {
  alertStatusTone,
  checkResultLabel,
  checkResultTone,
  DEFAULT_UTILIZATION_ALERT,
  failedChecksTone,
  incidentSeverityLabel,
  incidentSeverityTone,
  incidentStateTone,
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
