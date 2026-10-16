import { SIPMonitorConfig } from './types';

/** The SIP config fields SipForm edits, in their form-state shapes. */
export interface SipFormFields {
  host: string;
  /** 0 means unset: from SRV under rfc3263 (the form requires a port otherwise). */
  port: number;
  /** 'auto' leaves the transport to SRV; offered only under rfc3263. */
  transport: 'udp' | 'tcp' | 'tls' | 'auto';
  resolution: 'a' | 'rfc3263';
  nameserver: string;
  method: 'options' | 'register';
  username: string;
  password: string;
  domain: string;
  tls_skip_verify: boolean;
  tls_server_name: string;
  tls_min_days_valid: string;
  expected_status: string;
}

// Keys SipForm owns: each is rewritten from form state on save, so a field
// the user cleared is dropped rather than resurrected from the loaded config.
const FORM_OWNED_KEYS = [
  'host',
  'port',
  'transport',
  'method',
  'username',
  'password',
  'domain',
  'tls_skip_verify',
  'tls_server_name',
  'tls_min_days_valid',
  'expected_status',
  'resolution',
  'nameserver',
] as const;

/**
 * Builds the SIP config SipForm submits. It starts from the loaded config
 * (the stored monitor's on edit, the clone source's on create) so any key
 * the form does not render — one set through the API or import, or added by
 * a newer release — survives a save, because an update replaces the whole
 * stored config. Only the form-owned keys are rebuilt from form state.
 *
 * Password follows the monitor-secret merge contract: the masked `***` the
 * form loaded means "keep stored", and dropping auth on a config that had a
 * password submits `''`, since an omitted secret is kept server-side.
 */
export function buildSipConfig(
  loaded: SIPMonitorConfig | undefined,
  form: SipFormFields,
): SIPMonitorConfig {
  const rest: Record<string, unknown> = { ...(loaded ?? {}) };
  for (const key of FORM_OWNED_KEYS) delete rest[key];

  // "Locate via DNS" is RFC 3263 resolution: port and transport may be left
  // to SRV (0 and ''). Without it the worker dials the host's A/AAAA
  // records, so an Auto transport cannot reach the API from here.
  const rfc3263 = form.resolution === 'rfc3263';
  const config = {
    ...rest,
    host: form.host.trim(),
    port: form.port,
    transport: form.transport === 'auto' ? (rfc3263 ? '' : 'udp') : form.transport,
  } as SIPMonitorConfig;
  if (rfc3263) {
    config.resolution = 'rfc3263';
    if (form.nameserver.trim()) config.nameserver = form.nameserver.trim();
  }

  if (form.method !== 'options') config.method = form.method;
  if (form.username.trim()) {
    config.username = form.username.trim();
    config.password = form.password;
  } else if (loaded?.password) {
    config.password = '';
  }
  if (form.domain.trim()) config.domain = form.domain.trim();
  if (form.transport === 'tls') {
    if (form.tls_skip_verify) config.tls_skip_verify = true;
    if (form.tls_server_name.trim()) config.tls_server_name = form.tls_server_name.trim();
    // The certificate is only seen over TLS; the API rejects the threshold
    // on any other transport, so switching away drops it.
    const minDays = parseInt(form.tls_min_days_valid, 10);
    if (form.tls_min_days_valid.trim() && !isNaN(minDays)) config.tls_min_days_valid = minDays;
  }

  if (form.expected_status) {
    const status = parseInt(form.expected_status, 10);
    if (!isNaN(status)) config.expected_status = status;
  }

  return config;
}
