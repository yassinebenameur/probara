import assert from 'node:assert/strict';
import test from 'node:test';
import { failureResponseHeaderLines, failureResponseOf, hasFailureResponse } from './http-capture';
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

test('hasFailureResponse: a capture in the row, or the marker a list read leaves', () => {
  const r = (status: CheckResult['status'], metrics: unknown) =>
    ({ id: 'a', status, metrics_data: metrics, created_at: '' }) as unknown as CheckResult;
  assert.equal(hasFailureResponse(r('failure', { http: { failure_response: capture } })), true);
  assert.equal(hasFailureResponse(r('failure', { http: { failure_response_omitted: true } })), true);
  assert.equal(failureResponseOf(r('failure', { http: { failure_response_omitted: true } })), null);
  assert.equal(hasFailureResponse(r('success', { http: { failure_response_omitted: true } })), false);
  assert.equal(hasFailureResponse(r('error', { http: {} })), false);
});
