// Wording for the notification delivery history (an alert's Notifications
// section on /alerts, the channel page's Recent deliveries). The outcome tone
// lives in lib/status-tones.ts (deliveryOutcomeTone); times go through
// lib/format.ts.

import type { DeliveryOutcome, NotificationDelivery } from './types';

const OUTCOME_LABEL: Record<string, string> = {
  delivered: 'Delivered',
  failed: 'Failed, retrying',
  permanent_failure: 'Rejected',
  dropped: 'Not sent',
};

/** The pill text. A transient failure the worker stopped retrying says so. */
export function deliveryOutcomeLabel(d: Pick<NotificationDelivery, 'outcome' | 'reason'>): string {
  if (d.outcome === 'failed' && d.reason === 'retries_exhausted') return 'Failed, gave up';
  return OUTCOME_LABEL[d.outcome] ?? d.outcome;
}

interface ReasonCopy {
  label: string;
  hint: string;
}

// Every reason a row can carry (shared/notifications/deliverylog Reason*).
// Drops are worded as what happened, never as an error.
const REASON: Record<string, ReasonCopy> = {
  resolved: {
    label: 'Alert resolved first',
    hint: 'Queued before the alert resolved. Sending it afterwards could reopen the alert at the provider, so it was dropped; the recovery notice went out instead.',
  },
  unacknowledged: {
    label: 'Unacknowledged first',
    hint: 'An acknowledgement notice queued before the alert was unacknowledged. A later acknowledgement is sent again.',
  },
  muted: {
    label: 'Held by acknowledgement or snooze',
    hint: 'A page or reminder queued before the alert was acknowledged or snoozed. A page that had reached no one goes out again once the mute lifts.',
  },
  channel_inactive: {
    label: 'Channel deactivated',
    hint: 'The channel was switched off after the notification was queued.',
  },
  channel_changed: {
    label: 'Channel type changed',
    hint: 'The channel was changed to another type after the notification was queued.',
  },
  egress_blocked: {
    label: 'Destination blocked',
    hint: 'The notification egress policy refused the address (private ranges are blocked unless listed in NOTIFICATION_ALLOWED_CIDRS). Not retried.',
  },
  invalid_config: {
    label: 'Unreadable channel config',
    hint: 'The stored channel configuration could not be read. Save the channel again. Not retried.',
  },
  plugin_missing: {
    label: 'Channel type unavailable',
    hint: 'The delivery worker that picked this up has no plugin for the channel type. Retried, in case another worker has it.',
  },
  retries_exhausted: {
    label: 'Out of retries',
    hint: 'The delivery worker’s last attempt failed; nothing retries it.',
  },
};

/** Short reason text, or null when the row has none. Unknown reasons show as sent. */
export function deliveryReasonLabel(reason: string | null | undefined): string | null {
  if (!reason) return null;
  return REASON[reason]?.label ?? reason.replace(/_/g, ' ');
}

/** The longer explanation behind a reason (tooltip), or undefined. */
export function deliveryReasonHint(reason: string | null | undefined): string | undefined {
  return reason ? REASON[reason]?.hint : undefined;
}

const EVENT_LABEL: Record<string, string> = {
  created: 'Page',
  reminder: 'Reminder',
  resolved: 'Recovery',
  acknowledged: 'Acknowledgement',
  suspected_cause: 'Suspected cause',
};

/** What was being delivered; a test send says so whatever event it carried. */
export function deliveryEventLabel(d: Pick<NotificationDelivery, 'event_type' | 'test'>): string {
  if (d.test) return 'Test notification';
  return EVENT_LABEL[d.event_type] ?? d.event_type.replace(/_/g, ' ').replace(/^\w/, (c) => c.toUpperCase());
}

/** Who sent it: the alerter in its own process, or a delivery worker after queueing. */
export function deliveryPathLabel(d: Pick<NotificationDelivery, 'path' | 'test'>): string {
  if (d.test) return 'Sent from the API';
  return d.path === 'async' ? 'Delivery worker' : 'Alerter';
}

/** What the alert was about, for the per-channel list: its monitor, else its kind. */
export function deliveryAlertSubject(d: Pick<NotificationDelivery, 'monitor_name' | 'alert_kind' | 'test'>): string {
  if (d.test) return 'Test notification';
  if (d.monitor_name) return d.monitor_name;
  return `${String(d.alert_kind || 'alert').replace(/_/g, ' ')} alert`;
}

/** The secondary line: masked recipient, provider status, sanitized error. */
export function deliveryDetail(d: Pick<NotificationDelivery, 'recipient' | 'provider_status' | 'error'>): string {
  return [d.recipient, d.provider_status ? `HTTP ${d.provider_status}` : null, d.error].filter(Boolean).join(' · ');
}

/** The Failing badge's tooltip. */
export const CHANNEL_FAILING_HINT =
  'A delivery to this channel failed in the last 24 hours and none has succeeded since (test sends count).';

/** The channel page's outcome filter: one segment per question an operator asks. */
export type DeliveryFilter = 'all' | 'failures' | 'delivered' | 'dropped';

export const DELIVERY_FILTERS: { value: DeliveryFilter; label: string; outcomes: DeliveryOutcome[] }[] = [
  { value: 'all', label: 'All', outcomes: [] },
  { value: 'failures', label: 'Failures', outcomes: ['failed', 'permanent_failure'] },
  { value: 'delivered', label: 'Delivered', outcomes: ['delivered'] },
  { value: 'dropped', label: 'Not sent', outcomes: ['dropped'] },
];

/** The API outcome filter for a segment. */
export function deliveryFilterOutcomes(filter: DeliveryFilter): DeliveryOutcome[] {
  return DELIVERY_FILTERS.find((f) => f.value === filter)?.outcomes ?? [];
}
