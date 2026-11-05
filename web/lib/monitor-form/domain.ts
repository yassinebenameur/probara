import { CreateMonitorRequest, DomainMonitorConfig, Monitor } from '../types';
import { buildScheduledMonitorRequest, initialScheduledFormFields } from './common';

// DomainForm's state and request. A domain monitor has no secrets; its
// schedule floor is one hour (shared/models DomainMinIntervalSeconds), and a
// new monitor checks twice a day.

/** The shortest interval the API accepts for a domain monitor. */
export const DOMAIN_MIN_INTERVAL_SECONDS = 3600;
/** A new domain monitor's interval: twice a day. */
export const DOMAIN_DEFAULT_INTERVAL_SECONDS = 43200;
/** A new domain monitor's timeout: room for RDAP plus the WHOIS fallback. */
export const DOMAIN_DEFAULT_TIMEOUT_SECONDS = 30;
/** A new domain monitor's alert threshold. */
export const DOMAIN_DEFAULT_MIN_DAYS = 30;
export const DOMAIN_INTERVAL_PRESETS = [3600, 21600, 43200, 86400] as const;

/** DomainForm's initial state: the stored monitor on edit, else the clone source. */
export function initialDomainFormState(monitor?: Monitor, initialData?: CreateMonitorRequest) {
  const isEditMode = Boolean(monitor);
  const cloneConfig =
    !isEditMode && initialData?.type === 'domain' ? (initialData.config as DomainMonitorConfig) : undefined;
  const existingConfig = monitor && monitor.type === 'domain' ? (monitor.config as DomainMonitorConfig) : undefined;
  const source = existingConfig ?? cloneConfig;
  const shared = initialScheduledFormFields(monitor, initialData);
  // Not a stored or cloned domain monitor: start on the domain schedule.
  const isNew = !existingConfig && !cloneConfig;

  return {
    ...shared,
    interval_seconds: isNew ? DOMAIN_DEFAULT_INTERVAL_SECONDS : shared.interval_seconds,
    timeout_seconds: isNew ? DOMAIN_DEFAULT_TIMEOUT_SECONDS : shared.timeout_seconds,
    domain: source?.domain ?? '',
    // A stored monitor without a threshold keeps alerting off; a new one
    // starts at 30 days.
    alert_on_expiry: source ? source.min_days_valid != null : true,
    min_days_valid: source?.min_days_valid ?? DOMAIN_DEFAULT_MIN_DAYS,
    rdap_server_override: source?.rdap_server_override ?? '',
    whois_fallback: source?.whois_fallback ?? true,
  };
}

export type DomainFormState = ReturnType<typeof initialDomainFormState>;

/** The config DomainForm submits. Optional fields are sent only when set. */
export function buildDomainConfig(form: DomainFormState): DomainMonitorConfig {
  const config: DomainMonitorConfig = { domain: form.domain.trim() };
  if (form.alert_on_expiry) config.min_days_valid = form.min_days_valid;
  const override = form.rdap_server_override.trim();
  if (override) config.rdap_server_override = override;
  config.whois_fallback = form.whois_fallback;
  return config;
}

/** The request DomainForm submits. Tags are always sent, so a save can clear them. */
export function buildDomainRequest(form: DomainFormState): CreateMonitorRequest {
  return buildScheduledMonitorRequest('domain', buildDomainConfig(form), form, { alwaysSendTags: true });
}

/** Field errors for a DomainForm submit (empty when it may be sent). */
export function validateDomainForm(form: DomainFormState): Record<string, string> {
  const errors: Record<string, string> = {};
  if (!form.name.trim()) errors.name = 'Name is required';
  const domain = form.domain.trim();
  if (!domain) {
    errors.domain = 'Domain is required';
  } else if (/[\s/:@]/.test(domain) || !domain.replace(/\.$/, '').includes('.')) {
    errors.domain = 'Enter the registered domain, such as example.com';
  }
  if (form.alert_on_expiry && (!Number.isInteger(form.min_days_valid) || form.min_days_valid < 0 || form.min_days_valid > 3650)) {
    errors.min_days_valid = 'Between 0 and 3650 days';
  }
  const override = form.rdap_server_override.trim();
  if (override && !/^https:\/\/[^/?#\s]+/i.test(override)) {
    errors.rdap_server_override = 'Must be an https:// RDAP base URL';
  }
  if (form.interval_seconds < DOMAIN_MIN_INTERVAL_SECONDS) {
    errors.interval_seconds = 'Domain monitors run at most once an hour';
  }
  if (form.timeout_seconds >= form.interval_seconds) {
    errors.timeout_seconds = 'Timeout must be less than interval';
  }
  return errors;
}
