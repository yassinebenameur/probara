import type {
  CreateMonitorRequest,
  DNSMonitorConfig,
  HTTPMonitorConfig,
  Monitor,
  MonitorConfig,
  MonitorType,
  PingMonitorConfig,
  SyntheticAPIMonitorConfig,
  SyntheticBrowserMonitorConfig,
} from '../types';
import { type FormErrors, hasErrors } from './common';
import { type DNSFormData, buildDNSConfig, initialDNSFormData, validateDNSForm } from './dns';
import { type HttpFormData, buildHttpConfig, initialHttpFormData, validateHttpForm } from './http';
import { buildPingConfig, validatePingForm } from './ping';
import { type SharedMonitorFields, buildMonitorRequest, validateSharedFields } from './shared';
import {
  type SyntheticAPIFormData,
  buildSyntheticAPIConfig,
  initialSyntheticAPIFormData,
  validateSyntheticAPIForm,
} from './synthetic-api';
import {
  type SyntheticBrowserFormData,
  buildSyntheticBrowserConfig,
  guidedJourneyOf,
  initialSyntheticBrowserFormData,
  validateSyntheticBrowserForm,
} from './synthetic-browser';

/** Types edited inline by MonitorForm; every other type delegates to its own form component. */
export type InlineMonitorType = 'http' | 'ping' | 'dns' | 'synthetic_api' | 'synthetic_browser';

export const isInlineMonitorType = (type: MonitorType): type is InlineMonitorType =>
  type === 'http' || type === 'ping' || type === 'dns' || type === 'synthetic_api' || type === 'synthetic_browser';

/**
 * Draft state for every inline type at once, so switching types while creating
 * a monitor keeps what was typed for each. Ping and DNS share `host`.
 */
export interface InlineMonitorDrafts {
  http: HttpFormData;
  host: DNSFormData;
  syntheticApi: SyntheticAPIFormData;
  syntheticBrowser: SyntheticBrowserFormData;
}

export interface InlineMonitorFormState extends InlineMonitorDrafts {
  type: InlineMonitorType;
  shared: SharedMonitorFields;
}

/**
 * The saved (or cloned) config when the form opens on `type`, else undefined.
 * The API returns `config` typed by `monitor.type`, hence the narrowing cast.
 */
export function sourceConfigFor<T extends MonitorConfig>(
  type: MonitorType,
  monitor?: Monitor,
  initialData?: CreateMonitorRequest
): T | undefined {
  const sourceType: MonitorType = monitor?.type || initialData?.type || 'http';
  return sourceType === type ? ((monitor?.config || initialData?.config) as T | undefined) : undefined;
}

/** Only the source type's config seeds its draft; the other drafts start empty. */
export function initialInlineMonitorDrafts(monitor?: Monitor, initialData?: CreateMonitorRequest): InlineMonitorDrafts {
  const configFor = <T extends MonitorConfig>(type: MonitorType) => sourceConfigFor<T>(type, monitor, initialData);
  return {
    http: initialHttpFormData(configFor<HTTPMonitorConfig>('http')),
    host: initialDNSFormData(configFor<PingMonitorConfig>('ping'), configFor<DNSMonitorConfig>('dns')),
    syntheticApi: initialSyntheticAPIFormData(configFor<SyntheticAPIMonitorConfig>('synthetic_api')),
    syntheticBrowser: initialSyntheticBrowserFormData(
      configFor<SyntheticBrowserMonitorConfig>('synthetic_browser'),
      Boolean(monitor)
    ),
  };
}

function validateTypeFields(state: InlineMonitorFormState): FormErrors {
  switch (state.type) {
    case 'http':
      return validateHttpForm(state.http);
    case 'ping':
      return validatePingForm(state.host);
    case 'dns':
      return validateDNSForm(state.host);
    case 'synthetic_api':
      return validateSyntheticAPIForm(state.syntheticApi);
    case 'synthetic_browser':
      return validateSyntheticBrowserForm(state.syntheticBrowser);
  }
}

function buildTypeConfig(state: InlineMonitorFormState): MonitorConfig {
  switch (state.type) {
    case 'http':
      return buildHttpConfig(state.http);
    case 'ping':
      return buildPingConfig(state.host);
    case 'dns':
      return buildDNSConfig(state.host);
    case 'synthetic_api':
      return buildSyntheticAPIConfig(state.syntheticApi);
    case 'synthetic_browser':
      return buildSyntheticBrowserConfig(state.syntheticBrowser);
  }
}

export type InlineMonitorSubmission =
  | { ok: true; request: CreateMonitorRequest }
  | { ok: false; errors: FormErrors };

/** Validates shared and type fields together; the request is built only when both pass. */
export function prepareInlineMonitorSubmission(state: InlineMonitorFormState): InlineMonitorSubmission {
  const errors = { ...validateSharedFields(state.shared), ...validateTypeFields(state) };
  if (hasErrors(errors)) return { ok: false, errors };
  return { ok: true, request: buildMonitorRequest(state.type, buildTypeConfig(state), state.shared) };
}

function describeTarget(state: InlineMonitorFormState): string | null {
  switch (state.type) {
    case 'http': {
      const url = state.http.url.trim();
      if (!url) return null;
      let host = url;
      try {
        const u = new URL(host.startsWith('http') ? host : `https://${host}`);
        host = `${u.hostname}${u.pathname !== '/' ? u.pathname : ''}`;
      } catch {
        /* show as typed */
      }
      return `${state.http.method} ${host} · expect ${state.http.status_rules.trim() || '2xx'}`;
    }
    case 'ping':
      return state.host.host.trim() ? `ping ${state.host.host.trim()}` : null;
    case 'dns':
      return state.host.host.trim() ? `resolve ${state.host.record_type} for ${state.host.host.trim()}` : null;
    case 'synthetic_api':
      return `run ${state.syntheticApi.steps.length}-step API journey`;
    case 'synthetic_browser': {
      const browser = state.syntheticBrowser;
      const steps = browser.setup_mode === 'guided' ? guidedJourneyOf(browser).steps.length : browser.steps.length;
      return `run ${steps}-step browser journey`;
    }
  }
}

/**
 * Plain-language summary of what the monitor will do, shown next to the form
 * actions so misconfigurations are visible right before submitting.
 */
export function describeCheckPlan(state: InlineMonitorFormState): string | null {
  const target = describeTarget(state);
  if (!target) return null;
  const fails = state.shared.consecutive_failures_threshold;
  return `Every ${state.shared.interval_seconds}s · ${target} · down after ${fails} failed check${fails === 1 ? '' : 's'}`;
}
