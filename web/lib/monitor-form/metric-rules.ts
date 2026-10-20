import { MetricRule } from '../types';
import { isUtilizationMetric, percentToRatio, ratioToPercent } from '../metrics';

// AgentForm's metric rule drafts (MetricRuleBuilder edits them) and their
// parsing into metric_rules, mirroring the server's validation.

// Client-side mirror of the server's metric_rules validation.
export const MAX_RULES = 50;
const MAX_METRIC_NAME_LENGTH = 255;
const MAX_ATTRIBUTE_FILTERS = 8;
const MAX_ATTRIBUTE_TOKEN_LENGTH = 128;
const MAX_FOR_DURATION_SECONDS = 86400;
const METRIC_NAME_PATTERN = /^[a-zA-Z_][a-zA-Z0-9_./-]*$/;
const FORBIDDEN_ATTRIBUTE_CHARS = /[,}=]/;

export interface MetricRuleDraft {
  metric_name: string;
  filters: Array<{ key: string; value: string }>;
  operator: '>=' | '<=';
  // Display units: percent for *.utilization metrics, native otherwise.
  threshold: string;
  for_duration_seconds: number;
}

function thresholdToInput(rule: MetricRule): string {
  if (isUtilizationMetric(rule.metric_name)) {
    return String(ratioToPercent(rule.threshold));
  }
  return String(rule.threshold);
}

export function draftsFromRules(rules?: MetricRule[]): MetricRuleDraft[] {
  return (rules || []).map((rule) => ({
    metric_name: rule.metric_name,
    filters: Object.entries(rule.attribute_filters || {}).map(([key, value]) => ({ key, value })),
    operator: rule.operator,
    threshold: thresholdToInput(rule),
    for_duration_seconds: rule.for_duration_seconds ?? 0,
  }));
}

export function canonicalRuleKey(metricName: string, filters: Record<string, string>): string {
  const parts = Object.keys(filters)
    .sort()
    .map((key) => `${key}=${filters[key]}`);
  return `${metricName}{${parts.join(',')}}`;
}

// Parses drafts into MetricRule[] mirroring the server's validation.
// Returns per-row error messages keyed by row index.
export function rulesFromDrafts(drafts: MetricRuleDraft[]): {
  rules: MetricRule[];
  errors: Record<number, string>;
} {
  const rules: MetricRule[] = [];
  const errors: Record<number, string> = {};
  const seen = new Set<string>();

  if (drafts.length > MAX_RULES) {
    errors[MAX_RULES] = `At most ${MAX_RULES} rules are allowed`;
  }

  drafts.forEach((draft, index) => {
    const name = draft.metric_name.trim();
    if (!name) {
      errors[index] = 'Metric name is required';
      return;
    }
    if (name.length > MAX_METRIC_NAME_LENGTH || !METRIC_NAME_PATTERN.test(name)) {
      errors[index] = 'Metric name must match ^[a-zA-Z_][a-zA-Z0-9_./-]*$';
      return;
    }

    if (draft.filters.length > MAX_ATTRIBUTE_FILTERS) {
      errors[index] = `At most ${MAX_ATTRIBUTE_FILTERS} attribute filters per rule`;
      return;
    }
    const filters: Record<string, string> = {};
    for (const filter of draft.filters) {
      const key = filter.key.trim();
      const value = filter.value.trim();
      if (!key || !value) {
        errors[index] = 'Attribute filters need both a key and a value';
        return;
      }
      if (key.length > MAX_ATTRIBUTE_TOKEN_LENGTH || value.length > MAX_ATTRIBUTE_TOKEN_LENGTH) {
        errors[index] = `Attribute keys/values are limited to ${MAX_ATTRIBUTE_TOKEN_LENGTH} characters`;
        return;
      }
      if (FORBIDDEN_ATTRIBUTE_CHARS.test(key) || FORBIDDEN_ATTRIBUTE_CHARS.test(value)) {
        errors[index] = 'Attribute keys/values must not contain "," "}" or "="';
        return;
      }
      if (key in filters) {
        errors[index] = `Duplicate attribute key "${key}"`;
        return;
      }
      filters[key] = value;
    }

    const rawThreshold = draft.threshold.trim();
    const parsed = Number(rawThreshold);
    if (rawThreshold === '' || !Number.isFinite(parsed)) {
      errors[index] = 'Threshold is required';
      return;
    }
    let threshold = parsed;
    if (isUtilizationMetric(name)) {
      if (parsed <= 0 || parsed > 100) {
        errors[index] = 'Utilization thresholds are percentages between 0 and 100';
        return;
      }
      threshold = percentToRatio(parsed);
    }

    if (
      draft.for_duration_seconds < 0 ||
      draft.for_duration_seconds > MAX_FOR_DURATION_SECONDS
    ) {
      errors[index] = `Sustained duration must be between 0 and ${MAX_FOR_DURATION_SECONDS} seconds`;
      return;
    }

    const key = canonicalRuleKey(name, filters);
    if (seen.has(key)) {
      errors[index] = 'Duplicate rule: same metric and attribute filters as another rule';
      return;
    }
    seen.add(key);

    const rule: MetricRule = { metric_name: name, operator: draft.operator, threshold };
    if (Object.keys(filters).length > 0) rule.attribute_filters = filters;
    if (draft.for_duration_seconds > 0) rule.for_duration_seconds = draft.for_duration_seconds;
    rules.push(rule);
  });

  return { rules, errors };
}
