import { CreateMonitorRequest, Monitor, SIPMonitorConfig } from '../types';
import { buildScheduledMonitorRequest, initialScheduledFormFields } from './common';
import { SipFormFields, buildSipConfig, sipCallFormFields } from '../sip-config';

// SipForm's state and request. The config itself is built by
// buildSipConfig (lib/sip-config.ts) on top of the loaded config, so keys the
// form does not render survive a save; password follows the monitor-secret
// merge contract there ("***" keeps, "" clears).

export const normalizeSipMethod = (method: string | undefined): SipFormFields['method'] => {
  const m = (method || '').trim().toLowerCase();
  return m === 'register' || m === 'invite' ? m : 'options';
};

/** The config SipForm builds on: the stored monitor's on edit, else the clone's. */
export function sipLoadedConfig(monitor?: Monitor, initialData?: CreateMonitorRequest): SIPMonitorConfig | undefined {
  const isEditMode = Boolean(monitor);
  const initialSipConfig =
    !isEditMode && initialData?.type === 'sip' ? (initialData.config as SIPMonitorConfig) : undefined;
  const editSipConfig =
    monitor && monitor.type === 'sip' ? (monitor.config as SIPMonitorConfig) : undefined;
  return editSipConfig ?? initialSipConfig;
}

/** SipForm's initial state: the stored monitor on edit, else the clone source. */
export function initialSipFormState(monitor?: Monitor, initialData?: CreateMonitorRequest) {
  const isEditMode = Boolean(monitor);
  const initialSipConfig =
    !isEditMode && initialData?.type === 'sip' ? (initialData.config as SIPMonitorConfig) : undefined;
  const editSipConfig =
    monitor && monitor.type === 'sip' ? (monitor.config as SIPMonitorConfig) : undefined;
  const loadedConfig = editSipConfig ?? initialSipConfig;

  // RFC 3263 resolution ("Locate server via DNS") leaves port and transport
  // to SRV when they are unset (0 and '').
  // Read case-insensitively, as the worker does: a stored "RFC3263" is rfc3263.
  const rawResolution = editSipConfig?.resolution ?? initialSipConfig?.resolution;
  const initialResolution: SipFormFields['resolution'] =
    typeof rawResolution === 'string' && rawResolution.trim().toLowerCase() === 'rfc3263' ? 'rfc3263' : 'a';
  const initialRfc3263 = initialResolution === 'rfc3263';

  // The API accepts transport in any case and port 0 (5060, or 5061 over
  // TLS); normalize both so the select matches and a save keeps the port the
  // worker actually dials. Under rfc3263 an empty transport is Auto and port
  // 0 stays empty: SRV decides both.
  const storedTransport = (editSipConfig?.transport || initialSipConfig?.transport || '').toLowerCase();
  const initialTransport = (storedTransport || (initialRfc3263 ? 'auto' : 'udp')) as SipFormFields['transport'];

  return {
    ...initialScheduledFormFields(monitor, initialData),
    host: editSipConfig?.host || initialSipConfig?.host || '',
    port:
      editSipConfig?.port || initialSipConfig?.port || (initialRfc3263 ? 0 : initialTransport === 'tls' ? 5061 : 5060),
    transport: initialTransport,
    resolution: initialResolution,
    nameserver: editSipConfig?.nameserver || initialSipConfig?.nameserver || '',
    method: normalizeSipMethod(editSipConfig?.method || initialSipConfig?.method),
    username: editSipConfig?.username || initialSipConfig?.username || '',
    password: editSipConfig?.password || initialSipConfig?.password || '',
    domain: editSipConfig?.domain || initialSipConfig?.domain || '',
    tls_skip_verify: editSipConfig?.tls_skip_verify ?? initialSipConfig?.tls_skip_verify ?? false,
    tls_server_name: editSipConfig?.tls_server_name || initialSipConfig?.tls_server_name || '',
    tls_min_days_valid:
      (editSipConfig?.tls_min_days_valid ?? initialSipConfig?.tls_min_days_valid)?.toString() ?? '',
    expected_status: editSipConfig?.expected_status?.toString()
      || initialSipConfig?.expected_status?.toString() || '',
    ...sipCallFormFields(loadedConfig?.call),
  };
}

export type SipFormState = ReturnType<typeof initialSipFormState>;

/** The SIP config fields of the form state, as buildSipConfig takes them. */
export const sipFieldsFromForm = (form: SipFormState): SipFormFields => ({
  host: form.host,
  port: form.port,
  transport: form.transport,
  resolution: form.resolution,
  nameserver: form.nameserver,
  method: form.method,
  username: form.username,
  password: form.password,
  domain: form.domain,
  tls_skip_verify: form.tls_skip_verify,
  tls_server_name: form.tls_server_name,
  tls_min_days_valid: form.tls_min_days_valid,
  expected_status: form.expected_status,
  call_target_kind: form.call_target_kind,
  call_number: form.call_number,
  call_target_uri: form.call_target_uri,
  call_from_user: form.call_from_user,
  call_from_display: form.call_from_display,
  call_from_domain: form.call_from_domain,
  call_mode: form.call_mode,
  call_hold_ms: form.call_hold_ms,
  call_warn_pdd_ms: form.call_warn_pdd_ms,
  call_max_pdd_ms: form.call_max_pdd_ms,
  call_warn_answer_ms: form.call_warn_answer_ms,
  call_max_answer_ms: form.call_max_answer_ms,
  call_sdp_address: form.call_sdp_address,
  call_probe_header: form.call_probe_header,
});

/** The request SipForm submits, built on `loadedConfig` (see sipLoadedConfig). */
export function buildSipRequest(form: SipFormState, loadedConfig: SIPMonitorConfig | undefined): CreateMonitorRequest {
  return buildScheduledMonitorRequest('sip', buildSipConfig(loadedConfig, sipFieldsFromForm(form)), form);
}
