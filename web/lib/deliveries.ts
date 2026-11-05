// Wording for the notification delivery history (alerts page Deliveries
// panel, alert channel page). Outcome labels and tones live in
// lib/status-tones.ts.

import type { NotificationDelivery } from './types';

const EVENT_LABEL: Record<string, string> = {
  created: 'Triggered',
  reminder: 'Reminder',
  resolved: 'Resolved',
  acknowledged: 'Acknowledged',
  suspected_cause: 'Suspected cause',
  updated: 'Card updated',
};

/** The event a delivery carried; unknown (newer) event types show as sent. */
export function deliveryEventLabel(eventType: string): string {
  return EVENT_LABEL[eventType] ?? eventType.replace(/_/g, ' ').replace(/^\w/, (c) => c.toUpperCase());
}

/** "2 of 3 recipients" for a fan-out channel (SMS), else null. */
export function deliveryRecipients(d: Pick<NotificationDelivery, 'recipients_total' | 'recipients_delivered'>): string | null {
  if (!d.recipients_total) return null;
  const n = d.recipients_total;
  return `${d.recipients_delivered ?? 0} of ${n} recipient${n === 1 ? '' : 's'}`;
}

/** Who sent it: the alerter itself, or a delivery worker after a JetStream hand-off. */
export function deliveryPathLabel(path: NotificationDelivery['path']): string {
  return path === 'async' ? 'Worker' : 'Alerter';
}

/** What the alert was about, for the per-channel view: its monitor, else its kind. */
export function deliveryAlertSubject(d: Pick<NotificationDelivery, 'monitor_name' | 'alert_kind'>): string {
  if (d.monitor_name) return d.monitor_name;
  return `${String(d.alert_kind || 'alert').replace(/_/g, ' ')} alert`;
}

/**
 * Where a delivery went: the channel, or for an escalation page the person
 * paged ("alice · a***@example.com", via the SMS channel when one carried it).
 */
export function deliveryTarget(
  d: Pick<NotificationDelivery, 'channel_name' | 'channel_id' | 'username' | 'recipient'>
): { title: string; via: string | null } {
  if (!d.recipient) return { title: d.channel_name, via: null };
  const who = d.username ?? 'Removed user';
  return { title: `${who} · ${d.recipient}`, via: d.channel_id ? `via ${d.channel_name}` : 'Email' };
}

/** "Step 2" (and the repeat cycle) for an escalation page's ledger key, else null. */
export function deliveryStepLabel(deliveryKey: string): string | null {
  const m = /^escalation:(\d+):(\d+)/.exec(deliveryKey);
  if (!m) return null;
  const step = Number(m[1]) + 1;
  const cycle = Number(m[2]);
  return cycle > 0 ? `Step ${step}, repeat ${cycle}` : `Step ${step}`;
}
