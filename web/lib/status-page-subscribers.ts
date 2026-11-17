import type { StatusPageSettings, StatusPageSubscriberState, StatusPageVisibility } from './types';
import { pluralize } from './format';
import { isGatedVisibility } from './status-page-access';

/**
 * Email subscribers as the operator UI presents them. The rules live in the
 * API (api/internal/services/pagesubscribers); these are display strings.
 */

/** Automatic outage publishing waits this long (fixed in the API). */
export const AUTO_PUBLISH_DELAY_MINUTES = 5;

export const SUBSCRIBER_STATE_LABELS: Record<StatusPageSubscriberState, string> = {
  pending: 'Awaiting confirmation',
  confirmed: 'Confirmed',
};

/** Whether the page can take subscribers at all, and why not. */
export function subscriptionAvailability(
  settings: StatusPageSettings | undefined,
  visibility: StatusPageVisibility | undefined
): { active: boolean; reason?: string } {
  if (!settings?.enable_email_subscriptions) {
    return { active: false, reason: 'Email subscriptions are off for this page.' };
  }
  if (isGatedVisibility(visibility)) {
    return {
      active: false,
      reason:
        'This page is password-protected or private: it shows no subscribe form and mails nobody, so its incident text never leaves the page.',
    };
  }
  return { active: true };
}

/**
 * The line the incident publish form shows for a page: who will be
 * emailed when the incident is published there. null when the page mails
 * nobody for a reason the subscriber panel already explains.
 */
export function publishNotice(
  settings: StatusPageSettings | undefined,
  visibility: StatusPageVisibility | undefined,
  confirmed: number | null
): string | null {
  if (!subscriptionAvailability(settings, visibility).active || confirmed === null) {
    return null;
  }
  if (confirmed === 0) {
    return 'No email subscribers yet: nobody is emailed.';
  }
  return `${pluralize(confirmed, 'email subscriber')} will be notified when you publish.`;
}
