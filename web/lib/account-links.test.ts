import assert from 'node:assert/strict';
import test from 'node:test';
import { isBareRoute, readLinkToken } from './account-links';

test('reads the token from the fragment', () => {
  assert.equal(readLinkToken('#token=abc_-123'), 'abc_-123');
  assert.equal(readLinkToken('token=xyz'), 'xyz');
  assert.equal(readLinkToken('#other=1&token=t'), 't');
  assert.equal(readLinkToken(''), '');
  assert.equal(readLinkToken('#'), '');
});

test('account links and sign-in render bare', () => {
  for (const path of ['/login', '/accept-invite', '/reset-password', '/confirm-email', '/alert-action/x', '/connect', '/oauth/consent']) {
    assert.equal(isBareRoute(path), true, path);
  }
  for (const path of ['/', '/account', '/users/1', '/alert-actions']) {
    assert.equal(isBareRoute(path), false, path);
  }
});
