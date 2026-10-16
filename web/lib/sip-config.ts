import { SIPMonitorConfig } from './types';

/** The SIP config fields SipForm edits, in their form-state shapes. */
export interface SipFormFields {
  host: string;
  port: number;
  transport: 'udp' | 'tcp' | 'tls';
  method: 'options' | 'register';
  username: string;
  password: string;
  domain: string;
  tls_skip_verify: boolean;
  tls_server_name: string;
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
  'expected_status',
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

  const config = {
    ...rest,
    host: form.host.trim(),
    port: form.port,
    transport: form.transport,
  } as SIPMonitorConfig;

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
  }

  if (form.expected_status) {
    const status = parseInt(form.expected_status, 10);
    if (!isNaN(status)) config.expected_status = status;
  }

  return config;
}
