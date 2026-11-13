import assert from 'node:assert/strict';
import test from 'node:test';
import { ApiRequestError, apiErrorFromBody, errorCode, errorMessage } from './errors';

test('an API error is an Error that keeps the server code and message', () => {
  const err = apiErrorFromBody({ error: 'conflict', message: 'Name already in use' });
  assert.ok(err instanceof Error);
  assert.ok(err instanceof ApiRequestError);
  assert.equal(err.message, 'Name already in use');
  assert.equal(err.error, 'conflict');
  // The pattern most call sites use must surface the server message.
  const shown = err instanceof Error ? err.message : 'Failed to save';
  assert.equal(shown, 'Name already in use');
});

test('error bodies without a code or message still produce a usable error', () => {
  assert.deepEqual(
    [apiErrorFromBody({ error: 'forbidden' }).message, apiErrorFromBody({ error: 'forbidden' }).error],
    ['forbidden', 'forbidden'],
  );
  const bare = apiErrorFromBody({ message: 'Nope' });
  assert.deepEqual([bare.error, bare.message], ['unknown_error', 'Nope']);
  const empty = apiErrorFromBody(null);
  assert.deepEqual([empty.error, empty.message], ['unknown_error', 'An error occurred']);
});

test('errorMessage reads Errors and plain objects, and falls back otherwise', () => {
  assert.equal(errorMessage(new Error('boom'), 'fallback'), 'boom');
  assert.equal(errorMessage({ error: 'x', message: 'from object' }, 'fallback'), 'from object');
  assert.equal(errorMessage(new Error(''), 'fallback'), 'fallback');
  assert.equal(errorMessage({ message: 42 }, 'fallback'), 'fallback');
  assert.equal(errorMessage('a string', 'fallback'), 'fallback');
  assert.equal(errorMessage(undefined, 'fallback'), 'fallback');
  assert.equal(errorMessage(null), 'The request failed. Please retry.');
});

test('errorCode returns the server code only when one is present', () => {
  assert.equal(errorCode(apiErrorFromBody({ error: 'not_found', message: 'gone' })), 'not_found');
  assert.equal(errorCode({ error: 'not_found' }), 'not_found');
  assert.equal(errorCode(new Error('boom')), undefined);
  assert.equal(errorCode(null), undefined);
});
