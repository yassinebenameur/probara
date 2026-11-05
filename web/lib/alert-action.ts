// The signed acknowledge link a notification carries opens
// /alert-action/<token>, which reads GET /api/v1/alert-actions/<token> and
// acknowledges with a POST on a button press. Never on load: mail scanners
// and chat unfurlers prefetch links. The token is the only credential, so
// the page works signed out.

export type AlertActionState = 'pending' | 'acknowledged' | 'already' | 'resolved' | 'expired' | 'invalid';

export type AlertActionResponse = {
  state: AlertActionState;
  alert?: {
    subject: string;
    kind: string;
    status: string;
    triggered_at: string;
    acknowledged_at?: string;
  };
  expires_at?: string;
};

const STATES: AlertActionState[] = ['pending', 'acknowledged', 'already', 'resolved', 'expired', 'invalid'];

export function alertActionPath(token: string): string {
  return `/api/v1/alert-actions/${encodeURIComponent(token)}`;
}

// The API answers every refusal with a JSON state (404 invalid, 410
// expired, 409 already/resolved). Anything else — a proxy error page, a 5xx
// — is a failure the page offers to retry, never a verdict on the link.
export function parseAlertActionResponse(status: number, body: unknown): AlertActionResponse | null {
  if (status >= 500 || typeof body !== 'object' || body === null) return null;
  const state = (body as { state?: unknown }).state;
  if (typeof state !== 'string' || !STATES.includes(state as AlertActionState)) return null;
  return body as AlertActionResponse;
}

export type AlertActionCopy = {
  tone: 'success' | 'info' | 'warning' | 'danger';
  title: string;
  text: string;
};

// What the page says once there is nothing left to press.
export function describeAlertActionState(state: AlertActionState): AlertActionCopy | null {
  switch (state) {
    case 'pending':
      return null;
    case 'acknowledged':
      return {
        tone: 'success',
        title: 'Acknowledged',
        text: 'You own this alert now. Reminders and escalation tiers stop unless the workspace keeps paging after an acknowledgement.',
      };
    case 'already':
      return { tone: 'info', title: 'Already acknowledged', text: 'Someone acknowledged this alert before you.' };
    case 'resolved':
      return { tone: 'info', title: 'Already resolved', text: 'This alert has closed, so there is nothing to acknowledge.' };
    case 'expired':
      return {
        tone: 'warning',
        title: 'Link expired',
        text: 'Acknowledge links work for 72 hours. Sign in to acknowledge the alert from the Alerts page.',
      };
    case 'invalid':
      return {
        tone: 'danger',
        title: 'Link not valid',
        text: 'This link is malformed, or its alert, channel or workspace no longer exists. Sign in to find the alert.',
      };
  }
}
