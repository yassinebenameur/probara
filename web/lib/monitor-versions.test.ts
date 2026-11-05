import assert from 'node:assert/strict';
import test from 'node:test';
import { formatVersionValue, summarizeChangedFields, versionActorLabel, versionFieldLabel } from './monitor-versions';

test('field labels name snapshot paths', () => {
  assert.equal(versionFieldLabel('config.url'), 'Config › url');
  assert.equal(versionFieldLabel('location_ids'), 'Locations');
  assert.equal(versionFieldLabel('config.headers.Accept'), 'Config › headers.Accept');
  assert.equal(versionFieldLabel('something_new'), 'something_new');
});

test('changed fields summarize by top-level field', () => {
  assert.equal(summarizeChangedFields(['config.url', 'config.method', 'name']), 'Config, Name');
  assert.equal(summarizeChangedFields(['name', 'tags', 'location_ids', 'member_ids', 'config.url']), 'Name, Tags, Locations +2 more');
  assert.equal(summarizeChangedFields([]), '');
});

test('values format with names for known IDs', () => {
  const names = { 'loc-1': 'Paris', 'ch-1': 'Ops Slack' };
  assert.equal(formatVersionValue(['loc-1', 'loc-2'], names), 'Paris, loc-2');
  assert.equal(formatVersionValue([]), '(none)');
  assert.equal(formatVersionValue(30), '30');
  assert.equal(formatVersionValue(false), 'false');
  assert.equal(formatVersionValue(undefined), '');
  assert.equal(
    formatVersionValue([{ channel_id: 'ch-1', delay_seconds: 300, notify_after_ack: true }], names),
    'Ops Slack after 300s (pages after ack)',
  );
  assert.equal(formatVersionValue({ a: 1 }), '{"a":1}');
});

test('actors fall back to the credential kind', () => {
  assert.equal(versionActorLabel({ actor_type: 'user', actor_label: 'alice' }), 'alice');
  assert.equal(versionActorLabel({ actor_type: 'api_key', actor_label: '' }), 'API key');
  assert.equal(versionActorLabel({ actor_type: 'system', actor_label: '' }), 'System');
});
