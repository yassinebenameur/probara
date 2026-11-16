import assert from 'node:assert/strict';
import test from 'node:test';
import {
  droppedReferenceLabel,
  formatVersionValue,
  secretChangeText,
  summarizeVersion,
  versionActorLabel,
  versionFieldLabel,
} from './monitor-versions';

test('field labels name snapshot paths', () => {
  assert.equal(versionFieldLabel('config.url'), 'Config › url');
  assert.equal(versionFieldLabel('location_ids'), 'Locations');
  assert.equal(versionFieldLabel('config.secret_headers.Authorization'), 'Config › secret_headers.Authorization');
  assert.equal(versionFieldLabel('something_new'), 'something_new');
});

test('versions summarize by top-level field, secrets by count', () => {
  const base = { source: 'ui' as const, changed_secret_fields: [] as string[], version: 3 };
  assert.equal(summarizeVersion({ ...base, changed_fields: ['config.url', 'config.method', 'name'] }), 'Config, Name');
  assert.equal(
    summarizeVersion({ ...base, changed_fields: ['name', 'tags', 'location_ids', 'member_ids', 'config.url'] }),
    'Name, Tags, Locations +2 more',
  );
  assert.equal(summarizeVersion({ ...base, changed_fields: [], changed_secret_fields: ['password'] }), '1 secret changed');
  assert.equal(
    summarizeVersion({ ...base, changed_fields: ['name'], changed_secret_fields: ['a', 'b'] }),
    'Name · 2 secrets',
  );
  assert.equal(summarizeVersion({ ...base, changed_fields: [], version: 1 }), 'First recorded version');
  assert.equal(
    summarizeVersion({ ...base, source: 'restore', restored_from: 2, changed_fields: ['name'] }),
    'Restored version 2',
  );
});

test('values format with names for known IDs', () => {
  const names = { 'loc-1': 'Paris', 'ch-1': 'Ops Slack' };
  assert.equal(formatVersionValue(['loc-1', 'loc-2'], names), 'Paris, loc-2');
  assert.equal(formatVersionValue([]), '(none)');
  assert.equal(formatVersionValue(30), '30');
  assert.equal(formatVersionValue(false), 'false');
  assert.equal(formatVersionValue(undefined), '');
  assert.equal(formatVersionValue([{ channel_id: 'ch-1', delay_seconds: 300 }], names), 'Ops Slack after 300s');
  assert.equal(formatVersionValue({ a: 1 }), '{"a":1}');
});

test('secret changes read as words, never values', () => {
  assert.deepEqual(secretChangeText({ path: 'config.password', secret: true, after: '***' }), {
    before: 'not set',
    after: 'set',
  });
  assert.deepEqual(secretChangeText({ path: 'config.password', secret: true, before: '***', after: '***' }), {
    before: 'set',
    after: 'changed',
  });
  assert.deepEqual(secretChangeText({ path: 'config.password', secret: true, before: '***' }), {
    before: 'set',
    after: 'not set',
  });
});

test('actors fall back to the credential kind', () => {
  assert.equal(versionActorLabel({ actor_type: 'user', actor_label: 'alice' }), 'alice');
  assert.equal(versionActorLabel({ actor_type: 'api_key', actor_label: '' }), 'API key');
  assert.equal(versionActorLabel({ actor_type: 'system', actor_label: '' }), 'System');
});

test('dropped references name what was left out', () => {
  assert.equal(droppedReferenceLabel('member:m-1', { 'm-1': 'API' }), 'member API');
  assert.equal(droppedReferenceLabel('channel:c-9'), 'channel c-9');
  assert.equal(droppedReferenceLabel('dependency_cycle:m-1', { 'm-1': 'API' }), 'dependency (would form a cycle) API');
});

test('a baseline reads as the configuration before the first change', () => {
  assert.equal(
    summarizeVersion({ source: 'baseline', changed_fields: [], changed_secret_fields: [], version: 1 }),
    'Configuration before the first recorded change',
  );
});
