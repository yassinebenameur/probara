// A report email's unsubscribe link opens /report-unsubscribe#<token>. As
// with acknowledge links (lib/alert-action.ts), the token rides in the URL
// fragment, which the browser never sends to a server, and the page hands it
// to the API in a request header. Loading the page only reads (GET);
// unsubscribing is the button's POST, because mail scanners and link
// previewers fetch every link they see. Mail clients' one-click unsubscribe
// (RFC 8058) POSTs to the API directly and never reaches this page. The
// token is the only credential, so the page works signed out, and recipients
// are often outside the workspace.

export { ACTION_TOKEN_HEADER, readActionToken, takeActionToken } from './alert-action';

export const REPORT_UNSUBSCRIBE_PATH = '/api/v1/report-unsubscribe';

export type ReportUnsubscribeState = 'subscribed' | 'unsubscribed' | 'expired' | 'invalid';

export type ReportUnsubscribeResponse = {
  state: ReportUnsubscribeState;
  /** The SLA the emails are about; only on a valid link. */
  sla_name?: string;
};

const STATES: readonly ReportUnsubscribeState[] = ['subscribed', 'unsubscribed', 'expired', 'invalid'];

/**
 * The API answers every verdict with a JSON state (404 invalid, 410
 * expired). Anything else (a proxy error page, a 5xx) is a failure the page
 * offers to retry, never a verdict on the link.
 */
export function parseReportUnsubscribeResponse(status: number, body: unknown): ReportUnsubscribeResponse | null {
  if (status >= 500 || typeof body !== 'object' || body === null) return null;
  const state = (body as { state?: unknown }).state;
  if (typeof state !== 'string' || !STATES.includes(state as ReportUnsubscribeState)) return null;
  const name = (body as { sla_name?: unknown }).sla_name;
  return { state: state as ReportUnsubscribeState, sla_name: typeof name === 'string' && name ? name : undefined };
}

export type ReportUnsubscribeCopy = {
  tone: 'success' | 'info' | 'warning' | 'danger';
  title: string;
  text: string;
};

/** What the page says once there is nothing left to press (null while subscribed). */
export function describeReportUnsubscribeState(state: ReportUnsubscribeState): ReportUnsubscribeCopy | null {
  switch (state) {
    case 'subscribed':
      return null;
    case 'unsubscribed':
      return {
        tone: 'success',
        title: 'Unsubscribed',
        text: 'You will not receive this report by email any more. To get it again, ask whoever manages the report to add you back.',
      };
    case 'expired':
      return {
        tone: 'warning',
        title: 'Link expired',
        text: 'Unsubscribe links work for a year. Use the link in a more recent report email, or ask whoever manages the report to remove you.',
      };
    case 'invalid':
      return {
        tone: 'danger',
        title: 'Link not valid',
        text: 'This link is incomplete or no longer valid. If the report schedule was deleted, no more emails are sent from it.',
      };
  }
}
