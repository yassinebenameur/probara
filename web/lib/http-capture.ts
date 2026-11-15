import type { CheckResult, HTTPFailureResponse, HTTPMetricsEnvelope } from './types';

// The redacted response a failed HTTP check stored with its result
// (metrics_data.http.failure_response, worker http_failure_capture.go).

/** The captured response of a failed or errored check, if any. */
export function failureResponseOf(result: Pick<CheckResult, 'status' | 'metrics_data'>): HTTPFailureResponse | null {
  if (result.status !== 'failure' && result.status !== 'error') return null;
  const capture = (result.metrics_data as HTTPMetricsEnvelope | undefined)?.http?.failure_response;
  return capture && typeof capture.status_line === 'string' ? capture : null;
}

/** `Name: value` lines, sorted by name as the worker stored them. */
export function failureResponseHeaderLines(capture: HTTPFailureResponse): string[] {
  return Object.entries(capture.headers ?? {})
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([name, value]) => `${name}: ${value}`);
}
