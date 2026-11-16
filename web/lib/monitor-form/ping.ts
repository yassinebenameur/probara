import type { PingMonitorConfig } from '../types';

// The ping monitor's echo settings in MonitorForm's state: text fields, empty
// meaning "absent" so the server's defaults apply (3 echoes 250 ms apart,
// failing only when every echo is lost). The bounds mirror Go
// models.PingMonitorConfig.Validate; the API also checks that the echoes fit
// the timeout.

export const PING_DEFAULT_COUNT = 3;
export const PING_MAX_COUNT = 10;
export const PING_DEFAULT_INTERVAL_MS = 250;
export const PING_MIN_INTERVAL_MS = 200;
export const PING_MAX_INTERVAL_MS = 10000;

export interface PingFormFields {
  ping_count: string;
  ping_interval_ms: string;
  ping_max_packet_loss_pct: string;
  ping_max_rtt_ms: string;
}

const text = (n: number | undefined): string => (n === undefined || n === null ? '' : String(n));

/** The form fields of a stored or cloned ping config (blank when absent). */
export function pingFormFields(config?: PingMonitorConfig): PingFormFields {
  return {
    ping_count: text(config?.count),
    ping_interval_ms: text(config?.interval_ms),
    ping_max_packet_loss_pct: text(config?.max_packet_loss_pct),
    ping_max_rtt_ms: text(config?.max_rtt_ms),
  };
}

const wholeNumber = (s: string): number | null => (/^\d+$/.test(s.trim()) ? Number(s.trim()) : null);

/** Field errors, keyed like the form state. */
export function pingFieldErrors(form: PingFormFields): Partial<Record<keyof PingFormFields, string>> {
  const errors: Partial<Record<keyof PingFormFields, string>> = {};
  if (form.ping_count.trim()) {
    const n = wholeNumber(form.ping_count);
    if (n === null || n < 1 || n > PING_MAX_COUNT) errors.ping_count = `Between 1 and ${PING_MAX_COUNT} echoes`;
  }
  if (form.ping_interval_ms.trim()) {
    const n = wholeNumber(form.ping_interval_ms);
    if (n === null || n < PING_MIN_INTERVAL_MS || n > PING_MAX_INTERVAL_MS) {
      errors.ping_interval_ms = `Between ${PING_MIN_INTERVAL_MS} and ${PING_MAX_INTERVAL_MS} ms`;
    }
  }
  if (form.ping_max_packet_loss_pct.trim()) {
    const n = Number(form.ping_max_packet_loss_pct.trim());
    if (!Number.isFinite(n) || n < 0 || n >= 100) errors.ping_max_packet_loss_pct = '0 or more, below 100';
  }
  if (form.ping_max_rtt_ms.trim()) {
    const n = wholeNumber(form.ping_max_rtt_ms);
    if (n === null || n < 1) errors.ping_max_rtt_ms = 'A whole number of milliseconds, 1 or more';
  }
  return errors;
}

/** The ping config a submit sends: only the settings the operator filled in. */
export function buildPingConfig(host: string, form: PingFormFields): PingMonitorConfig {
  const config: PingMonitorConfig = { host: host.trim() };
  if (form.ping_count.trim()) config.count = Number(form.ping_count.trim());
  if (form.ping_interval_ms.trim()) config.interval_ms = Number(form.ping_interval_ms.trim());
  if (form.ping_max_packet_loss_pct.trim()) config.max_packet_loss_pct = Number(form.ping_max_packet_loss_pct.trim());
  if (form.ping_max_rtt_ms.trim()) config.max_rtt_ms = Number(form.ping_max_rtt_ms.trim());
  return config;
}
