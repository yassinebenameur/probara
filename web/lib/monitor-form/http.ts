import type {
  HTTPBodyAssertion,
  HTTPHeaderAssertion,
  HTTPJSONAssertion,
  HTTPMonitorConfig,
  HTTPStatusRange,
} from '../types';
import {
  type FormErrors,
  type HeaderKV,
  hasIncompleteHeaderRow,
  headersFromRows,
  isInvalidOptionalInt,
  mapHeadersToRows,
  parseOptionalInt,
} from './common';

export interface HttpFormData {
  url: string;
  method: string;
  /** Comma-separated codes, ranges and classes, e.g. `200, 300-302, 2xx`. */
  status_rules: string;
  request_headers: HeaderKV[];
  body: string;
  body_assertions: HTTPBodyAssertion[];
  response_header_assertions: HTTPHeaderAssertion[];
  json_assertions: HTTPJSONAssertion[];
  max_latency_ms: string;
  follow_redirects: boolean;
  max_redirects: number;
  tls_skip_verify: boolean;
  tls_min_days_valid: string;
  tls_server_name: string;
  tls_ca_pem: string;
  collect_timing: boolean;
}

export const buildStatusRulesFromHTTPConfig = (cfg?: HTTPMonitorConfig): string => {
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

export type ParsedStatusRules = { codes: number[]; ranges: HTTPStatusRange[]; classes: string[]; error?: string };

export const parseStatusRules = (input: string): ParsedStatusRules => {
  const trimmed = input.trim();
  if (!trimmed) return { codes: [], ranges: [], classes: [] };

  const codes: number[] = [];
  const ranges: HTTPStatusRange[] = [];
  const classes: string[] = [];
  const fail = (error: string): ParsedStatusRules => ({ codes: [], ranges: [], classes: [], error });

  const tokens = trimmed.split(',').map((t) => t.trim()).filter(Boolean);
  for (const token of tokens) {
    if (/^[1-5]xx$/i.test(token)) {
      classes.push(token.toLowerCase());
      continue;
    }

    const rangeMatch = token.match(/^(\d{3})\s*-\s*(\d{3})$/);
    if (rangeMatch) {
      const min = parseInt(rangeMatch[1], 10);
      const max = parseInt(rangeMatch[2], 10);
      if (min < 100 || min > 599 || max < 100 || max > 599) return fail(`Invalid status range: ${token}`);
      if (min > max) return fail(`Invalid status range (min > max): ${token}`);
      ranges.push({ min, max });
      continue;
    }

    if (/^\d{3}$/.test(token)) {
      const code = parseInt(token, 10);
      if (code < 100 || code > 599) return fail(`Invalid status code: ${token}`);
      codes.push(code);
      continue;
    }

    return fail(`Invalid status rule: ${token}`);
  }

  return { codes, ranges, classes };
};

/**
 * Pre-assertion configs stored `expected_body_substring` / `expected_body_regex`;
 * they are shown (and re-saved) as body assertions.
 */
const initialBodyAssertions = (cfg?: HTTPMonitorConfig): HTTPBodyAssertion[] => {
  if (!cfg) return [];
  if (cfg.body_assertions && cfg.body_assertions.length > 0) return cfg.body_assertions;
  const legacy: HTTPBodyAssertion[] = [];
  if (cfg.expected_body_substring) legacy.push({ op: 'contains', value: cfg.expected_body_substring });
  if (cfg.expected_body_regex) legacy.push({ op: 'regex', value: cfg.expected_body_regex });
  return legacy;
};

export function initialHttpFormData(cfg?: HTTPMonitorConfig): HttpFormData {
  return {
    url: cfg?.url || '',
    method: cfg?.method || 'GET',
    status_rules: buildStatusRulesFromHTTPConfig(cfg),
    request_headers: mapHeadersToRows(cfg?.headers),
    body: cfg?.body || '',
    body_assertions: initialBodyAssertions(cfg),
    response_header_assertions: cfg?.response_header_assertions || [],
    json_assertions: cfg?.json_assertions || [],
    max_latency_ms: cfg?.max_latency_ms?.toString() || '',
    follow_redirects: cfg?.follow_redirects ?? true,
    max_redirects: cfg?.max_redirects ?? 10,
    tls_skip_verify: cfg?.tls_skip_verify ?? false,
    tls_min_days_valid: cfg?.tls_min_days_valid?.toString() || '',
    tls_server_name: cfg?.tls_server_name || '',
    tls_ca_pem: cfg?.tls_ca_pem || '',
    collect_timing: cfg?.collect_timing ?? false,
  };
}

export function validateHttpForm(data: HttpFormData): FormErrors {
  const errors: FormErrors = {};
  const url = data.url.trim();
  const isHTTPS = url.startsWith('https://');

  if (!url) {
    errors.url = 'URL is required';
  } else if (!url.startsWith('http://') && !isHTTPS) {
    errors.url = 'URL must start with http:// or https://';
  }

  const parsed = parseStatusRules(data.status_rules);
  if (parsed.error) errors.status_rules = parsed.error;

  if (isInvalidOptionalInt(data.max_latency_ms, 1)) {
    errors.max_latency_ms = 'Max latency must be a positive number (ms)';
  }

  if (data.max_redirects < 0) {
    errors.max_redirects = 'Max redirects must be 0 or greater';
  }

  if (isInvalidOptionalInt(data.tls_min_days_valid, 0)) {
    errors.tls_min_days_valid = 'TLS min days valid must be 0 or greater';
  } else if (data.tls_min_days_valid.trim() && !isHTTPS) {
    errors.tls_min_days_valid = 'TLS checks require an https:// URL';
  }

  const hasTLSSettings = data.tls_skip_verify || !!data.tls_ca_pem.trim() || !!data.tls_server_name.trim();
  if (hasTLSSettings && url && !isHTTPS) {
    errors.url = 'TLS settings require an https:// URL';
  }

  if (hasIncompleteHeaderRow(data.request_headers)) {
    errors.request_headers = 'Headers must have both a name and a value';
  }

  return errors;
}

/** Assumes `validateHttpForm` passed. Blank optional fields and empty assertions are omitted. */
export function buildHttpConfig(data: HttpFormData): HTTPMonitorConfig {
  const parsed = parseStatusRules(data.status_rules);
  const config: HTTPMonitorConfig = {
    url: data.url.trim(),
    method: data.method,
  };

  const headers = headersFromRows(data.request_headers);
  if (Object.keys(headers).length > 0) config.headers = headers;

  if (parsed.codes.length > 0) config.expected_statuses = parsed.codes;
  if (parsed.ranges.length > 0) config.expected_status_ranges = parsed.ranges;
  if (parsed.classes.length > 0) config.expected_status_classes = parsed.classes;

  if (data.body.trim()) config.body = data.body.trim();

  const bodyAssertions = data.body_assertions
    .map((a): HTTPBodyAssertion => ({
      op: a.op,
      value: a.value.trim(),
      ...(a.case_insensitive ? { case_insensitive: true } : {}),
    }))
    .filter((a) => a.value);
  if (bodyAssertions.length > 0) config.body_assertions = bodyAssertions;

  const headerAssertions = data.response_header_assertions
    .map((a): HTTPHeaderAssertion => ({
      name: a.name.trim(),
      op: a.op,
      ...(a.value && a.value.trim() ? { value: a.value } : {}),
      ...(a.case_insensitive ? { case_insensitive: true } : {}),
    }))
    .filter((a) => a.name);
  if (headerAssertions.length > 0) config.response_header_assertions = headerAssertions;

  const jsonAssertions = data.json_assertions
    .map((a): HTTPJSONAssertion => ({
      path: a.path.trim(),
      op: a.op,
      ...(a.value && a.value.trim() ? { value: a.value } : {}),
      ...(a.case_insensitive ? { case_insensitive: true } : {}),
    }))
    .filter((a) => a.path);
  if (jsonAssertions.length > 0) config.json_assertions = jsonAssertions;

  const maxLatency = parseOptionalInt(data.max_latency_ms);
  if (maxLatency !== undefined) config.max_latency_ms = maxLatency;

  config.follow_redirects = data.follow_redirects;
  config.max_redirects = data.max_redirects;
  config.collect_timing = data.collect_timing;

  if (data.tls_skip_verify) config.tls_skip_verify = true;
  const tlsDays = parseOptionalInt(data.tls_min_days_valid);
  if (tlsDays !== undefined) config.tls_min_days_valid = tlsDays;
  if (data.tls_server_name.trim()) config.tls_server_name = data.tls_server_name.trim();
  if (data.tls_ca_pem.trim()) config.tls_ca_pem = data.tls_ca_pem.trim();

  return config;
}
