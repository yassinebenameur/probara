import { CreateMonitorRequest, DomainMonitorConfig, Monitor } from '../types';
import { buildScheduledMonitorRequest, initialScheduledFormFields } from './common';

// DomainForm's state and request. A domain monitor has no secrets; its
// schedule has a floor (shared/models DomainMinIntervalSeconds) because
// registries rate-limit lookups, and its default cadence is daily.

/** The shortest interval the API accepts for a domain monitor. */
export const DOMAIN_MIN_INTERVAL_SECONDS = 3600;
/** The cadence a new domain monitor starts with. */
export const DOMAIN_DEFAULT_INTERVAL_SECONDS = 86400;
/** The warning window the API applies when min_days_valid is absent. */
export const DOMAIN_DEFAULT_MIN_DAYS_VALID = 30;
/** The largest warning window the API accepts. */
export const DOMAIN_MAX_MIN_DAYS_VALID = 3650;
/** Interval presets for the schedule panel: hourly to daily. */
export const DOMAIN_INTERVAL_PRESETS = [3600, 21600, 43200, 86400] as const;

/** The lowest "mark down after" the API allows: one registry answer must never page. */
export const DOMAIN_MIN_CONSECUTIVE_FAILURES = 2;

/**
 * DomainForm's initial state: the stored monitor on edit, else the clone
 * source, else the defaults (daily, 30 s timeout, a 30-day window, down
 * after two failed lookups).
 */
export function initialDomainFormState(monitor?: Monitor, initialData?: CreateMonitorRequest) {
  const isEditMode = Boolean(monitor);
  const initialConfig =
    !isEditMode && initialData?.type === 'domain' ? (initialData.config as DomainMonitorConfig) : undefined;
  const existingConfig = monitor && monitor.type === 'domain' ? (monitor.config as DomainMonitorConfig) : undefined;
  const config = existingConfig ?? initialConfig;
  const shared = initialScheduledFormFields(monitor, initialData);

  return {
    ...shared,
    interval_seconds: monitor?.interval_seconds || initialData?.interval_seconds || DOMAIN_DEFAULT_INTERVAL_SECONDS,
    timeout_seconds: monitor?.timeout_seconds || initialData?.timeout_seconds || 30,
    domain: config?.domain ?? '',
    min_days_valid: config?.min_days_valid ?? DOMAIN_DEFAULT_MIN_DAYS_VALID,
  };
}

export type DomainFormState = ReturnType<typeof initialDomainFormState>;

/** The config DomainForm submits and tests. */
export const buildDomainConfig = (form: DomainFormState): DomainMonitorConfig => ({
  domain: form.domain.trim(),
  min_days_valid: form.min_days_valid,
});

/** The request DomainForm submits. Tags are always sent, so a save can clear them. */
export function buildDomainRequest(form: DomainFormState): CreateMonitorRequest {
  return buildScheduledMonitorRequest('domain', buildDomainConfig(form), form, { alwaysSendTags: true });
}

/** Client-side checks mirroring the API's, keyed by form field. */
export function validateDomainForm(form: DomainFormState): Record<string, string> {
  const errors: Record<string, string> = {};
  if (!form.name.trim()) errors.name = 'Name is required';
  const domain = form.domain.trim();
  if (!domain) {
    errors.domain = 'Domain is required';
  } else if (/[/:@?#\s[\]]/.test(domain)) {
    errors.domain = 'Enter a bare domain such as example.com, without scheme, port or path';
  } else if (/^[\d.]+$/.test(domain)) {
    errors.domain = 'Enter a domain name, not an IP address';
  } else if (!domain.replace(/\.$/, '').includes('.')) {
    errors.domain = 'Enter a registered domain such as example.com, not a top-level domain';
  }
  if (!Number.isInteger(form.min_days_valid) || form.min_days_valid < 0 || form.min_days_valid > DOMAIN_MAX_MIN_DAYS_VALID) {
    errors.min_days_valid = `Enter a whole number of days between 0 and ${DOMAIN_MAX_MIN_DAYS_VALID}`;
  }
  if (form.interval_seconds < DOMAIN_MIN_INTERVAL_SECONDS) {
    errors.interval_seconds = 'Check at most once an hour: registries rate-limit lookups';
  }
  if (form.timeout_seconds >= form.interval_seconds) {
    errors.timeout_seconds = 'Timeout must be less than interval';
  }
  if (form.consecutive_failures_threshold < DOMAIN_MIN_CONSECUTIVE_FAILURES) {
    errors.consecutive_failures_threshold = 'Mark a domain down after at least 2 failed lookups';
  }
  if (form.location_ids.length > 1) {
    errors.location_ids = 'A domain monitor runs from one location: registries answer the same from everywhere';
  }
  return errors;
}
