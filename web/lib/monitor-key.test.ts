import assert from 'node:assert/strict';
import test from 'node:test';
import { monitorKeyError, monitorKeyPatch } from './monitor-key';

test('key format matches the API', () => {
  for (const key of ['', 'web', 'db-primary', 'eu.api_v2', '0day', 'a'.repeat(64)]) {
    assert.equal(monitorKeyError(key), undefined, key);
  }
  for (const key of ['Web', '-web', '.web', 'has space', 'a/b', 'a'.repeat(65)]) {
    assert.ok(monitorKeyError(key), key);
  }
});

test('create sends an entered key only', () => {
  assert.deepEqual(monitorKeyPatch(undefined, '', false), {});
  assert.deepEqual(monitorKeyPatch(undefined, ' web ', false), { key: 'web' });
});

test('edit sends a change only, and an empty value removes the key', () => {
  assert.deepEqual(monitorKeyPatch('web', 'web', true), {});
  assert.deepEqual(monitorKeyPatch('web', 'site', true), { key: 'site' });
  assert.deepEqual(monitorKeyPatch('web', '', true), { key: '' });
  assert.deepEqual(monitorKeyPatch(undefined, '', true), {});
});
