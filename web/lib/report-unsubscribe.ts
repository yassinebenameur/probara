// The unsubscribe link of a scheduled SLA report email opens
// /report-unsubscribe/<token>, which reads GET
// /api/v1/report-unsubscribe/<token> and unsubscribes with a POST on a button
// press. Never on load: mail scanners prefetch links. The token is the only
// credential, so the page works signed out. Mail clients with one-click
// unsubscribe POST to the API route directly (List-Unsubscribe-Post).

export type ReportUnsubscribeState = 'subscribed' | 'unsubscribed' | 'invalid';

export type ReportUnsubscribeResponse = {
  state: ReportUnsubscribeState;
  sla_name?: string;
  /** Masked: "o***@example.com". */
  recipient?: string;
};

const STATES: ReportUnsubscribeState[] = ['subscribed', 'unsubscribed', 'invalid'];

export function reportUnsubscribePath(token: string): string {
  return `/api/v1/report-unsubscribe/${encodeURIComponent(token)}`;
}

// The API answers an invalid link with a JSON state and 404. Anything else
// (a proxy error page, a 5xx) is a failure to retry, never a verdict.
export function parseReportUnsubscribeResponse(status: number, body: unknown): ReportUnsubscribeResponse | null {
  if (status >= 500 || typeof body !== 'object' || body === null) return null;
  const state = (body as { state?: unknown }).state;
  if (typeof state !== 'string' || !STATES.includes(state as ReportUnsubscribeState)) return null;
  return body as ReportUnsubscribeResponse;
}
