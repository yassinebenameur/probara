import type { StatusPageDomain } from './types';

/**
 * Display state of a status page custom domain. "failing" is a verified
 * domain whose recent checks fail: it is still served until the failure
 * tolerance runs out (see "Custom domains" in the status-pages docs).
 */
export type StatusPageDomainState = 'pending' | 'verified' | 'failing' | 'failed';

export function statusPageDomainState(domain: Pick<StatusPageDomain, 'status' | 'failing_since'>): StatusPageDomainState {
  if (domain.status === 'verified' && domain.failing_since) return 'failing';
  return domain.status;
}

export const STATUS_PAGE_DOMAIN_LABEL: Record<StatusPageDomainState, string> = {
  pending: 'Pending',
  verified: 'Verified',
  failing: 'Verified · failing checks',
  failed: 'Failed',
};

/** One line on what the domain's state means for visitors. */
export function statusPageDomainHint(state: StatusPageDomainState): string {
  switch (state) {
    case 'pending':
      return 'Not served yet. Create both records below; the domain is checked every few minutes, or now.';
    case 'verified':
      return 'Served: the page is live on this domain.';
    case 'failing':
      return 'Still served, but its records stopped checking out. It stops being served after a day of failed checks.';
    case 'failed':
      return 'Not served: its records failed every check for a day. It is served again once they pass.';
  }
}

/**
 * Trims and lower-cases a hostname typed into the form, dropping a pasted
 * scheme, path or trailing dot so "https://Status.Example.com/" adds
 * status.example.com. The API validates what is left.
 */
export function cleanHostnameInput(raw: string): string {
  let host = raw.trim();
  host = host.replace(/^[a-z][a-z0-9+.-]*:\/\//i, '');
  host = host.split(/[/?#]/)[0];
  return host.replace(/\.$/, '').toLowerCase();
}
