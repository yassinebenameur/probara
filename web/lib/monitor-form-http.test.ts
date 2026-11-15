import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';
import { isSensitiveHeaderName } from './monitor-form/http';

// The sensitive-header rule lives twice: isSensitiveHeaderName here (the
// form locks such rows secret) and shared/secrets IsSensitiveHeaderName (the
// API moves them into the encrypted secret_headers on write). Both suites
// read this one fixture, so the twins cannot drift. npm test runs from web/.
const FIXTURE = join(process.cwd(), '..', 'shared', 'secrets', 'testdata', 'sensitive_header_names.json');

test('isSensitiveHeaderName matches the shared fixture the Go twin also reads', () => {
  const fixture = JSON.parse(readFileSync(FIXTURE, 'utf8')) as { sensitive: string[]; plain: string[] };
  assert.ok(fixture.sensitive.length > 0 && fixture.plain.length > 0);
  for (const name of fixture.sensitive) {
    assert.equal(isSensitiveHeaderName(name), true, `${JSON.stringify(name)} should be sensitive`);
  }
  for (const name of fixture.plain) {
    assert.equal(isSensitiveHeaderName(name), false, `${JSON.stringify(name)} should be plain`);
  }
});
