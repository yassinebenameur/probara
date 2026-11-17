import assert from 'node:assert/strict';
import test from 'node:test';
import {
  absoluteLink,
  groupSecret,
  linkErrorMessage,
  looksLikeTotpCode,
  readLinkToken,
  recoveryCodesFile,
} from './account-links';
import { sessionSecondFactorLabel } from './account';
import { passwordProblem } from './password';
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
  assert.match(passwordProblem('short', 'short'), /12 to 128/);
  assert.match(passwordProblem('long-enough-pass', 'long-enough-PASS'), /do not match/);
  assert.equal(passwordProblem('long-enough-pass', 'long-enough-pass'), '');
});
