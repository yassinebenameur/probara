import assert from 'node:assert/strict';
import test from 'node:test';
import { describeUserAgent, sessionMethodLabel } from './sessions';

test('describes common browsers and systems', () => {
  assert.equal(
    describeUserAgent(
      'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36'
    ),
    'Chrome on macOS'
  );
  assert.equal(
    describeUserAgent('Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:132.0) Gecko/20100101 Firefox/132.0'),
    'Firefox on Windows'
  );
  assert.equal(
    describeUserAgent(
      'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36 Edg/130.0.0.0'
    ),
    'Edge on Windows'
  );
  assert.equal(
    describeUserAgent(
      'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1'
    ),
    'Safari on iOS'
  );
});

test('falls back for missing or unrecognized agents', () => {
  assert.equal(describeUserAgent(''), 'Unknown device');
  assert.equal(describeUserAgent(undefined), 'Unknown device');
  assert.equal(describeUserAgent('curl/8.4.0'), 'curl');
  assert.equal(describeUserAgent('custom-agent'), 'custom-agent');
  assert.equal(describeUserAgent('x'.repeat(60)).length, 48);
});

test('labels the sign-in method', () => {
  assert.equal(sessionMethodLabel('oidc'), 'SSO');
  assert.equal(sessionMethodLabel('password'), 'Password');
});
