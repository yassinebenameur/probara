import {
  CheckResult,
  SyntheticAPIAssertionConfig,
  SyntheticAPIAssertionTarget,
  SyntheticAPIExtractConfig,
  SyntheticAPIStepConfig,
  SyntheticBrowserAction,
  SyntheticBrowserStepConfig,
} from '../types';
import { HeaderKV, defaultHeaderRows, mapHeadersToRows } from './http';

// Pure synthetic API / browser helpers for MonitorForm: draft shapes, the
// stored-config <-> draft mappers, defaults, and the guided browser builder.

export type SyntheticVarKV = { key: string; value: string };
export type SyntheticAPIAssertionDraft = {
  target: SyntheticAPIAssertionTarget;
  op: string;
  path: string;
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
export type SyntheticBrowserStepDraft = {
  id: string;
  action: SyntheticBrowserAction;
  url: string;
  selector: string;
  value: string;
  timeout_seconds: string;
};

export type SyntheticEditorMode = 'basic' | 'advanced';
export type SyntheticTestPhase = 'idle' | 'queueing' | 'polling' | 'success' | 'failure' | 'error' | 'timeout';
export type SyntheticBrowserSetupMode = 'guided' | 'expert';
export type SyntheticBrowserGuidedTemplate = 'homepage_smoke' | 'login_flow';
export type SyntheticBrowserGuidedStep = 1 | 2 | 3;

export type SyntheticTestState = {
  phase: SyntheticTestPhase;
  message?: string;
  queuedAt?: string;
  jobId?: string;
  result?: CheckResult;
};

export type SyntheticBrowserGuidedDraft = {
  template: SyntheticBrowserGuidedTemplate;
  start_url: string;
  must_see_selector: string;
  email_selector: string;
  password_selector: string;
  submit_selector: string;
  post_login_selector: string;
  expected_url: string;
  username: string;
  password: string;
};

export const mapVariablesToRows = (variables?: Record<string, string>): SyntheticVarKV[] => {
  if (!variables || Object.keys(variables).length === 0) return [{ key: '', value: '' }];
  const rows = Object.entries(variables).map(([key, value]) => ({ key, value: String(value) }));
  return rows.length > 0 ? rows : [{ key: '', value: '' }];
};

export const syntheticAssertionOpsByTarget: Record<SyntheticAPIAssertionTarget, string[]> = {
  status: ['equals', 'not_equals', 'in'],
  header: ['exists', 'equals', 'not_equals', 'contains', 'not_contains', 'regex', 'not_regex'],
  body: ['exists', 'equals', 'not_equals', 'contains', 'not_contains', 'regex', 'not_regex'],
  json: ['exists', 'equals', 'not_equals', 'contains', 'not_contains', 'regex', 'not_regex', 'number_gt', 'number_gte', 'number_lt', 'number_lte', 'bool_is'],
};

export const defaultSyntheticAssertionForTarget = (target: SyntheticAPIAssertionTarget): SyntheticAPIAssertionDraft => {
  if (target === 'status') {
    return { target, op: 'equals', path: '', value_input: '200' };
  }
  if (target === 'json') {
    return { target, op: 'exists', path: 'data.ok', value_input: '' };
  }
  if (target === 'header') {
    return { target, op: 'exists', path: 'Content-Type', value_input: '' };
  }
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
  if (target === 'status' || target === 'header' || target === 'body' || target === 'json') {
    return target;
  }
  return 'status';
};

export const assertionPathRequired = (target: SyntheticAPIAssertionTarget): boolean => target === 'header' || target === 'json';

export const assertionValueRequired = (target: SyntheticAPIAssertionTarget, op: string): boolean => {
  const normalizedOp = op.trim().toLowerCase();
  if (target === 'status') return true;
  return normalizedOp !== 'exists';
};

export const parseAssertionValueInput = (input: string): unknown => {
  const trimmed = input.trim();
  if (!trimmed) return undefined;
  try {
    return JSON.parse(trimmed);
  } catch {
    return trimmed;
  }
};

export const isIntegerLike = (value: unknown): boolean => {
  if (typeof value === 'number') return Number.isInteger(value);
  if (typeof value === 'string') return /^-?\d+$/.test(value.trim());
  return false;
};

export const isNumberLike = (value: unknown): boolean => {
  if (typeof value === 'number') return Number.isFinite(value);
  if (typeof value === 'string') return value.trim() !== '' && !Number.isNaN(Number(value));
  return false;
};

export const isBooleanLike = (value: unknown): boolean => {
  if (typeof value === 'boolean') return true;
  if (typeof value === 'string') {
    const normalized = value.trim().toLowerCase();
    return normalized === 'true' || normalized === 'false';
  }
  return false;
};

export const assertionValuePlaceholder = (target: SyntheticAPIAssertionTarget, op: string): string => {
  if (!assertionValueRequired(target, op)) return '';
  if (target === 'status') {
    return op === 'in' ? '[200, 201]' : '200';
  }
  if (target === 'json') {
    if (op === 'bool_is') return 'true';
    if (op === 'number_gt' || op === 'number_gte' || op === 'number_lt' || op === 'number_lte') return '100';
    return '"ok"';
  }
  return 'expected value';
};

/**
 * Whether an assertion compares text: every header and body op, and the json
 * contains/regex ops. The worker matches these against the value's text
 * (shared/models SyntheticAPIAssertionConfig.ValueText), so the input is
 * submitted as the text typed: `1.50`, `{"status":"ok"}` or a long id would
 * otherwise go out as a number or object and lose its spelling.
 */
export const isTextAssertion = (target: SyntheticAPIAssertionTarget, op: string): boolean => {
  if (target === 'header' || target === 'body') return true;
  if (target !== 'json') return false;
  const normalizedOp = op.trim().toLowerCase();
  return normalizedOp === 'contains' || normalizedOp === 'not_contains' || normalizedOp === 'regex' || normalizedOp === 'not_regex';
};

/**
 * The value an assertion's input submits. The input is a JSON literal or
 * plain text: for json equals, `123`, `true` and `null` are those types and
 * `"123"` (quoted) is the string. Text assertions always submit a string —
 * the quoted literal's content, else the input as typed (trimmed).
 */
export const assertionValueFromInput = (target: SyntheticAPIAssertionTarget, op: string, input: string): unknown => {
  const parsed = parseAssertionValueInput(input);
  if (parsed === undefined || typeof parsed === 'string' || !isTextAssertion(target, op)) return parsed;
  return input.trim();
};

/**
 * The input a stored assertion value hydrates into, chosen so a save submits
 * the same value back: a string that would read as another type (`"123"`,
 * `"true"`, or one with edge whitespace or quotes) is shown quoted. Stored
 * values from before the form kept strings (`123` that meant a string) stay
 * as they are; a text assertion re-saves them as their text.
 */
export const assertionValueToInput = (target: SyntheticAPIAssertionTarget, op: string, value: unknown): string => {
  if (typeof value !== 'string') return stringifyAssertionValueInput(value);
  return assertionValueFromInput(target, op, value) === value ? value : JSON.stringify(value);
};

export const stringifyAssertionValueInput = (value: unknown): string => {
  if (value === undefined || value === null) return '';
  if (typeof value === 'string') return value;
  if (typeof value === 'number' || typeof value === 'boolean') return String(value);
  try {
    return JSON.stringify(value);
  } catch {
    return String(value);
  }
};

export const mapSyntheticAPIAssertionsToDraft = (assertions?: SyntheticAPIAssertionConfig[]): SyntheticAPIAssertionDraft[] => {
  if (!assertions || assertions.length === 0) return [];
  return assertions.map((assertion) => {
    const target = normalizeAssertionTarget(assertion.target || 'status');
    const ops = syntheticAssertionOpsByTarget[target];
    const normalizedOp = (assertion.op || '').trim().toLowerCase();
    const op = ops.includes(normalizedOp) ? normalizedOp : ops[0];
    return {
      target,
      op,
      path: assertion.path || '',
      value_input: assertionValueToInput(target, op, assertion.value),
    };
  });
};

export const mapSyntheticAPIExtractsToDraft = (extracts?: SyntheticAPIExtractConfig[]): SyntheticAPIExtractDraft[] => {
  if (!extracts || extracts.length === 0) return [];
  return extracts.map((extract) => ({
    name: extract.name || '',
    from: extract.from === 'header' ? 'header' : 'json',
    path: extract.path || '',
    sensitive: !!extract.sensitive,
  }));
};

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

export const defaultSyntheticBrowserStep = (index: number): SyntheticBrowserStepDraft => ({
  id: `step_${index}`,
  action: 'goto',
  url: '',
  selector: '',
  value: '',
  timeout_seconds: '',
});

export const syntheticBrowserActionMeta: Record<
  SyntheticBrowserAction,
  {
    label: string;
    hint: string;
    selectorPlaceholder?: string;
    valuePlaceholder?: string;
  }
> = {
  goto: {
    label: 'Go to URL',
    hint: 'Navigate browser to a URL.',
  },
  click: {
    label: 'Click element',
    hint: 'Click a matching DOM element.',
    selectorPlaceholder: '[data-test=submit]',
  },
  fill: {
    label: 'Fill input',
    hint: 'Type value into a field selected by CSS selector.',
    selectorPlaceholder: '#email',
    valuePlaceholder: '{{email}}',
  },
  wait_for: {
    label: 'Wait for element',
    hint: 'Wait until a selector appears before continuing.',
    selectorPlaceholder: '[data-test=dashboard]',
  },
  assert_visible: {
    label: 'Assert element visible',
    hint: 'Fail step if selector is not visible.',
    selectorPlaceholder: '[data-test=dashboard]',
  },
  assert_text: {
    label: 'Assert text',
    hint: 'Assert an element contains expected text.',
    selectorPlaceholder: 'h1',
    valuePlaceholder: 'Welcome back',
  },
  assert_url: {
    label: 'Assert current URL',
    hint: 'Assert current URL contains expected value.',
    valuePlaceholder: '/dashboard',
  },
};

export const mapSyntheticAPIStepsToDraft = (steps?: SyntheticAPIStepConfig[]): SyntheticAPIStepDraft[] => {
  if (!steps || steps.length === 0) {
    return [
      {
        ...defaultSyntheticAPIStep(1),
        id: 'health',
        url: '/health',
      },
    ];
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
    assertions: mapSyntheticAPIAssertionsToDraft(step.assert),
    extracts: mapSyntheticAPIExtractsToDraft(step.extract),
  }));
};

export const mapSyntheticBrowserStepsToDraft = (steps?: SyntheticBrowserStepConfig[]): SyntheticBrowserStepDraft[] => {
  if (!steps || steps.length === 0) {
    return [
      { ...defaultSyntheticBrowserStep(1), id: 'open', action: 'goto', url: 'https://example.com/login' },
      { ...defaultSyntheticBrowserStep(2), id: 'assert_dashboard', action: 'assert_visible', selector: '[data-test=dashboard]' },
    ];
  }

  return steps.map((step, idx) => ({
    id: step.id || `step_${idx + 1}`,
    action: step.action || 'goto',
    url: step.url || '',
    selector: step.selector || '',
    value: step.value || '',
    timeout_seconds: step.timeout_seconds !== undefined ? String(step.timeout_seconds) : '',
  }));
};

export const defaultSyntheticBrowserGuidedDraft = (
  startURL = ''
): SyntheticBrowserGuidedDraft => ({
  template: 'homepage_smoke',
  start_url: startURL,
  must_see_selector: 'main',
  email_selector: '[name=email]',
  password_selector: '[name=password]',
  submit_selector: 'button[type=submit]',
  post_login_selector: '[data-test=dashboard]',
  expected_url: '/dashboard',
  username: '',
  password: '',
});

export const normalizeSyntheticBrowserGuidedTemplate = (value: string): SyntheticBrowserGuidedTemplate =>
  value === 'login_flow' ? 'login_flow' : 'homepage_smoke';

export const buildSyntheticBrowserGuidedDraft = (draft: SyntheticBrowserGuidedDraft): {
  startURL: string;
  variables: Record<string, string>;
  steps: SyntheticBrowserStepDraft[];
} => {
  const template = normalizeSyntheticBrowserGuidedTemplate(draft.template);
  const startURL = draft.start_url.trim();
  const variables: Record<string, string> = {};
  const username = draft.username.trim();
  const password = draft.password.trim();

  if (username) {
    variables.username = username;
  }
  if (password) {
    variables.password = password;
  }

  if (template === 'homepage_smoke') {
    return {
      startURL,
      variables,
      steps: [
        {
          ...defaultSyntheticBrowserStep(1),
          id: 'open_homepage',
          action: 'goto',
          url: startURL,
        },
        {
          ...defaultSyntheticBrowserStep(2),
          id: 'assert_page_ready',
          action: 'assert_visible',
          selector: draft.must_see_selector.trim() || 'main',
        },
      ],
    };
  }

  const steps: SyntheticBrowserStepDraft[] = [
    {
      ...defaultSyntheticBrowserStep(1),
      id: 'open_login',
      action: 'goto',
      url: startURL,
    },
  ];

  if (username) {
    steps.push({
      ...defaultSyntheticBrowserStep(steps.length + 1),
      id: 'fill_username',
      action: 'fill',
      selector: draft.email_selector.trim(),
      value: '{{username}}',
    });
  }

  if (password) {
    steps.push({
      ...defaultSyntheticBrowserStep(steps.length + 1),
      id: 'fill_password',
      action: 'fill',
      selector: draft.password_selector.trim(),
      value: '{{password}}',
    });
  }

  steps.push(
    {
      ...defaultSyntheticBrowserStep(steps.length + 1),
      id: 'submit_login',
      action: 'click',
      selector: draft.submit_selector.trim(),
    },
    {
      ...defaultSyntheticBrowserStep(steps.length + 1),
      id: 'wait_dashboard',
      action: 'wait_for',
      selector: draft.post_login_selector.trim(),
    },
    {
      ...defaultSyntheticBrowserStep(steps.length + 1),
      id: 'assert_url',
      action: 'assert_url',
      value: draft.expected_url.trim(),
    }
  );

  return { startURL, variables, steps };
};
