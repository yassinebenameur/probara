import assert from 'node:assert/strict';
import test from 'node:test';
import { IMPORT_SOURCES, importSourceFor } from './import-sources';
import {
  IMPORT_SCHEMA_BETTER_STACK,
  IMPORT_SCHEMA_GATUS,
  IMPORT_SCHEMA_PORTABLE,
  IMPORT_SCHEMA_UPTIME_KUMA,
  IMPORT_SCHEMA_UPTIMEROBOT,
} from './types/import';

test('every foreign schema has one source entry with an export hint', () => {
  for (const schema of [IMPORT_SCHEMA_UPTIME_KUMA, IMPORT_SCHEMA_BETTER_STACK, IMPORT_SCHEMA_UPTIMEROBOT, IMPORT_SCHEMA_GATUS]) {
    const matches = IMPORT_SOURCES.filter((source) => source.schema === schema);
    assert.equal(matches.length, 1, schema);
    assert.ok(matches[0].exportHint.length > 0, schema);
  }
});

test('the new sources explain their translation on the mapping step', () => {
  for (const schema of [IMPORT_SCHEMA_BETTER_STACK, IMPORT_SCHEMA_UPTIMEROBOT, IMPORT_SCHEMA_GATUS]) {
    const note = importSourceFor(schema)?.previewNote;
    assert.ok(note?.translated && note.notMigrated, schema);
  }
});

test('importSourceFor ignores the portable bundle and generic files', () => {
  assert.equal(importSourceFor(IMPORT_SCHEMA_PORTABLE), undefined);
  assert.equal(importSourceFor(undefined), undefined);
  assert.equal(importSourceFor(''), undefined);
});
