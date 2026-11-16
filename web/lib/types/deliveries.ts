// Notification delivery history (mirrors shared/notifications/deliverylog).

/** What one attempt came to. `dropped` is a deliberate non-send, never a failure. */
export type DeliveryOutcome = 'delivered' | 'failed' | 'permanent_failure' | 'dropped';

/**
 * Why a row was dropped or failed (deliverylog.Reason*). Free text on the
 * wire, so an unknown value renders as-is.
 */
export type DeliveryReason =
  | 'resolved'
  | 'unacknowledged'
  | 'muted'
  | 'muted_requeued'
  | 'channel_inactive'
  | 'channel_changed'
  | 'egress_blocked'
  | 'invalid_config'
  | 'plugin_missing'
  | 'retries_exhausted';

export interface NotificationDelivery {
  id: string;
  /** Null for a channel test send. */
  alert_id: string | null;
  channel_id: string;
  channel_name: string;
  channel_type: string;
  event_type: string;
  /** Masked; set per recipient on channels that fan out (SMS). */
  recipient: string | null;
  attempt: number;
  /** sync: the alerter (or the API, for tests) sent it; async: a delivery worker did. */
  path: 'sync' | 'async';
  test: boolean;
  outcome: DeliveryOutcome;
  reason: DeliveryReason | string | null;
  /** Sanitized: no URLs past the host, no tokens or secrets. */
  error: string | null;
  provider_status: number | null;
  duration_ms: number | null;
  alert_kind: string | null;
  monitor_name: string | null;
  /** A monitor-less alert's subject: the offline location, or a mesh edge's two ends. */
  source_location_name: string | null;
  target_location_name: string | null;
  created_at: string;
}

export interface NotificationDeliveryPage {
  items: NotificationDelivery[];
  page: number;
  page_size: number;
  total: number;
}
