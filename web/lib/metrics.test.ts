import assert from 'node:assert/strict';
import test from 'node:test';
import {
  displayAttr,
  formatAlertValue,
  formatMetricValue,
  formatSeriesLabel,
  isUtilizationMetric,
  metricUnitKind,
  parseSeriesKey,
  percentToRatio,
  ratioToPercent,
} from './metrics';

test('parseSeriesKey splits name and attributes', () => {
  const parsed = parseSeriesKey('system.filesystem.utilization{mountpoint=/data,state=used}');
  assert.equal(parsed.name, 'system.filesystem.utilization');
  assert.deepEqual(parsed.attributes, { mountpoint: '/data', state: 'used' });
});

test('parseSeriesKey handles bare legacy kinds', () => {
  const parsed = parseSeriesKey('cpu');
  assert.equal(parsed.name, 'cpu');
  assert.deepEqual(parsed.attributes, {});
});

test('parseSeriesKey keeps = inside attribute values out of keys', () => {
  const parsed = parseSeriesKey('system.network.io{device=eth0,direction=receive}');
  assert.deepEqual(parsed.attributes, { device: 'eth0', direction: 'receive' });
});

test('displayAttr prefers mountpoint over direction, device, and other attrs', () => {
  assert.equal(displayAttr({ device: 'sda1', mountpoint: '/data', state: 'used' }), '/data');
  assert.equal(displayAttr({ device: 'eth0', direction: 'receive' }), 'receive');
  assert.equal(displayAttr({ device: 'sda' }), 'sda');
  assert.equal(displayAttr({ state: 'used', status: 'running' }), 'running');
  assert.equal(displayAttr({ state: 'used' }), null);
  assert.equal(displayAttr({}), null);
});

test('formatSeriesLabel produces curated short labels with attribute', () => {
  assert.equal(
    formatSeriesLabel('system.filesystem.utilization{mountpoint=/data,state=used}'),
    'Filesystem (/data)'
  );
  assert.equal(formatSeriesLabel('system.cpu.utilization{state=used}'), 'CPU');
  assert.equal(formatSeriesLabel('cpu'), 'CPU');
  assert.equal(formatSeriesLabel('custom.metric{foo=bar}'), 'custom.metric (bar)');
  assert.equal(formatSeriesLabel(undefined), 'host metric');
});

test('formatAlertValue keeps legacy percent values as-is', () => {
  assert.equal(formatAlertValue('cpu', 92.5), '92.5%');
  assert.equal(formatAlertValue('swap', 80), '80%');
});

test('formatAlertValue converts native ratios to percent', () => {
  assert.equal(formatAlertValue('system.cpu.utilization{state=used}', 0.925), '92.5%');
  assert.equal(
    formatAlertValue('system.filesystem.utilization{mountpoint=/data,state=used}', 0.9),
    '90.0%'
  );
});

test('formatAlertValue formats bytes and seconds metrics natively', () => {
  assert.equal(formatAlertValue('system.memory.usage{state=used}', 2 * 1024 * 1024 * 1024), '2.0 GB');
  assert.equal(formatAlertValue('system.uptime', 90061), '1d 1h');
  assert.equal(formatAlertValue('system.processes.count{status=running}', 123), '123');
});

test('formatMetricValue formats by unit kind', () => {
  assert.equal(formatMetricValue(0.42, 'ratio'), '42.0%');
  assert.equal(formatMetricValue(1536, 'bytes'), '1.5 KB');
  assert.equal(formatMetricValue(3720, 'seconds'), '1h 2m');
  assert.equal(formatMetricValue(1.234567, 'count'), '1.23');
});

test('metricUnitKind uses curated map, utilization suffix, then OTLP unit', () => {
  assert.equal(metricUnitKind('system.memory.usage'), 'bytes');
  assert.equal(metricUnitKind('custom.pool.utilization'), 'ratio');
  assert.equal(metricUnitKind('custom.bytes', 'By'), 'bytes');
  assert.equal(metricUnitKind('custom.duration', 's'), 'seconds');
  assert.equal(metricUnitKind('custom.things', '1'), 'count');
});

test('ratio and percent conversions round-trip cleanly', () => {
  assert.equal(percentToRatio(90), 0.9);
  assert.equal(ratioToPercent(0.9), 90);
  assert.equal(ratioToPercent(percentToRatio(87.5)), 87.5);
});

test('isUtilizationMetric matches the server threshold rule', () => {
  assert.equal(isUtilizationMetric('system.cpu.utilization'), true);
  assert.equal(isUtilizationMetric('system.memory.usage'), false);
  assert.equal(isUtilizationMetric('utilization'), false);
});
