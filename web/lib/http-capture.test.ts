import assert from 'node:assert/strict';
import test from 'node:test';
import { failureResponseHeaderLines, failureResponseOf } from './http-capture';
import type { CheckResult } from './types';

const capture = { status_line: 'HTTP/1.1 503 Service Unavailable', headers: { Server: 'edge', 'Content-Type': 'text/plain' }, body_excerpt: 'down' };

test('failureResponseOf: only a failed or errored check, only a well-formed capture', () => {
  const r = (status: CheckResult['status'], metrics: unknown) =>
    ({ id: 'a', status, metrics_data: metrics, created_at: '' }) as unknown as CheckResult;
  assert.deepEqual(failureResponseOf(r('failure', { http: { failure_response: capture } })), capture);
  assert.deepEqual(failureResponseOf(r('error', { http: { failure_response: capture } })), capture);
  assert.equal(failureResponseOf(r('success', { http: { failure_response: capture } })), null);
  assert.equal(failureResponseOf(r('failure', { http: {} })), null);
  assert.equal(failureResponseOf(r('failure', { http: { failure_response: { headers: {} } } })), null);
  assert.equal(failureResponseOf(r('failure', undefined)), null);
});

test('failureResponseHeaderLines: sorted Name: value lines', () => {
  assert.deepEqual(failureResponseHeaderLines(capture), ['Content-Type: text/plain', 'Server: edge']);
  assert.deepEqual(failureResponseHeaderLines({ status_line: 'HTTP/1.1 500' }), []);
});
