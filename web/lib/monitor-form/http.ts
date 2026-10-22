import { HTTPMonitorConfig, HTTPStatusRange } from '../types';

// Pure HTTP (and DNS) helpers for MonitorForm: the form-state shapes, status
// rule parsing, and header rows. No React here, so lib tests can pin them.

export type HeaderKV = { key: string; value: string; reveal?: boolean };

export const isSensitiveHeaderName = (name: string) => {
  const n = name.trim().toLowerCase();
  if (!n) return false;
  if (n === 'authorization') return true;
  if (n === 'cookie') return true;
  if (n === 'x-api-key') return true;
  if (n === 'api-key') return true;
  if (n === 'x-auth-token') return true;
  if (n.includes('token')) return true;
  if (n.includes('secret')) return true;
  return false;
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

export const mapHeadersToRows = (headers?: Record<string, string>): HeaderKV[] => {
  if (!headers || Object.keys(headers).length === 0) return defaultHeaderRows();
  const rows = Object.entries(headers).map(([key, value]) => ({ key, value, reveal: false }));
  return rows.length > 0 ? rows : defaultHeaderRows();
};
