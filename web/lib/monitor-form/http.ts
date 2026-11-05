import { HTTPMonitorConfig, HTTPStatusRange } from '../types';
import { SecretKV, plainRowsFromMap, secretRowsFromMap } from './secret-map';

// Pure HTTP (and DNS) helpers for MonitorForm: the form-state shapes, status
// rule parsing, and header rows. No React here, so lib tests can pin them.

/** A request header row; `secret` rows submit to `secret_headers`. */
export type HeaderKV = SecretKV;

const SENSITIVE_HEADER_EXACT = new Set([
  'authorization',
  'proxy-authorization',
  'cookie',
  'x-api-key',
  'api-key',
  'x-auth-token',
]);
const SENSITIVE_HEADER_MARKERS = ['token', 'secret', 'password', 'apikey', 'api-key'];

/**
 * Whether a header name conventionally carries a credential. TS twin of
 * IsSensitiveHeaderName in shared/secrets/sensitive_headers.go: the API moves
 * such headers into the encrypted `secret_headers` map on every write, so the
 * form shows them as secret (locked on) to match. Keep the two rules in step;
 * shared/secrets/testdata/sensitive_header_names.json is read by both test
 * suites (lib/monitor-form-http.test.ts here) and pins the pair.
 */
export const isSensitiveHeaderName = (name: string): boolean => {
  const n = name.trim().toLowerCase();
  if (!n) return false;
  if (SENSITIVE_HEADER_EXACT.has(n)) return true;
  return SENSITIVE_HEADER_MARKERS.some((marker) => n.includes(marker));
};

export const buildStatusRulesFromHTTPConfig = (cfg?: HTTPMonitorConfig) => {
  if (!cfg) return '';
  if (cfg.expected_status !== undefined) return String(cfg.expected_status);

  const parts: string[] = [];
  if (cfg.expected_statuses && cfg.expected_statuses.length > 0) {
    parts.push(cfg.expected_statuses.join(','));
  }
  if (cfg.expected_status_ranges && cfg.expected_status_ranges.length > 0) {
    parts.push(...cfg.expected_status_ranges.map((r) => `${r.min}-${r.max}`));
  }
  if (cfg.expected_status_classes && cfg.expected_status_classes.length > 0) {
    parts.push(...cfg.expected_status_classes);
  }
  return parts.join(', ');
};

export const parseStatusRules = (input: string): { codes: number[]; ranges: HTTPStatusRange[]; classes: string[]; error?: string } => {
  const trimmed = input.trim();
  if (!trimmed) return { codes: [], ranges: [], classes: [] };

  const codes: number[] = [];
  const ranges: HTTPStatusRange[] = [];
  const classes: string[] = [];

  const tokens = trimmed.split(',').map(t => t.trim()).filter(Boolean);
  for (const token of tokens) {
    if (/^[1-5]xx$/i.test(token)) {
      classes.push(token.toLowerCase());
      continue;
    }

    const rangeMatch = token.match(/^(\d{3})\s*-\s*(\d{3})$/);
    if (rangeMatch) {
      const min = parseInt(rangeMatch[1], 10);
      const max = parseInt(rangeMatch[2], 10);
      if (min < 100 || min > 599 || max < 100 || max > 599) {
        return { codes: [], ranges: [], classes: [], error: `Invalid status range: ${token}` };
      }
      if (min > max) {
        return { codes: [], ranges: [], classes: [], error: `Invalid status range (min > max): ${token}` };
      }
      ranges.push({ min, max });
      continue;
    }

    if (/^\d{3}$/.test(token)) {
      const code = parseInt(token, 10);
      if (code < 100 || code > 599) {
        return { codes: [], ranges: [], classes: [], error: `Invalid status code: ${token}` };
      }
      codes.push(code);
      continue;
    }

    return { codes: [], ranges: [], classes: [], error: `Invalid status rule: ${token}` };
  }

  return { codes, ranges, classes };
};

export const parseDNSExpectedAnswers = (input: string): string[] => {
  return input
    .split(/[\n,]/)
    .map((item) => item.trim())
    .filter((item) => item.length > 0);
};

export const defaultHeaderRows = (): HeaderKV[] => [{ key: '', value: '', reveal: false }];

/**
 * Header rows for a loaded config: plain `headers`, then `secretHeaders`
 * (masked on read; a stored value is only marked in edit mode). A legacy
 * credential-named header still in plain `headers` loads as a plain row with
 * its value; the form submits it as secret, which migrates it.
 */
export const mapHeadersToRows = (
  headers?: Record<string, string>,
  secretHeaders?: Record<string, string>,
  isEditMode = false
): HeaderKV[] => {
  const rows = [...plainRowsFromMap(headers), ...secretRowsFromMap(secretHeaders, isEditMode)];
  return rows.length > 0 ? rows : defaultHeaderRows();
};

/** Failure-response capture budget (shared/models HTTPCaptureDefaultBodyBytes / HTTPCaptureMaxBodyBytes). */
export const HTTP_CAPTURE_DEFAULT_BODY_BYTES = 2048;
export const HTTP_CAPTURE_MAX_BODY_BYTES = 8192;
