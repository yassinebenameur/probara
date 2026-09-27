import type {
  SyntheticAPIAssertionConfig,
  SyntheticAPIAssertionTarget,
  SyntheticAPIExtractConfig,
  SyntheticAPIMonitorConfig,
  SyntheticAPIStepConfig,
  SyntheticFailureMode,
} from '../types';
import {
  type FormErrors,
  type HeaderKV,
  type VariableKV,
  defaultHeaderRows,
  emptyVariableRow,
  hasIncompleteHeaderRow,
  hasKeylessVariableRow,
  headersFromRows,
  isHttpUrl,
  isInvalidOptionalInt,
  mapHeadersToRows,
  mapVariablesToRows,
  parseOptionalInt,
  variablesFromRows,
} from './common';

export type SyntheticAPIAssertionDraft = {
  target: SyntheticAPIAssertionTarget;
  op: string;
  path: string;
  /** Plain text or a JSON literal; see `parseAssertionValueInput`. */
  value_input: string;
};

export type SyntheticAPIExtractDraft = {
  name: string;
  from: SyntheticAPIExtractConfig['from'];
  path: string;
  sensitive: boolean;
};

export type SyntheticAPIStepDraft = {
  id: string;
  name: string;
  method: string;
  url: string;
  headers: HeaderKV[];
  body: string;
  timeout_seconds: string;
  follow_redirects: boolean;
  max_redirects: string;
  assertions: SyntheticAPIAssertionDraft[];
  extracts: SyntheticAPIExtractDraft[];
};

export interface SyntheticAPIFormData {
  base_url: string;
  failure_mode: SyntheticFailureMode;
  variables: VariableKV[];
  steps: SyntheticAPIStepDraft[];
}

export type SyntheticAPITemplate = 'single_health' | 'health_auth';

/** Error keys owned by the synthetic API editor. */
export const SYNTHETIC_API_ERROR_KEYS = ['synthetic_api_steps', 'synthetic_api_variables', 'synthetic_api_base_url'];

export const syntheticAssertionOpsByTarget: Record<SyntheticAPIAssertionTarget, string[]> = {
  status: ['equals', 'not_equals', 'in'],
  header: ['exists', 'equals', 'not_equals', 'contains', 'not_contains', 'regex', 'not_regex'],
  body: ['exists', 'equals', 'not_equals', 'contains', 'not_contains', 'regex', 'not_regex'],
  json: ['exists', 'equals', 'not_equals', 'contains', 'not_contains', 'regex', 'not_regex', 'number_gt', 'number_gte', 'number_lt', 'number_lte', 'bool_is'],
};

const NUMERIC_JSON_OPS = ['number_gt', 'number_gte', 'number_lt', 'number_lte'];

export const defaultSyntheticAssertionForTarget = (target: SyntheticAPIAssertionTarget): SyntheticAPIAssertionDraft => {
  if (target === 'status') return { target, op: 'equals', path: '', value_input: '200' };
  if (target === 'json') return { target, op: 'exists', path: 'data.ok', value_input: '' };
  if (target === 'header') return { target, op: 'exists', path: 'Content-Type', value_input: '' };
  return { target, op: 'contains', path: '', value_input: 'ok' };
};

export const defaultSyntheticAPIAssertion = (): SyntheticAPIAssertionDraft => defaultSyntheticAssertionForTarget('status');

export const defaultSyntheticAPIExtract = (): SyntheticAPIExtractDraft => ({
  name: '',
  from: 'json',
  path: '',
  sensitive: false,
});

export const normalizeAssertionTarget = (target: string): SyntheticAPIAssertionTarget => {
  if (target === 'status' || target === 'header' || target === 'body' || target === 'json') return target;
  return 'status';
};

export const assertionPathRequired = (target: SyntheticAPIAssertionTarget): boolean =>
  target === 'header' || target === 'json';

export const assertionValueRequired = (target: SyntheticAPIAssertionTarget, op: string): boolean => {
  if (target === 'status') return true;
  return op.trim().toLowerCase() !== 'exists';
};

/** JSON literals (`200`, `true`, `[200,201]`) parse as JSON; anything else stays a string. */
export const parseAssertionValueInput = (input: string): unknown => {
  const trimmed = input.trim();
  if (!trimmed) return undefined;
  try {
    return JSON.parse(trimmed);
  } catch {
    return trimmed;
  }
};

const isIntegerLike = (value: unknown): boolean => {
  if (typeof value === 'number') return Number.isInteger(value);
  if (typeof value === 'string') return /^-?\d+$/.test(value.trim());
  return false;
};

const isNumberLike = (value: unknown): boolean => {
  if (typeof value === 'number') return Number.isFinite(value);
  if (typeof value === 'string') return value.trim() !== '' && !Number.isNaN(Number(value));
  return false;
};

const isBooleanLike = (value: unknown): boolean => {
  if (typeof value === 'boolean') return true;
  if (typeof value === 'string') {
    const normalized = value.trim().toLowerCase();
    return normalized === 'true' || normalized === 'false';
  }
  return false;
};

export const assertionValuePlaceholder = (target: SyntheticAPIAssertionTarget, op: string): string => {
  if (!assertionValueRequired(target, op)) return '';
  if (target === 'status') return op === 'in' ? '[200, 201]' : '200';
  if (target === 'json') {
    if (op === 'bool_is') return 'true';
    if (NUMERIC_JSON_OPS.includes(op)) return '100';
    return '"ok"';
  }
  return 'expected value';
};

const stringifyAssertionValueInput = (value: unknown): string => {
  if (value === undefined || value === null) return '';
  if (typeof value === 'string') return value;
  if (typeof value === 'number' || typeof value === 'boolean') return String(value);
  try {
    return JSON.stringify(value);
  } catch {
    return String(value);
  }
};

const mapAssertionsToDraft = (assertions?: SyntheticAPIAssertionConfig[]): SyntheticAPIAssertionDraft[] =>
  (assertions || []).map((assertion) => {
    const target = normalizeAssertionTarget(assertion.target || 'status');
    const ops = syntheticAssertionOpsByTarget[target];
    const normalizedOp = (assertion.op || '').trim().toLowerCase();
    return {
      target,
      op: ops.includes(normalizedOp) ? normalizedOp : ops[0],
      path: assertion.path || '',
      value_input: stringifyAssertionValueInput(assertion.value),
    };
  });

const mapExtractsToDraft = (extracts?: SyntheticAPIExtractConfig[]): SyntheticAPIExtractDraft[] =>
  (extracts || []).map((extract) => ({
    name: extract.name || '',
    from: extract.from === 'header' ? 'header' : 'json',
    path: extract.path || '',
    sensitive: !!extract.sensitive,
  }));

export const defaultSyntheticAPIStep = (index: number): SyntheticAPIStepDraft => ({
  id: `step_${index}`,
  name: '',
  method: 'GET',
  url: '',
  headers: defaultHeaderRows(),
  body: '',
  timeout_seconds: '',
  follow_redirects: true,
  max_redirects: '',
  assertions: [defaultSyntheticAPIAssertion()],
  extracts: [],
});

const mapStepsToDraft = (steps?: SyntheticAPIStepConfig[]): SyntheticAPIStepDraft[] => {
  if (!steps || steps.length === 0) {
    return [{ ...defaultSyntheticAPIStep(1), id: 'health', url: '/health' }];
  }
  return steps.map((step, idx) => ({
    id: step.id || `step_${idx + 1}`,
    name: step.name || '',
    method: step.request?.method || 'GET',
    url: step.request?.url || '',
    headers: mapHeadersToRows(step.request?.headers),
    body: step.request?.body || '',
    timeout_seconds: step.request?.timeout_seconds ? String(step.request.timeout_seconds) : '',
    follow_redirects: step.request?.follow_redirects ?? true,
    max_redirects: step.request?.max_redirects !== undefined ? String(step.request.max_redirects) : '',
    assertions: mapAssertionsToDraft(step.assert),
    extracts: mapExtractsToDraft(step.extract),
  }));
};

export function initialSyntheticAPIFormData(cfg?: SyntheticAPIMonitorConfig): SyntheticAPIFormData {
  return {
    base_url: cfg?.base_url || '',
    failure_mode: cfg?.failure_mode || 'fail_fast',
    variables: mapVariablesToRows(cfg?.variables),
    steps: mapStepsToDraft(cfg?.steps),
  };
}

/** Saved journeys using variables, headers, tuning, assertions or extracts open in Advanced mode. */
export const hasAdvancedSyntheticAPIConfig = (cfg?: SyntheticAPIMonitorConfig): boolean => {
  if (!cfg) return false;
  return (
    Object.keys(cfg.variables || {}).length > 0 ||
    (cfg.steps || []).some(
      (step) =>
        Object.keys(step.request?.headers || {}).length > 0 ||
        Boolean(step.request?.timeout_seconds) ||
        Boolean(step.request?.max_redirects) ||
        (step.assert || []).length > 0 ||
        (step.extract || []).length > 0
    )
  );
};

const healthStep = (): SyntheticAPIStepDraft => ({
  ...defaultSyntheticAPIStep(1),
  id: 'health',
  name: 'Health endpoint',
  method: 'GET',
  url: '/health',
  assertions: [{ target: 'status', op: 'equals', path: '', value_input: '200' }],
});

/** Quick-start journeys; they replace the whole editor content. */
export function syntheticAPITemplate(template: SyntheticAPITemplate): SyntheticAPIFormData {
  if (template === 'single_health') {
    return {
      base_url: 'https://api.example.com',
      failure_mode: 'fail_fast',
      variables: [emptyVariableRow()],
      steps: [healthStep()],
    };
  }

  return {
    base_url: 'https://api.example.com',
    failure_mode: 'fail_fast',
    variables: [
      { key: 'email', value: 'monitor@example.com' },
      { key: 'password', value: 'change-me' },
    ],
    steps: [
      healthStep(),
      {
        ...defaultSyntheticAPIStep(2),
        id: 'login',
        name: 'Login',
        method: 'POST',
        url: '/auth/login',
        headers: [{ key: 'Content-Type', value: 'application/json', reveal: false }],
        body: '{"email":"{{email}}","password":"{{password}}"}',
        assertions: [
          { target: 'status', op: 'in', path: '', value_input: '[200, 201]' },
          { target: 'json', op: 'exists', path: 'token', value_input: '' },
        ],
        extracts: [{ name: 'token', from: 'json', path: 'token', sensitive: true }],
      },
      {
        ...defaultSyntheticAPIStep(3),
        id: 'profile',
        name: 'Fetch profile',
        method: 'GET',
        url: '/me',
        headers: [{ key: 'Authorization', value: 'Bearer {{token}}', reveal: false }],
        assertions: [{ target: 'status', op: 'equals', path: '', value_input: '200' }],
      },
    ],
  };
}

/** First problem with one assertion, phrased without the step/assertion prefix. */
function assertionProblem(assertion: SyntheticAPIAssertionDraft): string | undefined {
  const target = normalizeAssertionTarget(assertion.target);
  const op = assertion.op.trim().toLowerCase();

  if (!op || !syntheticAssertionOpsByTarget[target].includes(op)) return 'has invalid operator';
  if (assertionPathRequired(target) && !assertion.path.trim()) return 'requires a path';

  const value = parseAssertionValueInput(assertion.value_input);
  if (assertionValueRequired(target, op) && (value === undefined || (typeof value === 'string' && !value.trim()))) {
    return 'requires a value';
  }

  if (target === 'status') {
    if (op === 'in') {
      if (!Array.isArray(value) || value.length === 0 || value.some((v) => !isIntegerLike(v))) {
        return 'expects a non-empty array of status codes';
      }
    } else if (!isIntegerLike(value)) {
      return 'expects an integer status code';
    }
  }

  if (target === 'json' && NUMERIC_JSON_OPS.includes(op) && !isNumberLike(value)) return 'expects a numeric value';
  if (target === 'json' && op === 'bool_is' && !isBooleanLike(value)) return 'expects true or false';

  if ((op === 'regex' || op === 'not_regex') && value !== undefined) {
    try {
      // Validate regex syntax early to avoid a backend validation round-trip.
      new RegExp(String(value));
    } catch {
      return 'has invalid regex';
    }
  }
  return undefined;
}

/** First problem with one step, or undefined. `extractNames` spans all steps: extract names are global. */
function stepProblem(step: SyntheticAPIStepDraft, id: string, extractNames: Set<string>): string | undefined {
  if (!step.url.trim()) return `Step '${id}' requires a URL`;
  if (!step.method.trim()) return `Step '${id}' requires an HTTP method`;
  if (isInvalidOptionalInt(step.timeout_seconds, 1)) return `Step '${id}' has invalid timeout`;
  if (isInvalidOptionalInt(step.max_redirects, 0)) return `Step '${id}' has invalid max redirects`;
  if (hasIncompleteHeaderRow(step.headers)) return `Step '${id}' has invalid headers`;

  for (let i = 0; i < step.assertions.length; i += 1) {
    const problem = assertionProblem(step.assertions[i]);
    if (problem) return `Step '${id}' assertion #${i + 1} ${problem}`;
  }

  for (let i = 0; i < step.extracts.length; i += 1) {
    const extract = step.extracts[i];
    const name = extract.name.trim();
    const path = extract.path.trim();
    if (!name && !path) continue;
    const prefix = `Step '${id}' extract #${i + 1}`;
    if (!name) return `${prefix} requires a name`;
    if (extractNames.has(name)) return `${prefix} name must be globally unique`;
    extractNames.add(name);
    if (extract.from !== 'json' && extract.from !== 'header') return `${prefix} has invalid source`;
    if (!path) return `${prefix} requires a path`;
  }
  return undefined;
}

function stepsProblem(steps: SyntheticAPIStepDraft[]): string | undefined {
  if (steps.length === 0) return 'At least one API step is required';
  const ids = new Set<string>();
  const extractNames = new Set<string>();
  for (const step of steps) {
    const id = step.id.trim();
    if (!id) return 'Each API step requires an ID';
    if (ids.has(id)) return 'API step IDs must be unique';
    ids.add(id);
    const problem = stepProblem(step, id, extractNames);
    if (problem) return problem;
  }
  return undefined;
}

/** Reports at most one message per field: the first problem found, in step order. */
export function validateSyntheticAPIForm(data: SyntheticAPIFormData): FormErrors {
  const errors: FormErrors = {};
  const baseURL = data.base_url.trim();
  if (baseURL && !isHttpUrl(baseURL)) {
    errors.synthetic_api_base_url = 'Base URL must start with http:// or https://';
  }
  if (hasKeylessVariableRow(data.variables)) {
    errors.synthetic_api_variables = 'Each variable requires a key';
  }
  const stepError = stepsProblem(data.steps);
  if (stepError) errors.synthetic_api_steps = stepError;
  return errors;
}

function buildStep(step: SyntheticAPIStepDraft): SyntheticAPIStepConfig {
  const request: SyntheticAPIStepConfig['request'] = {
    method: step.method.trim().toUpperCase(),
    url: step.url.trim(),
  };
  const headers = headersFromRows(step.headers);
  if (Object.keys(headers).length > 0) request.headers = headers;
  if (step.body.trim()) request.body = step.body;
  const timeout = parseOptionalInt(step.timeout_seconds);
  if (timeout !== undefined) request.timeout_seconds = timeout;
  request.follow_redirects = step.follow_redirects;
  const maxRedirects = parseOptionalInt(step.max_redirects);
  if (maxRedirects !== undefined) request.max_redirects = maxRedirects;

  const config: SyntheticAPIStepConfig = { id: step.id.trim(), request };
  if (step.name.trim()) config.name = step.name.trim();

  const assertions: SyntheticAPIAssertionConfig[] = [];
  for (const assertion of step.assertions) {
    const op = assertion.op.trim().toLowerCase();
    if (!op) continue;
    const item: SyntheticAPIAssertionConfig = { target: normalizeAssertionTarget(assertion.target), op };
    if (assertion.path.trim()) item.path = assertion.path.trim();
    if (assertion.value_input.trim()) item.value = parseAssertionValueInput(assertion.value_input);
    assertions.push(item);
  }
  if (assertions.length > 0) config.assert = assertions;

  const extracts: SyntheticAPIExtractConfig[] = [];
  for (const extract of step.extracts) {
    const name = extract.name.trim();
    const path = extract.path.trim();
    if (!name || !path) continue;
    const item: SyntheticAPIExtractConfig = { name, from: extract.from, path };
    if (extract.sensitive) item.sensitive = true;
    extracts.push(item);
  }
  if (extracts.length > 0) config.extract = extracts;

  return config;
}

/** Assumes `validateSyntheticAPIForm` passed. */
export function buildSyntheticAPIConfig(data: SyntheticAPIFormData): SyntheticAPIMonitorConfig {
  const config: SyntheticAPIMonitorConfig = {
    failure_mode: data.failure_mode,
    steps: data.steps.map(buildStep),
  };
  if (data.base_url.trim()) config.base_url = data.base_url.trim();
  const variables = variablesFromRows(data.variables);
  if (Object.keys(variables).length > 0) config.variables = variables;
  return config;
}
