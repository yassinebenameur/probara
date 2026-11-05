import assert from 'node:assert/strict';
import test from 'node:test';
import { failureResponseHeaderLines, failureResponseOf } from './http-capture';
import type { CheckResult } from './types';

const capture = {
  status_line: 'HTTP/1.1 503 Service Unavailable',
  headers: { Server: 'edge', 'Set-Cookie': '[REDACTED]' },
  body_excerpt: '{"error":"down"}',
};

test('failureResponseOf reads the capture of a failed check only', () => {
  const failed = { status: 'failure', metrics_data: { http: { failure_response: capture } } } as unknown as CheckResult;
  assert.equal(failureResponseOf(failed)?.status_line, 'HTTP/1.1 503 Service Unavailable');
  assert.equal(failureResponseOf({ ...failed, status: 'success' }), null);
  assert.equal(failureResponseOf({ status: 'error', metrics_data: { http: {} } } as unknown as CheckResult), null);
  assert.equal(failureResponseOf({ status: 'error' } as unknown as CheckResult), null);
});

test('failureResponseHeaderLines renders name: value, sorted', () => {
  assert.deepEqual(failureResponseHeaderLines(capture), ['Server: edge', 'Set-Cookie: [REDACTED]']);
  assert.deepEqual(failureResponseHeaderLines({ status_line: 'HTTP/1.1 500' }), []);
});
