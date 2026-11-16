import type { CheckResult, HTTPFailureResponse, HTTPMetricsEnvelope } from './types';

// The redacted response a failed HTTP check stored with its result
// (metrics_data.http.failure_response, worker http_failure_capture.go).

/** The captured response of a failed or errored check, if this row carries it. */
export function failureResponseOf(result: Pick<CheckResult, 'status' | 'metrics_data'>): HTTPFailureResponse | null {
  if (result.status !== 'failure' && result.status !== 'error') return null;
  const capture = (result.metrics_data as HTTPMetricsEnvelope | undefined)?.http?.failure_response;
  return capture && typeof capture.status_line === 'string' ? capture : null;
}

/**
 * Whether a failed or errored check has a captured response: in the row, or
 * left out by a list read (fetch the result alone, getMonitorResult).
 */
export function hasFailureResponse(result: Pick<CheckResult, 'status' | 'metrics_data'>): boolean {
  if (failureResponseOf(result)) return true;
  if (result.status !== 'failure' && result.status !== 'error') return false;
  return (result.metrics_data as HTTPMetricsEnvelope | undefined)?.http?.failure_response_omitted === true;
}

/** `Name: value` lines, sorted by name as the worker stored them. */
export function failureResponseHeaderLines(capture: HTTPFailureResponse): string[] {
  return Object.entries(capture.headers ?? {})
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([name, value]) => `${name}: ${value}`);
}
