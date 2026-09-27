import type { PingMonitorConfig } from '../types';
import type { FormErrors } from './common';

export interface PingFormData {
  host: string;
}

export function validatePingForm(data: PingFormData): FormErrors {
  return data.host.trim() ? {} : { host: 'Host is required' };
}

export function buildPingConfig(data: PingFormData): PingMonitorConfig {
  return { host: data.host.trim() };
}
