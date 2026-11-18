import type {
  StatusPageNotificationOutcome,
  StatusPageSettings,
  StatusPageSubscriberState,
  StatusPageVisibility,
} from './types';
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

const NOTICE_KIND_LABELS: Record<string, string> = {
  incident_published: 'Incident published',
  incident_updated: 'Incident update',
  incident_resolved: 'Incident resolved',
  maintenance_scheduled: 'Maintenance scheduled',
  maintenance_started: 'Maintenance started',
  maintenance_cancelled: 'Maintenance cancelled',
};

/** A notice's name in the outcomes list: its subject, else its kind. */
export function noticeLabel(o: Pick<StatusPageNotificationOutcome, 'kind' | 'subject'>): string {
  return o.subject?.trim() || NOTICE_KIND_LABELS[o.kind] || o.kind;
}

/**
 * What a notice came to, in one line: why nobody was mailed, or how its
 * deliveries ended. `problem` flags mail that was lost or may have been.
 */
export function noticeOutcome(o: StatusPageNotificationOutcome): { text: string; problem: boolean } {
  if (o.status === 'pending') return { text: 'Waiting to be sent', problem: false };
  if (o.status === 'skipped') {
    return { text: `Not emailed: ${o.skip_reason || 'skipped'}`, problem: !/takes no email subscriptions/.test(o.skip_reason ?? '') };
  }
  if (o.recipients === 0) return { text: 'No confirmed subscribers to email', problem: false };
  const parts = [`${o.sent} sent`];
  if (o.waiting) parts.push(`${o.waiting} waiting`);
  if (o.failed) parts.push(`${o.failed} failed`);
  if (o.uncertain) parts.push(`${o.uncertain} uncertain`);
  if (o.skipped) parts.push(`${o.skipped} skipped`);
  return { text: `${parts.join(' · ')} of ${o.recipients}`, problem: o.failed > 0 || o.uncertain > 0 };
}
