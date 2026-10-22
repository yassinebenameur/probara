// Wording for the live alert-stream toasts (components/alerts/
// AlertStreamProvider.tsx), by alert kind. A toast is a glance: who, and what
// condition, in one line. A burst of events coalesces into one counted toast
// ("5 alerts triggered") whose text comes from summarizeAlertBurst.

import { formatAlertValue, formatSeriesLabel } from './metrics';
import type { Alert, AlertStreamEventType } from './types';

export type AlertToastType = 'error' | 'warning' | 'success' | 'info';

export interface AlertToastContent {
  title: string;
  message: string;
  type: AlertToastType;
  /** Coalescing key: events with the same group merge into one toast. */
  group: string;
}

/** Who the alert is about: the monitor, or the location pair of a mesh edge. */
export function alertSubject(alert: Alert): string {
  if (alert.monitor_name) return alert.monitor_name;
  if (alert.source_location_name && alert.target_location_name) {
    return `${alert.source_location_name} → ${alert.target_location_name}`;
  }
  return alert.policy_name || (alert.kind === 'mesh_edge' ? 'A mesh path' : 'A monitor');
}

/** The firing condition, phrased to follow the subject ("Orders API is down"). */
export function alertCondition(alert: Alert): string {
  switch (alert.kind) {
    case 'latency_anomaly':
      return alert.observed_latency_ms != null
        ? `latency is anomalous (${Math.round(alert.observed_latency_ms)} ms)`
        : 'latency is anomalous';
    case 'host_metric': {
      const label = alert.metric_name ? formatSeriesLabel(alert.metric_name) : 'a host metric';
      if (alert.metric_value != null) {
        return `${label} at ${formatAlertValue(alert.metric_name, alert.metric_value)}${
          alert.threshold_value != null ? ` (threshold ${formatAlertValue(alert.metric_name, alert.threshold_value)})` : ''
        }`;
      }
      return `${label} is over its threshold`;
    }
    case 'tls_expiry':
      return alert.metric_value != null
        ? `certificate expires in ${Math.max(0, Math.round(alert.metric_value))}d`
        : 'certificate is about to expire';
    case 'mesh_edge':
      return 'path is down';
    case 'availability':
    default:
      return 'is down';
  }
}

// What closed, for resolve toasts. "Back up" is not claimed: a resolve can be
// manual while the target is still failing.
const RESOLVED_NOUN: Record<Alert['kind'], string> = {
  availability: 'downtime alert',
  latency_anomaly: 'latency alert',
  host_metric: 'host metric alert',
  tls_expiry: 'certificate alert',
  mesh_edge: 'path alert',
};

function isDependencySuppressed(alert: Alert): boolean {
  return alert.suppression_reason === 'dependency';
}

/**
 * The toast for one stream event, or null when the event deserves none
 * (acknowledgements: someone already acted on it).
 */
export function alertToast(type: AlertStreamEventType, alert: Alert): AlertToastContent | null {
  const subject = alertSubject(alert);
  if (type === 'created') {
    if (isDependencySuppressed(alert)) {
      // The alerter pages nobody for these; fold them under their root cause.
      const root = alert.root_cause_monitor_name || 'an upstream dependency';
      return {
        title: `Likely caused by ${root}`,
        message: `${subject} ${alertCondition(alert)}`,
        type: 'warning',
        group: `alert-dependents:${alert.root_cause_monitor_id ?? root}`,
      };
    }
    // Same split as alertCondition: a missing kind falls to its "is down"
    // wording, so it must take the down tone too.
    const severe = !alert.kind || alert.kind === 'availability' || alert.kind === 'mesh_edge';
    return {
      title: 'Alert triggered',
      message: `${subject} ${alertCondition(alert)}`,
      type: severe ? 'error' : 'warning',
      group: 'alert-created',
    };
  }
  if (type === 'resolved') {
    return {
      title: 'Alert resolved',
      message: `${subject}: ${RESOLVED_NOUN[alert.kind] ?? 'alert'} closed`,
      type: 'success',
      group: 'alert-resolved',
    };
  }
  return null;
}

/** "Orders API, Billing DB and 3 more" */
export function listSubjects(subjects: readonly string[], shown = 2): string {
  const unique = Array.from(new Set(subjects));
  if (unique.length <= shown + 1) {
    if (unique.length <= 1) return unique[0] ?? '';
    return `${unique.slice(0, -1).join(', ')} and ${unique[unique.length - 1]}`;
  }
  return `${unique.slice(0, shown).join(', ')} and ${unique.length - shown} more`;
}

/**
 * One toast for a burst of events of the same group. A single event keeps
 * its own wording; several become a count plus the first names.
 */
export function summarizeAlertBurst(type: AlertStreamEventType, alerts: readonly Alert[]): { title: string; message: string } {
  const last = alerts[alerts.length - 1];
  if (alerts.length === 0 || !last) return { title: '', message: '' };
  const single = alertToast(type, last);
  if (alerts.length === 1 && single) return { title: single.title, message: single.message };
  const subjects = alerts.map(alertSubject);
  const n = alerts.length;
  if (type === 'created' && isDependencySuppressed(last)) {
    const root = last.root_cause_monitor_name || 'an upstream dependency';
    return {
      title: `Likely caused by ${root}`,
      message: `${n} dependent alerts: ${listSubjects(subjects)}`,
    };
  }
  return {
    title: `${n} alerts ${type === 'resolved' ? 'resolved' : 'triggered'}`,
    message: listSubjects(subjects),
  };
}
