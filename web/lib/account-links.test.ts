import assert from 'node:assert/strict';
import test from 'node:test';
import {
  absoluteLink,
  groupSecret,
  linkErrorMessage,
  looksLikeTotpCode,
  readLinkToken,
  recoveryCodesFile,
  takeLinkToken,
} from './account-links';
import { sessionSecondFactorLabel } from './account';
import { passwordBytes, passwordProblem } from './password';
import { isPublicRoute } from './public-routes';

test('link tokens come from the fragment only when token-shaped', () => {
  const token = 'Abc_def-0123456789ABCDEFGHIJKLMNOPQRSTUVWXY';
  assert.equal(readLinkToken(`#${token}`), token);
  assert.equal(readLinkToken(''), '');
  assert.equal(readLinkToken('#short'), '');
  assert.equal(readLinkToken('#has spaces in it, not a token'), '');
});

test('absoluteLink prefixes the UI origin to a path only', () => {
  assert.equal(absoluteLink('/accept-invite#t', 'https://app.example/'), 'https://app.example/accept-invite#t');
  assert.equal(absoluteLink('https://probara.example/reset-password#t', 'https://other'), 'https://probara.example/reset-password#t');
});

test('codes and secrets', () => {
  assert.equal(looksLikeTotpCode('123 456'), true);
  assert.equal(looksLikeTotpCode('k7m2q-x9p4t'), false);
  assert.equal(groupSecret('JBSWY3DPEHPK3PXP'), 'JBSW Y3DP EHPK 3PXP');
  const file = recoveryCodesFile('alice', ['aaaaa-bbbbb', 'ccccc-ddddd'], new Date('2026-11-17T10:00:00Z'));
  assert.match(file, /^Probara recovery codes for alice\nGenerated 2026-11-17/);
  assert.ok(file.includes('\naaaaa-bbbbb\nccccc-ddddd\n'));
});

test('link error messages', () => {
  assert.match(linkErrorMessage('invalid_token', 'x'), /invalid or has expired/);
  assert.equal(linkErrorMessage(undefined, 'fallback'), 'fallback');
});

test('session second-factor labels', () => {
  assert.equal(sessionSecondFactorLabel('totp'), 'Authenticator code');
  assert.equal(sessionSecondFactorLabel('recovery_code'), 'Recovery code');
  assert.equal(sessionSecondFactorLabel(undefined), '');
});

test('the link pages sit outside the authenticated shell', () => {
  for (const path of ['/login', '/forgot-password', '/reset-password', '/accept-invite', '/confirm-email', '/alert-action']) {
    assert.equal(isPublicRoute(path), true, path);
  }
  assert.equal(isPublicRoute('/account'), false);
  assert.equal(isPublicRoute(null), false);
});

test('passwordProblem mirrors the API rule', () => {
  assert.equal(passwordProblem('', ''), 'Enter a new password.');
  assert.match(passwordProblem('short', 'short'), /at least 12/);
  // Bytes, not UTF-16 units: 30 CJK characters are 90 bytes, 24 are 72.
  const cjk30 = '密'.repeat(30);
  assert.match(passwordProblem(cjk30, cjk30), /too long/);
  const cjk24 = '密'.repeat(24);
  assert.equal(passwordProblem(cjk24, cjk24), '');
  assert.match(passwordProblem('a'.repeat(73), 'a'.repeat(73)), /too long/);
  assert.equal(passwordBytes('é'), 2);
  assert.match(passwordProblem('long-enough-pass', 'long-enough-PASS'), /do not match/);
  assert.equal(passwordProblem('long-enough-pass', 'long-enough-pass'), '');
});

test('takeLinkToken drops the fragment from history and survives a second mount', () => {
  const token = 'Abc_def-0123456789ABCDEFGHIJKLMNOPQRSTUVWXY';
  const replaced: string[] = [];
  const loc = { hash: `#${token}`, pathname: '/reset-password', search: '' };
  const g = globalThis as unknown as { window?: unknown };
  g.window = {
    location: loc,
    history: {
      state: { kept: true },
      replaceState: (_state: unknown, _title: string, url: string) => {
        replaced.push(url);
        loc.hash = '';
      },
    },
  };
  try {
    assert.equal(takeLinkToken(), token);
    assert.deepEqual(replaced, ['/reset-password']);
    // React's development double mount reads again with the fragment gone.
    assert.equal(takeLinkToken(), token);
    assert.equal(replaced.length, 1);
  } finally {
    delete g.window;
  }
});
