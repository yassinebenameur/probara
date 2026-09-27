import type {
  SyntheticBrowserAction,
  SyntheticBrowserMonitorConfig,
  SyntheticBrowserStepConfig,
  SyntheticFailureMode,
} from '../types';
import {
  type FormErrors,
  type VariableKV,
  emptyVariableRow,
  hasKeylessVariableRow,
  isHttpUrl,
  isInvalidOptionalInt,
  mapVariablesToRows,
  parseOptionalInt,
  variablesFromRows,
} from './common';

export type SyntheticBrowserStepDraft = {
  id: string;
  action: SyntheticBrowserAction;
  url: string;
  selector: string;
  value: string;
  timeout_seconds: string;
};

/**
 * Guided mode builds the journey from a template plus a few inputs; expert mode
 * edits steps directly. The mode decides which config is saved.
 */
export type SyntheticBrowserSetupMode = 'guided' | 'expert';
export type SyntheticBrowserGuidedTemplate = 'homepage_smoke' | 'login_flow';

/** Guided-mode inputs. The start URL is shared with expert mode (`start_url`). */
export interface SyntheticBrowserGuidedInputs {
  template: SyntheticBrowserGuidedTemplate;
  must_see_selector: string;
  email_selector: string;
  password_selector: string;
  submit_selector: string;
  post_login_selector: string;
  expected_url: string;
  username: string;
  password: string;
}

export interface SyntheticBrowserFormData {
  setup_mode: SyntheticBrowserSetupMode;
  start_url: string;
  device: string;
  failure_mode: SyntheticFailureMode;
  variables: VariableKV[];
  steps: SyntheticBrowserStepDraft[];
  screenshot_on_failure: boolean;
  trace_on_failure: boolean;
  har_on_failure: boolean;
  guided: SyntheticBrowserGuidedInputs;
}

export type SyntheticBrowserGuidedJourney = {
  startURL: string;
  variables: Record<string, string>;
  steps: SyntheticBrowserStepDraft[];
};

/** Error keys owned by the synthetic browser editor. */
export const SYNTHETIC_BROWSER_ERROR_KEYS = ['synthetic_browser_steps', 'synthetic_browser_variables', 'synthetic_browser_start_url'];

export const syntheticBrowserActionMeta: Record<
  SyntheticBrowserAction,
  { label: string; hint: string; selectorPlaceholder?: string; valuePlaceholder?: string }
> = {
  goto: { label: 'Go to URL', hint: 'Navigate browser to a URL.' },
  click: { label: 'Click element', hint: 'Click a matching DOM element.', selectorPlaceholder: '[data-test=submit]' },
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
  assert_url: { label: 'Assert current URL', hint: 'Assert current URL contains expected value.', valuePlaceholder: '/dashboard' },
};

export const browserActionNeedsURL = (action: SyntheticBrowserAction): boolean => action === 'goto';

export const browserActionNeedsSelector = (action: SyntheticBrowserAction): boolean =>
  action === 'click' || action === 'fill' || action === 'wait_for' || action === 'assert_visible' || action === 'assert_text';

export const browserActionNeedsValue = (action: SyntheticBrowserAction): boolean =>
  action === 'fill' || action === 'assert_text' || action === 'assert_url';

export const defaultSyntheticBrowserStep = (index: number): SyntheticBrowserStepDraft => ({
  id: `step_${index}`,
  action: 'goto',
  url: '',
  selector: '',
  value: '',
  timeout_seconds: '',
});

const mapStepsToDraft = (steps?: SyntheticBrowserStepConfig[]): SyntheticBrowserStepDraft[] => {
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

export const defaultGuidedInputs = (): SyntheticBrowserGuidedInputs => ({
  template: 'homepage_smoke',
  must_see_selector: 'main',
  email_selector: '[name=email]',
  password_selector: '[name=password]',
  submit_selector: 'button[type=submit]',
  post_login_selector: '[data-test=dashboard]',
  expected_url: '/dashboard',
  username: '',
  password: '',
});

export const normalizeGuidedTemplate = (value: string): SyntheticBrowserGuidedTemplate =>
  value === 'login_flow' ? 'login_flow' : 'homepage_smoke';

/** A saved journey that logs in (by step id or login-style actions) reopens as the login template. */
const inferGuidedTemplate = (cfg?: SyntheticBrowserMonitorConfig): SyntheticBrowserGuidedTemplate =>
  (cfg?.steps || []).some((step) => {
    const id = (step.id || '').toLowerCase();
    return id.includes('login') || step.action === 'wait_for' || step.action === 'assert_url';
  })
    ? 'login_flow'
    : 'homepage_smoke';

/** Edits open in expert mode; new monitors (including clones) start guided. */
export function initialSyntheticBrowserFormData(
  cfg: SyntheticBrowserMonitorConfig | undefined,
  isEditMode: boolean
): SyntheticBrowserFormData {
  return {
    setup_mode: isEditMode ? 'expert' : 'guided',
    start_url: cfg?.start_url || '',
    device: cfg?.device || 'Desktop Chrome',
    failure_mode: cfg?.failure_mode || 'fail_fast',
    variables: mapVariablesToRows(cfg?.variables),
    steps: mapStepsToDraft(cfg?.steps),
    screenshot_on_failure: cfg?.artifacts?.screenshot_on_failure ?? true,
    trace_on_failure: cfg?.artifacts?.trace_on_failure ?? false,
    har_on_failure: cfg?.artifacts?.har_on_failure ?? false,
    guided: {
      ...defaultGuidedInputs(),
      template: inferGuidedTemplate(cfg),
      username: cfg?.variables?.username || cfg?.variables?.email || '',
      password: cfg?.variables?.password || '',
    },
  };
}

/** Saved journeys using variables, a device, or step timeouts open in Advanced mode. */
export const hasAdvancedSyntheticBrowserConfig = (cfg?: SyntheticBrowserMonitorConfig): boolean => {
  if (!cfg) return false;
  return (
    Object.keys(cfg.variables || {}).length > 0 ||
    Boolean(cfg.device) ||
    (cfg.steps || []).some((step) => Boolean(step.timeout_seconds))
  );
};

/** Expands the guided inputs into concrete steps and variables. */
export function buildGuidedJourney(startURLInput: string, inputs: SyntheticBrowserGuidedInputs): SyntheticBrowserGuidedJourney {
  const startURL = startURLInput.trim();
  const variables: Record<string, string> = {};
  const username = inputs.username.trim();
  const password = inputs.password.trim();
  if (username) variables.username = username;
  if (password) variables.password = password;

  if (normalizeGuidedTemplate(inputs.template) === 'homepage_smoke') {
    return {
      startURL,
      variables,
      steps: [
        { ...defaultSyntheticBrowserStep(1), id: 'open_homepage', action: 'goto', url: startURL },
        {
          ...defaultSyntheticBrowserStep(2),
          id: 'assert_page_ready',
          action: 'assert_visible',
          selector: inputs.must_see_selector.trim() || 'main',
        },
      ],
    };
  }

  const steps: SyntheticBrowserStepDraft[] = [
    { ...defaultSyntheticBrowserStep(1), id: 'open_login', action: 'goto', url: startURL },
  ];
  if (username) {
    steps.push({
      ...defaultSyntheticBrowserStep(steps.length + 1),
      id: 'fill_username',
      action: 'fill',
      selector: inputs.email_selector.trim(),
      value: '{{username}}',
    });
  }
  if (password) {
    steps.push({
      ...defaultSyntheticBrowserStep(steps.length + 1),
      id: 'fill_password',
      action: 'fill',
      selector: inputs.password_selector.trim(),
      value: '{{password}}',
    });
  }
  steps.push(
    { ...defaultSyntheticBrowserStep(steps.length + 1), id: 'submit_login', action: 'click', selector: inputs.submit_selector.trim() },
    { ...defaultSyntheticBrowserStep(steps.length + 1), id: 'wait_dashboard', action: 'wait_for', selector: inputs.post_login_selector.trim() },
    { ...defaultSyntheticBrowserStep(steps.length + 1), id: 'assert_url', action: 'assert_url', value: inputs.expected_url.trim() }
  );
  return { startURL, variables, steps };
}

export const guidedJourneyOf = (data: SyntheticBrowserFormData): SyntheticBrowserGuidedJourney =>
  buildGuidedJourney(data.start_url, data.guided);

/** Guided mode always saves these; switching to expert copies them in. */
const GUIDED_RUN_SETTINGS = {
  device: 'Desktop Chrome',
  failure_mode: 'fail_fast',
  screenshot_on_failure: true,
  trace_on_failure: false,
  har_on_failure: false,
} as const;

/** Switching guided → expert replaces the expert steps with the guided journey. */
export function syncGuidedIntoExpert(data: SyntheticBrowserFormData): SyntheticBrowserFormData {
  const journey = guidedJourneyOf(data);
  return {
    ...data,
    ...GUIDED_RUN_SETTINGS,
    start_url: journey.startURL,
    steps: journey.steps,
    variables: mapVariablesToRows(journey.variables),
  };
}

/** Guided template picker: resets the template's selectors, keeps a typed start URL. */
export function applyGuidedTemplate(
  data: SyntheticBrowserFormData,
  template: SyntheticBrowserGuidedTemplate
): SyntheticBrowserFormData {
  const defaults = defaultGuidedInputs();
  return {
    ...data,
    start_url: data.start_url || (template === 'login_flow' ? 'https://app.example.com/login' : 'https://app.example.com'),
    guided: {
      ...defaults,
      template,
      username: data.guided.username,
      password: data.guided.password,
    },
  };
}

/** Expert-mode quick starts; they replace the run settings and steps. */
export function applyExpertTemplate(
  data: SyntheticBrowserFormData,
  template: SyntheticBrowserGuidedTemplate
): SyntheticBrowserFormData {
  if (template === 'homepage_smoke') {
    return {
      ...data,
      ...GUIDED_RUN_SETTINGS,
      start_url: 'https://app.example.com',
      variables: [emptyVariableRow()],
      steps: [
        { ...defaultSyntheticBrowserStep(1), id: 'open', action: 'goto', url: 'https://app.example.com' },
        { ...defaultSyntheticBrowserStep(2), id: 'hero_visible', action: 'assert_visible', selector: 'main' },
        { ...defaultSyntheticBrowserStep(3), id: 'cta_visible', action: 'assert_visible', selector: '[data-test=primary-cta]' },
      ],
    };
  }
  return {
    ...data,
    ...GUIDED_RUN_SETTINGS,
    start_url: 'https://app.example.com/login',
    variables: [
      { key: 'email', value: 'monitor@example.com' },
      { key: 'password', value: 'change-me' },
    ],
    steps: [
      { ...defaultSyntheticBrowserStep(1), id: 'open_login', action: 'goto', url: 'https://app.example.com/login' },
      { ...defaultSyntheticBrowserStep(2), id: 'fill_email', action: 'fill', selector: '[name=email]', value: '{{email}}' },
      { ...defaultSyntheticBrowserStep(3), id: 'fill_password', action: 'fill', selector: '[name=password]', value: '{{password}}' },
      { ...defaultSyntheticBrowserStep(4), id: 'submit', action: 'click', selector: 'button[type=submit]' },
      { ...defaultSyntheticBrowserStep(5), id: 'wait_dashboard', action: 'wait_for', selector: '[data-test=dashboard]' },
      { ...defaultSyntheticBrowserStep(6), id: 'assert_url', action: 'assert_url', value: '/dashboard' },
    ],
  };
}

function guidedInputsProblem(data: SyntheticBrowserFormData, journey: SyntheticBrowserGuidedJourney): string | undefined {
  const inputs = data.guided;
  if (journey.steps.length === 0) return 'Unable to build browser steps from guided setup';
  if (normalizeGuidedTemplate(inputs.template) !== 'login_flow') return undefined;
  if (!inputs.submit_selector.trim()) return 'Submit selector is required';
  if (!inputs.post_login_selector.trim()) return 'Post-login selector is required';
  if (!inputs.expected_url.trim()) return 'Expected URL value is required';
  if (inputs.username.trim() && !inputs.email_selector.trim()) return 'Email selector is required when username is set';
  if (inputs.password.trim() && !inputs.password_selector.trim()) return 'Password selector is required when password is set';
  return undefined;
}

function expertStepsProblem(steps: SyntheticBrowserStepDraft[]): string | undefined {
  if (steps.length === 0) return 'At least one browser step is required';
  const ids = new Set<string>();
  for (const step of steps) {
    const id = step.id.trim();
    if (!id) return 'Each browser step requires an ID';
    if (ids.has(id)) return 'Browser step IDs must be unique';
    ids.add(id);
    if (browserActionNeedsURL(step.action) && !step.url.trim()) return `Step '${id}' requires a URL`;
    if (browserActionNeedsSelector(step.action) && !step.selector.trim()) return `Step '${id}' requires a selector`;
    if (browserActionNeedsValue(step.action) && !step.value.trim()) return `Step '${id}' requires a value`;
    if (isInvalidOptionalInt(step.timeout_seconds, 1)) return `Step '${id}' has invalid timeout`;
  }
  return undefined;
}

/** Validates whichever mode will be saved. Reports the first problem per field. */
export function validateSyntheticBrowserForm(data: SyntheticBrowserFormData): FormErrors {
  const errors: FormErrors = {};
  const guided = data.setup_mode === 'guided';
  const journey = guided ? guidedJourneyOf(data) : undefined;

  const startURL = (journey?.startURL || data.start_url).trim();
  if (!startURL) {
    errors.synthetic_browser_start_url = 'Start URL is required';
  } else if (!isHttpUrl(startURL)) {
    errors.synthetic_browser_start_url = 'Start URL must start with http:// or https://';
  }

  if (journey) {
    const problem = guidedInputsProblem(data, journey);
    if (problem) errors.synthetic_browser_steps = problem;
    return errors;
  }

  if (hasKeylessVariableRow(data.variables)) {
    errors.synthetic_browser_variables = 'Each variable requires a key';
  }
  const stepProblem = expertStepsProblem(data.steps);
  if (stepProblem) errors.synthetic_browser_steps = stepProblem;
  return errors;
}

function buildStep(step: SyntheticBrowserStepDraft, includeTimeout: boolean): SyntheticBrowserStepConfig {
  const config: SyntheticBrowserStepConfig = { id: step.id.trim(), action: step.action };
  if (step.url.trim()) config.url = step.url.trim();
  if (step.selector.trim()) config.selector = step.selector.trim();
  if (step.value.trim()) config.value = step.value;
  if (includeTimeout) {
    const timeout = parseOptionalInt(step.timeout_seconds);
    if (timeout !== undefined) config.timeout_seconds = timeout;
  }
  return config;
}

/** Assumes `validateSyntheticBrowserForm` passed. Guided mode saves fixed run settings. */
export function buildSyntheticBrowserConfig(data: SyntheticBrowserFormData): SyntheticBrowserMonitorConfig {
  if (data.setup_mode === 'guided') {
    const journey = guidedJourneyOf(data);
    const config: SyntheticBrowserMonitorConfig = {
      start_url: journey.startURL,
      device: GUIDED_RUN_SETTINGS.device,
      failure_mode: GUIDED_RUN_SETTINGS.failure_mode,
      steps: journey.steps.map((step) => buildStep(step, false)),
      artifacts: {
        screenshot_on_failure: GUIDED_RUN_SETTINGS.screenshot_on_failure,
        trace_on_failure: GUIDED_RUN_SETTINGS.trace_on_failure,
        har_on_failure: GUIDED_RUN_SETTINGS.har_on_failure,
      },
    };
    if (Object.keys(journey.variables).length > 0) config.variables = journey.variables;
    return config;
  }

  const config: SyntheticBrowserMonitorConfig = {
    start_url: data.start_url.trim(),
    device: data.device.trim() || undefined,
    failure_mode: data.failure_mode,
    steps: data.steps.map((step) => buildStep(step, true)),
    artifacts: {
      screenshot_on_failure: data.screenshot_on_failure,
      trace_on_failure: data.trace_on_failure,
      har_on_failure: data.har_on_failure,
    },
  };
  const variables = variablesFromRows(data.variables);
  if (Object.keys(variables).length > 0) config.variables = variables;
  return config;
}
