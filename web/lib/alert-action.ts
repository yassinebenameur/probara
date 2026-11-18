// A notification's signed acknowledge link opens /alert-action#<token>. The
// token rides in the URL fragment, which the browser never sends to a server,
// so it stays out of access logs; the page hands it to the API in a request
// header. Loading the page only reads (GET); acknowledging is the button's
// POST, never automatic, because mail scanners and link previewers fetch every
// link they see. The token is the only credential, so the page works signed
// out.

export const ALERT_ACTION_PATH = '/api/v1/alert-actions/acknowledge';
export const ACTION_TOKEN_HEADER = 'X-Action-Token';

export type AlertActionState = 'pending' | 'acknowledged' | 'already' | 'resolved' | 'expired' | 'invalid';

export type AlertActionResponse = {
  state: AlertActionState;
  alert?: {
    subject: string;
    label: string;
    status: string;
    triggered_at: string;
    acknowledged_at?: string;
    /** `/alerts?alert=<id>&tenant=<id>`: the alert on the Alerts page (lib/alert-links.ts). */
    open_path?: string;
  };
  expires_at?: string;
};

const STATES: readonly AlertActionState[] = ['pending', 'acknowledged', 'already', 'resolved', 'expired', 'invalid'];

/** The token in a location hash ("#abc"), or '' when there is none or it is not token-shaped. */
export function readActionToken(hash: string): string {
  const token = hash.replace(/^#/, '').trim();
  return /^[A-Za-z0-9_-]{16,256}$/.test(token) ? token : '';
}

// The token last taken from this page's fragment. React runs a mount effect
// twice in development, and the second run finds the fragment gone.
let taken: { path: string; token: string } | null = null;

/**
 * Reads the token from the page's URL fragment, then removes the fragment
 * from the current history entry, so a link not yet used (an invite, a
 * reset) is not left in browser history for the next person at the machine.
 * Call it once on mount, in the browser.
 */
export function takeActionToken(): string {
  const { hash, pathname, search } = window.location;
  if (!hash && taken?.path === pathname) return taken.token;
  const token = readActionToken(hash);
  taken = { path: pathname, token };
  if (hash) {
    window.history.replaceState(window.history.state, '', pathname + search);
  }
  return token;
}

/**
 * The API answers every verdict with a JSON state (404 invalid, 410 expired,
 * 409 resolved on POST). Anything else (a proxy error page, a 5xx) is a
 * failure the page offers to retry, never a verdict on the link.
 */
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

/** What the page says once there is nothing left to press. */
export function describeAlertActionState(state: AlertActionState): AlertActionCopy | null {
  switch (state) {
    case 'pending':
      return null;
    case 'acknowledged':
      return {
        tone: 'success',
        title: 'Acknowledged',
        text: 'Your team can see someone is on it. The alert stays open until the monitor recovers or someone resolves it.',
      };
    case 'already':
      return { tone: 'info', title: 'Already acknowledged', text: 'Someone acknowledged this alert already. There is nothing more to do here.' };
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
        text: 'This link is incomplete or no longer valid. Sign in to find the alert on the Alerts page.',
      };
  }
}

/**
 * Where "Open in Probara" goes: the alert on the Alerts page when the API
 * named it, else the Alerts page. Only an `/alerts?` path is followed, so a
 * response can never send the visitor off-site.
 */
export function openInProbaraHref(data: AlertActionResponse | null): string {
  const path = data?.alert?.open_path ?? '';
  return path.startsWith('/alerts?') ? path : '/alerts';
}

/** The alert's own status as the page shows it. */
export function alertStatusLabel(status: string): string {
  switch (status) {
    case 'active':
      return 'Active';
    case 'acknowledged':
      return 'Acknowledged';
    case 'resolved':
      return 'Resolved';
    default:
      return status;
  }
}
