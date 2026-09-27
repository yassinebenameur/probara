import type { DNSMonitorConfig, PingMonitorConfig } from '../types';
import type { FormErrors } from './common';
import type { PingFormData } from './ping';

/**
 * DNS fields. `host` is the same field ping uses, so switching between the two
 * types while creating a monitor keeps the typed host.
 */
export interface DNSFormData extends PingFormData {
  record_type: string;
  /** Comma- or newline-separated, as typed. */
  expected_answers: string;
  nameserver: string;
}

export function initialDNSFormData(ping?: PingMonitorConfig, dns?: DNSMonitorConfig): DNSFormData {
  return {
    host: ping?.host || dns?.host || '',
    record_type: dns?.record_type || 'A',
    expected_answers: (dns?.expected_answers || []).join(', '),
    nameserver: dns?.nameserver || '',
  };
}

export const parseDNSExpectedAnswers = (input: string): string[] =>
  input
    .split(/[\n,]/)
    .map((item) => item.trim())
    .filter((item) => item.length > 0);

export function validateDNSForm(data: DNSFormData): FormErrors {
  return data.host.trim() ? {} : { host: 'Host is required' };
}

export function buildDNSConfig(data: DNSFormData): DNSMonitorConfig {
  const config: DNSMonitorConfig = {
    host: data.host.trim(),
    record_type: data.record_type || 'A',
  };
  const expected = parseDNSExpectedAnswers(data.expected_answers);
  if (expected.length > 0) config.expected_answers = expected;
  if (data.nameserver.trim()) config.nameserver = data.nameserver.trim();
  return config;
}
