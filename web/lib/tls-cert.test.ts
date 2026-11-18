import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';
import { commonName, tlsExpiryCertificate, tlsExpiryCondition } from './tls-cert';

// The Go reader (notifications.ParseTLSExpiryError) tests the same fixture,
// so the alert row, the toast and the notification name the same
// certificate. npm test runs from web/.
const FIXTURE = join(process.cwd(), '..', 'shared', 'notifications', 'testdata', 'tls_expiry_errors.json');

test('tlsExpiryCertificate matches the shared fixture the Go twin also reads', () => {
  const fixture = JSON.parse(readFileSync(FIXTURE, 'utf8')) as {
    cases: { text: string; ok: boolean; intermediate?: boolean; subject?: string; common_name?: string; not_after?: string }[];
  };
  assert.ok(fixture.cases.length > 3);
  for (const c of fixture.cases) {
    const cert = tlsExpiryCertificate(c.text);
    if (!c.ok) {
      assert.equal(cert, null, c.text);
      continue;
    }
    assert.deepEqual(cert, { intermediate: c.intermediate, subject: c.subject, notAfter: c.not_after }, c.text);
    assert.equal(commonName(cert!.subject), c.common_name, c.text);
  }
});

test('tlsExpiryCondition names an intermediate, never the leaf', () => {
  const inter = 'tls: intermediate certificate CN=R3,O=Example expires on 2026-12-01 (in 9d, < 14d)';
  assert.equal(tlsExpiryCondition(inter, 9), 'intermediate certificate (R3) expires in 9d');
  assert.equal(tlsExpiryCondition('tls: intermediate certificate CN=R3 expired on 2026-11-01 (2d ago, threshold 14d)', -2), 'intermediate certificate (R3) has expired');
  assert.equal(tlsExpiryCondition('tls: certificate CN=a.example expires on 2026-12-01 (in 9d, < 14d)', 9), 'certificate expires in 9d');
  assert.equal(tlsExpiryCondition('tls: expires in 9d (< 14d)', 9), 'certificate expires in 9d');
  assert.equal(tlsExpiryCondition(undefined, undefined), 'certificate is about to expire');
});
