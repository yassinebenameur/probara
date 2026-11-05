import type { PingMonitorConfig } from '../types';

// The ping fields of MonitorForm's shared state, kept as typed strings so a
// cleared input stays empty. Bounds mirror Go's shared/models PingMonitorConfig
// (Validate and ValidatePingSchedule), which the API enforces on save.

export const PING_DEFAULT_NEW_COUNT = 3;
const PING_MAX_COUNT = 10;
const PING_MIN_INTERVAL_MS = 200;
const PING_MAX_INTERVAL_MS = 10000;
const PING_DEFAULT_INTERVAL_MS = 1000;
const PING_MIN_PACKET = 24;
const PING_MAX_PACKET = 1472;
const PING_REPLY_WAIT_MS = 1000;

export interface PingFormFields {
  ping_count: string;
  ping_interval_ms: string;
  ping_packet_size: string;
  ping_max_packet_loss_pct: string;
  ping_max_rtt_ms: string;
  ping_warn_rtt_ms: string;
}

const str = (v: number | undefined) => (v === undefined || v === null ? '' : String(v));

/**
 * The ping fields' initial state. A new monitor starts at 3 echoes; a stored
 * or cloned config keeps what it has, so a config without `count` (one echo)
 * saves back without one.
 */
export function initialPingFields(config?: PingMonitorConfig): PingFormFields {
  return {
    ping_count: config ? str(config.count) : String(PING_DEFAULT_NEW_COUNT),
    ping_interval_ms: str(config?.interval_ms),
    ping_packet_size: str(config?.packet_size),
    ping_max_packet_loss_pct: str(config?.max_packet_loss_pct),
    ping_max_rtt_ms: str(config?.max_rtt_ms),
    ping_warn_rtt_ms: str(config?.warn_rtt_ms),
  };
}

const wholeNumber = (s: string): number | null => (/^\d+$/.test(s.trim()) ? parseInt(s.trim(), 10) : null);
const decimal = (s: string): number | null => {
  const t = s.trim();
  if (!/^\d+(\.\d+)?$/.test(t)) return null;
  return Number(t);
};

/** Field errors for the ping inputs; timeoutSeconds checks that the echoes fit. */
export function validatePingFields(form: PingFormFields, timeoutSeconds: number): Record<string, string> {
  const errors: Record<string, string> = {};
  const count = form.ping_count.trim() ? wholeNumber(form.ping_count) : 1;
  if (count === null || count < 1 || count > PING_MAX_COUNT) {
    errors.ping_count = `Must be a whole number from 1 to ${PING_MAX_COUNT}`;
  }
  const interval = form.ping_interval_ms.trim() ? wholeNumber(form.ping_interval_ms) : PING_DEFAULT_INTERVAL_MS;
  if (interval === null || interval < PING_MIN_INTERVAL_MS || interval > PING_MAX_INTERVAL_MS) {
    errors.ping_interval_ms = `Must be ${PING_MIN_INTERVAL_MS}–${PING_MAX_INTERVAL_MS} ms`;
  }
  if (form.ping_packet_size.trim()) {
    const size = wholeNumber(form.ping_packet_size);
    if (size === null || size < PING_MIN_PACKET || size > PING_MAX_PACKET) {
      errors.ping_packet_size = `Must be ${PING_MIN_PACKET}–${PING_MAX_PACKET} bytes`;
    }
  }
  if (form.ping_max_packet_loss_pct.trim()) {
    const loss = decimal(form.ping_max_packet_loss_pct);
    if (loss === null || loss >= 100) errors.ping_max_packet_loss_pct = 'Must be 0 or more and below 100';
  }
  const maxRtt = form.ping_max_rtt_ms.trim() ? wholeNumber(form.ping_max_rtt_ms) : undefined;
  if (maxRtt === null || maxRtt === 0) errors.ping_max_rtt_ms = 'Must be a whole number of ms above 0';
  const warnRtt = form.ping_warn_rtt_ms.trim() ? wholeNumber(form.ping_warn_rtt_ms) : undefined;
  if (warnRtt === null || warnRtt === 0) {
    errors.ping_warn_rtt_ms = 'Must be a whole number of ms above 0';
  } else if (warnRtt !== undefined && typeof maxRtt === 'number' && maxRtt > 0 && warnRtt >= maxRtt) {
    errors.ping_warn_rtt_ms = 'Must be below the failure threshold';
  }
  if (count !== null && interval !== null && !errors.ping_count && !errors.ping_interval_ms) {
    const needMs = (count - 1) * interval + PING_REPLY_WAIT_MS;
    if (needMs > timeoutSeconds * 1000) {
      errors.timeout_seconds = `${count} echoes ${interval} ms apart need a timeout of at least ${Math.ceil(needMs / 1000)}s`;
    }
  }
  return errors;
}

/** The ping config a submit sends, for fields that passed validatePingFields. */
export function buildPingConfig(host: string, form: PingFormFields): PingMonitorConfig {
  const config: PingMonitorConfig = { host: host.trim() };
  const whole = (s: string) => (s.trim() ? parseInt(s.trim(), 10) : undefined);
  const count = whole(form.ping_count);
  if (count !== undefined) config.count = count;
  const interval = whole(form.ping_interval_ms);
  if (interval !== undefined) config.interval_ms = interval;
  const size = whole(form.ping_packet_size);
  if (size !== undefined) config.packet_size = size;
  if (form.ping_max_packet_loss_pct.trim()) config.max_packet_loss_pct = Number(form.ping_max_packet_loss_pct.trim());
  const maxRtt = whole(form.ping_max_rtt_ms);
  if (maxRtt !== undefined) config.max_rtt_ms = maxRtt;
  const warnRtt = whole(form.ping_warn_rtt_ms);
  if (warnRtt !== undefined) config.warn_rtt_ms = warnRtt;
  return config;
}
