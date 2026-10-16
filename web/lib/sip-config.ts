import { SIPCallConfig, SIPMonitorConfig } from './types';

/** The SIP config fields SipForm edits, in their form-state shapes. */
export interface SipFormFields {
  host: string;
  /** 0 means unset: from SRV under rfc3263 (the form requires a port otherwise). */
  port: number;
  /** 'auto' leaves the transport to NAPTR/SRV; offered only under rfc3263. */
  transport: 'udp' | 'tcp' | 'tls' | 'auto';
  resolution: 'a' | 'rfc3263';
  nameserver: string;
  method: 'options' | 'register' | 'invite';
  username: string;
  password: string;
  domain: string;
  tls_skip_verify: boolean;
  tls_server_name: string;
  tls_min_days_valid: string;
  expected_status: string;
  // The test call (method invite). Numeric fields are input strings; '' is unset.
  call_target_kind: 'number' | 'uri';
  call_number: string;
  call_target_uri: string;
  call_from_user: string;
  call_from_display: string;
  call_from_domain: string;
  call_mode: 'answer' | 'ring';
  call_hold_ms: string;
  call_warn_pdd_ms: string;
  call_max_pdd_ms: string;
  call_warn_answer_ms: string;
  call_max_answer_ms: string;
  call_sdp_address: string;
  call_probe_header: boolean;
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
  'call',
] as const;

// Keys of `call` the form owns; any other key of a loaded call survives.
const CALL_FORM_OWNED_KEYS = [
  'number',
  'target_uri',
  'from_user',
  'from_display',
  'from_domain',
  'mode',
  'hold_ms',
  'warn_pdd_ms',
  'max_pdd_ms',
  'warn_answer_ms',
  'max_answer_ms',
  'sdp_address',
  'probe_header',
] as const;

// Test-call bounds, mirroring shared/models/sip_call.go (the API enforces
// them; the form checks first so the message lands on the field).
export const SIP_CALL_MIN_INTERVAL_SECONDS = 60;
export const SIP_CALL_MAX_TIMEOUT_SECONDS = 20;
export const SIP_CALL_MAX_HOLD_MS = 10000;
const SIP_CALL_MIN_ANSWER_BUDGET_MS = 2000;
const SIP_CALL_USER = /^[0-9A-Za-z+*#._~-]{1,64}$/;
// How a target_uri user part may be written (Go's sipCallURIUserRaw): the
// unescaped number characters but '#', or percent-escapes.
const SIP_CALL_URI_USER_RAW = /^(?:[0-9A-Za-z+*._~-]|%[0-9A-Fa-f]{2})+$/;
const SIP_CALL_MAX_URI_BYTES = 512;
// shared/models/hostname.go's hostnameRE.
const HOSTNAME = /^([a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)*[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$/;

function isIPv6(s: string): boolean {
  if (!s.includes(':') || !/^[0-9A-Fa-f:.]+$/.test(s)) return false;
  try {
    new URL(`http://[${s}]/`);
    return true;
  } catch {
    return false;
  }
}

/**
 * A hostname or IP address without brackets or port: the TS twin of Go's
 * isHostOrIP (shared/models), which the API applies to domain (on a test
 * call), call.from_domain and call.sdp_address.
 */
export function isHostOrIP(value: string): boolean {
  return (value.length <= 253 && HOSTNAME.test(value)) || isIPv6(value);
}

/**
 * Why a test call's target URI is refused, or null when it is accepted: the
 * client mirror of Go's ParseSIPCallURI (shared/models/sip_call.go), so the
 * message lands on the field instead of coming back from the API.
 */
export function sipCallURIError(uri: string): string | null {
  if (new TextEncoder().encode(uri).length > SIP_CALL_MAX_URI_BYTES) return `At most ${SIP_CALL_MAX_URI_BYTES} bytes`;
  if (/[?<>"\s\u0000-\u001f\u007f]/.test(uri)) return 'No ?headers, spaces or quotes';
  const scheme = /^(sips?):/i.exec(uri);
  if (!scheme) return 'Must start with sip: or sips:';
  const rest = uri.slice(scheme[0].length);
  const at = rest.indexOf('@');
  if (at <= 0) return 'Must be a sip: or sips: URI with a user part (sip:user@host)';
  const user = rest.slice(0, at);
  const hostPart = rest.slice(at + 1);
  if (hostPart.includes('@')) return 'Must have a single @';
  if (/[;:]/.test(user)) return 'No ;parameters or :password in the user part';
  let decoded = '';
  try {
    decoded = decodeURIComponent(user);
  } catch {
    decoded = '';
  }
  if (!SIP_CALL_URI_USER_RAW.test(user) || !SIP_CALL_USER.test(decoded)) {
    return 'User part: 1-64 digits, letters and + * # . _ ~ - (# written %23)';
  }
  const [hostPort, ...params] = hostPart.split(';');
  if (params.some((p) => p.split('=')[0].trim().toLowerCase() === 'maddr')) return 'Must not carry a maddr parameter';
  let host = hostPort;
  let port = '';
  if (hostPort.startsWith('[')) {
    const end = hostPort.indexOf(']');
    if (end < 0) return 'Has an unterminated IPv6 host';
    host = hostPort.slice(1, end);
    const after = hostPort.slice(end + 1);
    if (after && !after.startsWith(':')) return 'Has an invalid host';
    port = after.slice(1);
    if (!isIPv6(host) || /^[\d.]+$/.test(host)) return 'Has an invalid IPv6 host';
  } else {
    const colon = hostPort.indexOf(':');
    if (colon >= 0) {
      host = hostPort.slice(0, colon);
      port = hostPort.slice(colon + 1);
    }
    if (!isHostOrIP(host) || host.includes(':')) return 'Host must be a hostname or IP address';
  }
  if (port && !(/^\d+$/.test(port) && Number(port) >= 1 && Number(port) <= 65535)) {
    return 'Port must be between 1 and 65535';
  }
  return null;
}

/**
 * Whether a monitor places real calls: a `sip` monitor whose method is
 * invite or that carries a call. The TS twin of Go's
 * models.IsSIPCallConfig, which the API's tenant-admin gate uses, so the UI
 * hides exactly what the server refuses. Both fields are non-secret.
 */
export function isSipCall(type: string | undefined, config: unknown): boolean {
  if (type !== 'sip' || !config || typeof config !== 'object') return false;
  const cfg = config as { method?: unknown; call?: unknown };
  return (
    (typeof cfg.method === 'string' && cfg.method.trim().toLowerCase() === 'invite') ||
    (cfg.call !== undefined && cfg.call !== null)
  );
}

/**
 * Who a test call rings, as the operator reads it: `number@domain` (the
 * domain, else the host, as the worker builds the Request-URI) or the
 * configured target URI. Null when the config names neither.
 */
export function sipCallTarget(config: SIPMonitorConfig | undefined): string | null {
  const call = config?.call;
  if (!call) return null;
  if (call.target_uri?.trim()) return call.target_uri.trim();
  if (!call.number?.trim()) return null;
  const domain = config?.domain?.trim() || config?.host?.trim();
  return domain ? `${call.number.trim()}@${domain}` : call.number.trim();
}

/**
 * Calls a test-call monitor places per day: one per check per location (the
 * default fleet counts as one), rounded.
 */
export function sipCallsPerDay(intervalSeconds: number, locationCount: number): number {
  if (!(intervalSeconds > 0)) return 0;
  return Math.round((86400 / intervalSeconds) * Math.max(1, locationCount));
}

/** The call form fields for a loaded call config (or blank ones for none). */
export function sipCallFormFields(call: SIPCallConfig | undefined): Pick<
  SipFormFields,
  | 'call_target_kind'
  | 'call_number'
  | 'call_target_uri'
  | 'call_from_user'
  | 'call_from_display'
  | 'call_from_domain'
  | 'call_mode'
  | 'call_hold_ms'
  | 'call_warn_pdd_ms'
  | 'call_max_pdd_ms'
  | 'call_warn_answer_ms'
  | 'call_max_answer_ms'
  | 'call_sdp_address'
  | 'call_probe_header'
> {
  const num = (n: number | undefined) => (n === undefined || n === null ? '' : String(n));
  return {
    call_target_kind: call?.target_uri ? 'uri' : 'number',
    call_number: call?.number ?? '',
    call_target_uri: call?.target_uri ?? '',
    call_from_user: call?.from_user ?? '',
    call_from_display: call?.from_display ?? '',
    call_from_domain: call?.from_domain ?? '',
    call_mode: call?.mode?.toLowerCase() === 'ring' ? 'ring' : 'answer',
    call_hold_ms: call?.hold_ms ? String(call.hold_ms) : '',
    call_warn_pdd_ms: num(call?.warn_pdd_ms),
    call_max_pdd_ms: num(call?.max_pdd_ms),
    call_warn_answer_ms: num(call?.warn_answer_ms),
    call_max_answer_ms: num(call?.max_answer_ms),
    call_sdp_address: call?.sdp_address ?? '',
    call_probe_header: call?.probe_header ?? false,
  };
}

/** A whole number of 0 or more as typed (digits only), else null. */
export const parseWhole = (value: string): number | null => {
  const trimmed = value.trim();
  if (!/^\d+$/.test(trimmed)) return null;
  return parseInt(trimmed, 10);
};

/**
 * Form-side checks for a test call, keyed by form field. The API runs the
 * same rules (shared/models/sip_call.go); these put the message on the
 * field before a round trip. Empty when the method is not invite.
 */
export function validateSipCallFields(
  form: SipFormFields,
  intervalSeconds: number,
  timeoutSeconds: number,
): Record<string, string> {
  const errors: Record<string, string> = {};
  if (form.method !== 'invite') return errors;
  const ring = form.call_mode === 'ring';

  if (form.call_target_kind === 'uri') {
    const uri = form.call_target_uri.trim();
    const uriError = uri ? sipCallURIError(uri) : null;
    if (!uri) errors.call_target_uri = 'Enter the sip: or sips: URI to call';
    else if (uriError) errors.call_target_uri = uriError;
    else if (/^sips:/i.test(uri) && form.transport !== 'tls') errors.call_target_uri = 'A sips: URI requires transport TLS';
  } else {
    const number = form.call_number.trim();
    if (!number) errors.call_number = 'Enter the number to call';
    else if (!SIP_CALL_USER.test(number)) errors.call_number = 'Up to 64 digits, letters and + * # . _ ~ -';
  }
  if (form.call_from_user.trim() && !SIP_CALL_USER.test(form.call_from_user.trim())) {
    errors.call_from_user = 'Up to 64 digits, letters and + * # . _ ~ -';
  }
  // Test-call host fields are bare hosts (the API refuses anything else):
  // domain goes into the Request-URI, To and From.
  if (form.domain.trim() && !isHostOrIP(form.domain.trim())) {
    errors.domain = 'Must be a hostname or IP address (no port, parameters or headers)';
  }
  if (form.call_from_domain.trim() && !isHostOrIP(form.call_from_domain.trim())) {
    errors.call_from_domain = 'Must be a hostname or IP address';
  }
  if (form.call_sdp_address.trim() && !isHostOrIP(form.call_sdp_address.trim())) {
    errors.call_sdp_address = 'Must be a hostname or IP address (no port)';
  }
  // Counted in code points like the server's []rune (an emoji is one), on
  // the trimmed value the save sends; control characters are refused too.
  const display = form.call_from_display.trim();
  if (/["\\\u0000-\u001f\u007f]/.test(display) || [...display].length > 64) {
    errors.call_from_display = 'Up to 64 printable characters, without " or \\';
  }

  let hold = 0;
  if (!ring && form.call_hold_ms.trim()) {
    const parsed = parseWhole(form.call_hold_ms);
    if (parsed === null || parsed > SIP_CALL_MAX_HOLD_MS) {
      errors.call_hold_ms = `A whole number of milliseconds, 0 to ${SIP_CALL_MAX_HOLD_MS}`;
    } else {
      hold = parsed;
    }
  }

  const thresholds = (warnKey: keyof SipFormFields, maxKey: keyof SipFormFields) => {
    const warnRaw = String(form[warnKey]).trim();
    const maxRaw = String(form[maxKey]).trim();
    const warn = warnRaw ? parseWhole(warnRaw) : undefined;
    const max = maxRaw ? parseWhole(maxRaw) : undefined;
    if (warn === null || warn === 0) errors[warnKey] = 'A whole number of milliseconds above 0';
    if (max === null || max === 0) errors[maxKey] = 'A whole number of milliseconds above 0';
    if (warn && max && warn >= max) errors[warnKey] = 'Must be less than the failure threshold';
  };
  thresholds('call_warn_pdd_ms', 'call_max_pdd_ms');
  if (!ring) thresholds('call_warn_answer_ms', 'call_max_answer_ms');

  if (!ring && form.expected_status.trim()) {
    const status = parseWhole(form.expected_status);
    if (status === null || status < 200 || status > 699) {
      errors.expected_status = 'A final response, 200 to 699';
    }
  }

  if (intervalSeconds < SIP_CALL_MIN_INTERVAL_SECONDS) {
    errors.interval_seconds = `At least ${SIP_CALL_MIN_INTERVAL_SECONDS} seconds for a test call: each check places a real call`;
  }
  if (timeoutSeconds > SIP_CALL_MAX_TIMEOUT_SECONDS) {
    errors.timeout_seconds = `At most ${SIP_CALL_MAX_TIMEOUT_SECONDS} seconds for a test call`;
  } else if (!errors.call_hold_ms && hold + SIP_CALL_MIN_ANSWER_BUDGET_MS > timeoutSeconds * 1000) {
    errors.call_hold_ms = `The hold plus ${SIP_CALL_MIN_ANSWER_BUDGET_MS / 1000} s to ring and answer must fit in the ${timeoutSeconds} s timeout`;
  }
  return errors;
}

/**
 * The call object for a test-call save, built on the loaded call so keys the
 * form does not render survive. Mode is always explicit; fields the mode
 * cannot use (ring mode's hold and answer thresholds) are dropped, as the
 * API refuses them.
 */
function buildSipCallConfig(loaded: SIPCallConfig | undefined, form: SipFormFields): SIPCallConfig {
  const rest: Record<string, unknown> = { ...(loaded ?? {}) };
  for (const key of CALL_FORM_OWNED_KEYS) delete rest[key];
  const call = { ...rest } as SIPCallConfig;
  const ring = form.call_mode === 'ring';

  if (form.call_target_kind === 'uri') call.target_uri = form.call_target_uri.trim();
  else call.number = form.call_number.trim();
  if (form.call_from_user.trim()) call.from_user = form.call_from_user.trim();
  if (form.call_from_display.trim()) call.from_display = form.call_from_display.trim();
  if (form.call_from_domain.trim()) call.from_domain = form.call_from_domain.trim();
  call.mode = ring ? 'ring' : 'answer';

  const whole = (value: string) => {
    const n = parseWhole(value);
    return n === null ? undefined : n;
  };
  if (!ring) {
    const hold = whole(form.call_hold_ms);
    if (hold) call.hold_ms = hold;
  }
  const setThreshold = (key: 'warn_pdd_ms' | 'max_pdd_ms' | 'warn_answer_ms' | 'max_answer_ms', value: string) => {
    const n = whole(value);
    if (n !== undefined) call[key] = n;
  };
  setThreshold('warn_pdd_ms', form.call_warn_pdd_ms);
  setThreshold('max_pdd_ms', form.call_max_pdd_ms);
  if (!ring) {
    setThreshold('warn_answer_ms', form.call_warn_answer_ms);
    setThreshold('max_answer_ms', form.call_max_answer_ms);
  }
  if (form.call_sdp_address.trim()) call.sdp_address = form.call_sdp_address.trim();
  if (form.call_probe_header) call.probe_header = true;
  return call;
}

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
    // The form validates with the same parser, so what passed is saved as
    // typed ("1e2" or "1.5" never pass, and never save as 1).
    const minDays = parseWhole(form.tls_min_days_valid);
    if (minDays !== null) config.tls_min_days_valid = minDays;
  }

  // Ring mode succeeds on ringing, never on a final, so the API refuses an
  // expected status there.
  if (form.expected_status && !(form.method === 'invite' && form.call_mode === 'ring')) {
    const status = parseInt(form.expected_status, 10);
    if (!isNaN(status)) config.expected_status = status;
  }

  // The call exists exactly when the method is invite (the API requires
  // both or neither).
  if (form.method === 'invite') config.call = buildSipCallConfig(loaded?.call, form);

  return config;
}
