import type { CheckResult, HTTPFailureResponse, HTTPMetricsEnvelope } from './types';

/** The redacted response a failed HTTP check captured (capture_failure_response), if any. */
export function failureResponseOf(result: Pick<CheckResult, 'status' | 'metrics_data'>): HTTPFailureResponse | null {
  if (result.status !== 'failure' && result.status !== 'error') return null;
  const capture = (result.metrics_data as HTTPMetricsEnvelope | undefined)?.http?.failure_response;
  return capture && typeof capture.status_line === 'string' ? capture : null;
}

/** Header lines in the order the worker stored them (sorted by name). */
export function failureResponseHeaderLines(capture: HTTPFailureResponse): string[] {
  return Object.entries(capture.headers ?? {})
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([name, value]) => `${name}: ${value}`);
}
