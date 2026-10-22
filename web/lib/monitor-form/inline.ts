import {
  ChannelAssignment,
  CreateMonitorRequest,
  DNSMonitorConfig,
  DependencySuppression,
  HTTPBodyAssertion,
  HTTPDisplayField,
  HTTPHeaderAssertion,
  HTTPJSONAssertion,
  HTTPMonitorConfig,
  Monitor,
  MonitorType,
  NotificationMode,
  PingMonitorConfig,
  SyntheticAPIAssertionConfig,
  SyntheticAPIExtractConfig,
  SyntheticAPIMonitorConfig,
  SyntheticAPIStepConfig,
  SyntheticBrowserMonitorConfig,
  SyntheticBrowserStepConfig,
} from '../types';
import { buildScheduledMonitorRequest } from './common';
import { buildStatusRulesFromHTTPConfig, mapHeadersToRows, parseDNSExpectedAnswers, parseStatusRules } from './http';
import {
  SyntheticBrowserGuidedDraft,
  SyntheticBrowserGuidedTemplate,
  assertionPathRequired,
  assertionValueRequired,
  buildSyntheticBrowserGuidedDraft,
  defaultSyntheticAPIStep,
  defaultSyntheticBrowserGuidedDraft,
  defaultSyntheticBrowserStep,
  isBooleanLike,
  isIntegerLike,
  isNumberLike,
  mapSyntheticAPIStepsToDraft,
  mapSyntheticBrowserStepsToDraft,
  mapVariablesToRows,
  normalizeAssertionTarget,
  normalizeSyntheticBrowserGuidedTemplate,
  parseAssertionValueInput,
  syntheticAssertionOpsByTarget,
} from './synthetic';

// The monitor types MonitorForm edits itself (every other type delegates to
// its own form component): their shared form state, its hydration from a
// stored monitor or clone, validation, and the submitted request. Pure, so
// lib tests pin the exact payloads (lib/monitor-form-*.test.ts).

/** The types whose fields live in MonitorForm's own state. */
export type InlineMonitorType = 'http' | 'ping' | 'dns' | 'synthetic_api' | 'synthetic_browser';

// Browser journeys legitimately take longer than one request.
export const defaultTimeoutFor = (type: MonitorType): number =>
  type === 'synthetic_api' || type === 'synthetic_browser' ? 30 : 10;

/**
 * MonitorForm's initial state. `monitor` is the stored monitor on edit (its
 * secrets arrive masked); `initialData` is a clone source or import on
 * create. Fields of the types not being edited take their blank defaults.
 */
export function initialMonitorFormState(monitor?: Monitor, initialData?: CreateMonitorRequest) {
  const sourceType: MonitorType = monitor?.type || initialData?.type || 'http';
  const sourceConfig = monitor?.config || initialData?.config;
  const sourceTags = monitor?.tags || initialData?.tags || [];
  const sourceName = monitor?.name || initialData?.name || '';
  const sourceIntervalSeconds = monitor?.interval_seconds || initialData?.interval_seconds || 60;
  const sourceTimeoutSeconds = monitor?.timeout_seconds || initialData?.timeout_seconds || defaultTimeoutFor(sourceType);
  const sourceEnabled = monitor?.enabled ?? initialData?.enabled ?? true;

  const initialHTTPConfig: HTTPMonitorConfig | undefined =
    sourceType === 'http' ? (sourceConfig as HTTPMonitorConfig) : undefined;
  const initialPingConfig: PingMonitorConfig | undefined =
    sourceType === 'ping' ? (sourceConfig as PingMonitorConfig) : undefined;
  const initialDNSConfig: DNSMonitorConfig | undefined =
    sourceType === 'dns' ? (sourceConfig as DNSMonitorConfig) : undefined;
  const initialSyntheticAPIConfig: SyntheticAPIMonitorConfig | undefined =
    sourceType === 'synthetic_api' ? (sourceConfig as SyntheticAPIMonitorConfig) : undefined;
  const initialSyntheticBrowserConfig: SyntheticBrowserMonitorConfig | undefined =
    sourceType === 'synthetic_browser' ? (sourceConfig as SyntheticBrowserMonitorConfig) : undefined;
  const initialSyntheticBrowserTemplate: SyntheticBrowserGuidedTemplate =
    (initialSyntheticBrowserConfig?.steps || []).some((step) => {
      const id = (step.id || '').toLowerCase();
      return id.includes('login') || step.action === 'wait_for' || step.action === 'assert_url';
    })
      ? 'login_flow'
      : 'homepage_smoke';
  const initialSyntheticBrowserGuided = {
    ...defaultSyntheticBrowserGuidedDraft(initialSyntheticBrowserConfig?.start_url || ''),
    template: initialSyntheticBrowserTemplate,
    username:
      initialSyntheticBrowserConfig?.variables?.username ||
      initialSyntheticBrowserConfig?.variables?.email ||
      '',
    password: initialSyntheticBrowserConfig?.variables?.password || '',
  };

  return {
    name: sourceName,
    url: initialHTTPConfig?.url || '',
    method: initialHTTPConfig?.method || 'GET',
    status_rules: buildStatusRulesFromHTTPConfig(initialHTTPConfig),
    request_headers: mapHeadersToRows(initialHTTPConfig?.headers),
    body: initialHTTPConfig?.body || '',
    body_assertions: (() => {
      if (!initialHTTPConfig) return [] as HTTPBodyAssertion[];
      if (initialHTTPConfig.body_assertions && initialHTTPConfig.body_assertions.length > 0) {
        return initialHTTPConfig.body_assertions;
      }
      const legacy: HTTPBodyAssertion[] = [];
      if (initialHTTPConfig.expected_body_substring) {
        legacy.push({ op: 'contains', value: initialHTTPConfig.expected_body_substring });
      }
      if (initialHTTPConfig.expected_body_regex) {
        legacy.push({ op: 'regex', value: initialHTTPConfig.expected_body_regex });
      }
      return legacy;
    })(),
    response_header_assertions: initialHTTPConfig?.response_header_assertions || ([] as HTTPHeaderAssertion[]),
    json_assertions: initialHTTPConfig?.json_assertions || ([] as HTTPJSONAssertion[]),
    display_fields: initialHTTPConfig?.display_fields || ([] as HTTPDisplayField[]),
    max_latency_ms: initialHTTPConfig?.max_latency_ms?.toString() || '',
    follow_redirects: initialHTTPConfig?.follow_redirects ?? true,
    max_redirects: initialHTTPConfig?.max_redirects ?? 10,
    tls_skip_verify: initialHTTPConfig?.tls_skip_verify ?? false,
    tls_min_days_valid: initialHTTPConfig?.tls_min_days_valid?.toString() || '',
    tls_server_name: initialHTTPConfig?.tls_server_name || '',
    tls_ca_pem: initialHTTPConfig?.tls_ca_pem || '',
    collect_timing: initialHTTPConfig?.collect_timing ?? false,
    host: initialPingConfig?.host || initialDNSConfig?.host || '',
    dns_record_type: initialDNSConfig?.record_type || 'A',
    dns_expected_answers: (initialDNSConfig?.expected_answers || []).join(', '),
    dns_nameserver: initialDNSConfig?.nameserver || '',
    synthetic_api_base_url: initialSyntheticAPIConfig?.base_url || '',
    synthetic_api_failure_mode: initialSyntheticAPIConfig?.failure_mode || 'fail_fast',
    synthetic_api_variables: mapVariablesToRows(initialSyntheticAPIConfig?.variables),
    synthetic_api_steps: mapSyntheticAPIStepsToDraft(initialSyntheticAPIConfig?.steps),
    synthetic_browser_start_url: initialSyntheticBrowserConfig?.start_url || '',
    synthetic_browser_device: initialSyntheticBrowserConfig?.device || 'Desktop Chrome',
    synthetic_browser_failure_mode: initialSyntheticBrowserConfig?.failure_mode || 'fail_fast',
    synthetic_browser_variables: mapVariablesToRows(initialSyntheticBrowserConfig?.variables),
    synthetic_browser_steps: mapSyntheticBrowserStepsToDraft(initialSyntheticBrowserConfig?.steps),
    synthetic_browser_screenshot_on_failure: initialSyntheticBrowserConfig?.artifacts?.screenshot_on_failure ?? true,
    synthetic_browser_trace_on_failure: initialSyntheticBrowserConfig?.artifacts?.trace_on_failure ?? false,
    synthetic_browser_har_on_failure: initialSyntheticBrowserConfig?.artifacts?.har_on_failure ?? false,
    synthetic_browser_guided_template: initialSyntheticBrowserGuided.template,
    synthetic_browser_guided_must_see_selector: initialSyntheticBrowserGuided.must_see_selector,
    synthetic_browser_guided_email_selector: initialSyntheticBrowserGuided.email_selector,
    synthetic_browser_guided_password_selector: initialSyntheticBrowserGuided.password_selector,
    synthetic_browser_guided_submit_selector: initialSyntheticBrowserGuided.submit_selector,
    synthetic_browser_guided_post_login_selector: initialSyntheticBrowserGuided.post_login_selector,
    synthetic_browser_guided_expected_url: initialSyntheticBrowserGuided.expected_url,
    synthetic_browser_guided_username: initialSyntheticBrowserGuided.username,
    synthetic_browser_guided_password: initialSyntheticBrowserGuided.password,
    interval_seconds: sourceIntervalSeconds,
    timeout_seconds: sourceTimeoutSeconds,
    consecutive_failures_threshold: monitor?.consecutive_failures_threshold ?? 2,
    notification_mode: (monitor?.notification_mode ?? 'default') as NotificationMode,
    dependency_suppression: (monitor?.dependency_suppression ?? 'inherit') as DependencySuppression,
    notification_channels: (monitor?.notification_channels ?? []) as ChannelAssignment[],
    enabled: sourceEnabled,
    tags: sourceTags.join(', '),
    depends_on_ids: monitor?.depends_on_ids ?? initialData?.depends_on_ids ?? ([] as string[]),
    location_ids: monitor?.location_ids ?? initialData?.location_ids ?? ([] as string[]),
    location_quorum: monitor?.location_quorum ?? initialData?.location_quorum ?? 1,
  };
}

export type MonitorFormState = ReturnType<typeof initialMonitorFormState>;

/** A loaded synthetic API config that the Basic editor would hide parts of. */
export const hasAdvancedSyntheticAPIConfig = (config: SyntheticAPIMonitorConfig): boolean =>
  Object.keys(config.variables || {}).length > 0 ||
  (config.steps || []).some((step) =>
    Object.keys(step.request?.headers || {}).length > 0 ||
    Boolean(step.request?.timeout_seconds) ||
    Boolean(step.request?.max_redirects) ||
    (step.assert || []).length > 0 ||
    (step.extract || []).length > 0
  );

/** A loaded synthetic browser config that the Basic editor would hide parts of. */
export const hasAdvancedSyntheticBrowserConfig = (config: SyntheticBrowserMonitorConfig): boolean =>
  Object.keys(config.variables || {}).length > 0 ||
  Boolean(config.device) ||
  (config.steps || []).some((step) => Boolean(step.timeout_seconds));

/** The guided browser setup's inputs, read out of the form state. */
export const syntheticBrowserGuidedDraftFromForm = (form: MonitorFormState): SyntheticBrowserGuidedDraft => ({
  template: normalizeSyntheticBrowserGuidedTemplate(form.synthetic_browser_guided_template),
  start_url: form.synthetic_browser_start_url,
  must_see_selector: form.synthetic_browser_guided_must_see_selector,
  email_selector: form.synthetic_browser_guided_email_selector,
  password_selector: form.synthetic_browser_guided_password_selector,
  submit_selector: form.synthetic_browser_guided_submit_selector,
  post_login_selector: form.synthetic_browser_guided_post_login_selector,
  expected_url: form.synthetic_browser_guided_expected_url,
  username: form.synthetic_browser_guided_username,
  password: form.synthetic_browser_guided_password,
});

/** Leaving guided for expert: the guided journey becomes the expert fields. */
export const syntheticBrowserGuidedToExpertFields = (form: MonitorFormState): Partial<MonitorFormState> => {
  const next = buildSyntheticBrowserGuidedDraft(syntheticBrowserGuidedDraftFromForm(form));
  return {
    synthetic_browser_start_url: next.startURL,
    synthetic_browser_device: 'Desktop Chrome',
    synthetic_browser_failure_mode: 'fail_fast',
    synthetic_browser_steps: next.steps,
    synthetic_browser_variables: mapVariablesToRows(next.variables),
    synthetic_browser_screenshot_on_failure: true,
    synthetic_browser_trace_on_failure: false,
    synthetic_browser_har_on_failure: false,
  };
};

/** The fields a synthetic API quick-start template loads. */
export const syntheticAPITemplateFields = (template: 'health_auth' | 'single_health'): Partial<MonitorFormState> => {
  if (template === 'single_health') {
    return {
      synthetic_api_base_url: 'https://api.example.com',
      synthetic_api_failure_mode: 'fail_fast',
      synthetic_api_variables: [{ key: '', value: '' }],
      synthetic_api_steps: [
        {
          ...defaultSyntheticAPIStep(1),
          id: 'health',
          name: 'Health endpoint',
          method: 'GET',
          url: '/health',
          assertions: [{ target: 'status', op: 'equals', path: '', value_input: '200' }],
        },
      ],
    };
  }

  return {
    synthetic_api_base_url: 'https://api.example.com',
    synthetic_api_failure_mode: 'fail_fast',
    synthetic_api_variables: [
      { key: 'email', value: 'monitor@example.com' },
      { key: 'password', value: 'change-me' },
    ],
    synthetic_api_steps: [
      {
        ...defaultSyntheticAPIStep(1),
        id: 'health',
        name: 'Health endpoint',
        method: 'GET',
        url: '/health',
        assertions: [{ target: 'status', op: 'equals', path: '', value_input: '200' }],
      },
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
};

/** The fields an expert-mode synthetic browser quick-start template loads. */
export const syntheticBrowserTemplateFields = (template: 'login_flow' | 'homepage_smoke'): Partial<MonitorFormState> => {
  if (template === 'homepage_smoke') {
    return {
      synthetic_browser_start_url: 'https://app.example.com',
      synthetic_browser_device: 'Desktop Chrome',
      synthetic_browser_failure_mode: 'fail_fast',
      synthetic_browser_variables: [{ key: '', value: '' }],
      synthetic_browser_steps: [
        { ...defaultSyntheticBrowserStep(1), id: 'open', action: 'goto', url: 'https://app.example.com' },
        { ...defaultSyntheticBrowserStep(2), id: 'hero_visible', action: 'assert_visible', selector: 'main' },
        { ...defaultSyntheticBrowserStep(3), id: 'cta_visible', action: 'assert_visible', selector: '[data-test=primary-cta]' },
      ],
      synthetic_browser_screenshot_on_failure: true,
      synthetic_browser_trace_on_failure: false,
      synthetic_browser_har_on_failure: false,
    };
  }

  return {
    synthetic_browser_start_url: 'https://app.example.com/login',
    synthetic_browser_device: 'Desktop Chrome',
    synthetic_browser_failure_mode: 'fail_fast',
    synthetic_browser_variables: [
      { key: 'email', value: 'monitor@example.com' },
      { key: 'password', value: 'change-me' },
    ],
    synthetic_browser_steps: [
      { ...defaultSyntheticBrowserStep(1), id: 'open_login', action: 'goto', url: 'https://app.example.com/login' },
      { ...defaultSyntheticBrowserStep(2), id: 'fill_email', action: 'fill', selector: '[name=email]', value: '{{email}}' },
      { ...defaultSyntheticBrowserStep(3), id: 'fill_password', action: 'fill', selector: '[name=password]', value: '{{password}}' },
      { ...defaultSyntheticBrowserStep(4), id: 'submit', action: 'click', selector: 'button[type=submit]' },
      { ...defaultSyntheticBrowserStep(5), id: 'wait_dashboard', action: 'wait_for', selector: '[data-test=dashboard]' },
      { ...defaultSyntheticBrowserStep(6), id: 'assert_url', action: 'assert_url', value: '/dashboard' },
    ],
    synthetic_browser_screenshot_on_failure: true,
    synthetic_browser_trace_on_failure: false,
    synthetic_browser_har_on_failure: false,
  };
};

/**
 * The fields a guided-journey tile loads: the template's default selectors,
 * keeping a start URL the operator already typed.
 */
export const syntheticBrowserGuidedTemplateFields = (
  prev: MonitorFormState,
  template: SyntheticBrowserGuidedTemplate
): Partial<MonitorFormState> => {
  const defaults = defaultSyntheticBrowserGuidedDraft(
    template === 'login_flow' ? 'https://app.example.com/login' : 'https://app.example.com'
  );
  return {
    synthetic_browser_guided_template: template,
    synthetic_browser_start_url: prev.synthetic_browser_start_url || defaults.start_url,
    synthetic_browser_guided_must_see_selector: defaults.must_see_selector,
    synthetic_browser_guided_email_selector: defaults.email_selector,
    synthetic_browser_guided_password_selector: defaults.password_selector,
    synthetic_browser_guided_submit_selector: defaults.submit_selector,
    synthetic_browser_guided_post_login_selector: defaults.post_login_selector,
    synthetic_browser_guided_expected_url: defaults.expected_url,
  };
};

/**
 * Field errors for a submit, keyed by form field; empty when it may be sent.
 * `guidedMode` is a synthetic browser monitor in the guided setup, whose
 * steps come from the guided inputs instead of the expert step list.
 */
export function validateMonitorForm(
  type: MonitorType,
  form: MonitorFormState,
  guidedMode: boolean
): Record<string, string> {
  const newErrors: Record<string, string> = {};
  const guidedSyntheticBrowser = guidedMode
    ? buildSyntheticBrowserGuidedDraft(syntheticBrowserGuidedDraftFromForm(form))
    : null;
  if (!form.name.trim()) newErrors.name = 'Name is required';

  if (type === 'http') {
    const url = form.url.trim();
    const isHTTP = url.startsWith('http://');
    const isHTTPS = url.startsWith('https://');

    if (!url) {
      newErrors.url = 'URL is required';
    } else if (!isHTTP && !isHTTPS) {
      newErrors.url = 'URL must start with http:// or https://';
    }

    const parsed = parseStatusRules(form.status_rules);
    if (parsed.error) newErrors.status_rules = parsed.error;

    if (form.max_latency_ms.trim()) {
      const maxLatency = parseInt(form.max_latency_ms, 10);
      if (isNaN(maxLatency) || maxLatency <= 0) {
        newErrors.max_latency_ms = 'Max latency must be a positive number (ms)';
      }
    }

    if (form.max_redirects < 0) {
      newErrors.max_redirects = 'Max redirects must be 0 or greater';
    }

    const tlsDaysStr = form.tls_min_days_valid.trim();
    if (tlsDaysStr) {
      const days = parseInt(tlsDaysStr, 10);
      if (isNaN(days) || days < 0) {
        newErrors.tls_min_days_valid = 'TLS min days valid must be 0 or greater';
      } else if (!isHTTPS) {
        newErrors.tls_min_days_valid = 'TLS checks require an https:// URL';
      }
    }

    const hasTLSSettings =
      form.tls_skip_verify ||
      !!form.tls_ca_pem.trim() ||
      !!form.tls_server_name.trim();

    if (hasTLSSettings && url && !isHTTPS) {
      newErrors.url = 'TLS settings require an https:// URL';
    }

    const headerRows = (form.request_headers || []).filter(h => h.key.trim() || h.value.trim());
    for (const row of headerRows) {
      if (!row.key.trim() || !row.value.trim()) {
        newErrors.request_headers = 'Headers must have both a name and a value';
        break;
      }
    }
    if ((form.display_fields || []).some(f => f.label?.trim() && !f.path.trim())) {
      newErrors.display_fields = 'Each display value needs a path';
    }
  } else if (type === 'ping') {
    if (!form.host.trim()) newErrors.host = 'Host is required';
  } else if (type === 'dns') {
    if (!form.host.trim()) newErrors.host = 'Host is required';
  } else if (type === 'synthetic_api') {
    if (form.synthetic_api_base_url.trim()) {
      const baseURL = form.synthetic_api_base_url.trim();
      if (!baseURL.startsWith('http://') && !baseURL.startsWith('https://')) {
        newErrors.synthetic_api_base_url = 'Base URL must start with http:// or https://';
      }
    }

    const variableRows = (form.synthetic_api_variables || []).filter(v => v.key.trim() || v.value.trim());
    for (const row of variableRows) {
      if (!row.key.trim()) {
        newErrors.synthetic_api_variables = 'Each variable requires a key';
        break;
      }
    }

    const steps = form.synthetic_api_steps || [];
    if (steps.length === 0) {
      newErrors.synthetic_api_steps = 'At least one API step is required';
    } else {
      const ids = new Set<string>();
      const extractNames = new Set<string>();
      for (const step of steps) {
        const id = step.id.trim();
        if (!id) {
          newErrors.synthetic_api_steps = 'Each API step requires an ID';
          break;
        }
        if (ids.has(id)) {
          newErrors.synthetic_api_steps = 'API step IDs must be unique';
          break;
        }
        ids.add(id);
        if (!step.url.trim()) {
          newErrors.synthetic_api_steps = `Step '${id}' requires a URL`;
          break;
        }
        if (!step.method.trim()) {
          newErrors.synthetic_api_steps = `Step '${id}' requires an HTTP method`;
          break;
        }
        if (step.timeout_seconds.trim()) {
          const timeout = parseInt(step.timeout_seconds, 10);
          if (isNaN(timeout) || timeout <= 0) {
            newErrors.synthetic_api_steps = `Step '${id}' has invalid timeout`;
            break;
          }
        }
        if (step.max_redirects.trim()) {
          const maxRedirects = parseInt(step.max_redirects, 10);
          if (isNaN(maxRedirects) || maxRedirects < 0) {
            newErrors.synthetic_api_steps = `Step '${id}' has invalid max redirects`;
            break;
          }
        }
        const headerRows = step.headers.filter(h => h.key.trim() || h.value.trim());
        for (const header of headerRows) {
          if (!header.key.trim() || !header.value.trim()) {
            newErrors.synthetic_api_steps = `Step '${id}' has invalid headers`;
            break;
          }
        }
        if (newErrors.synthetic_api_steps) break;

        for (let assertionIdx = 0; assertionIdx < step.assertions.length; assertionIdx += 1) {
          const assertion = step.assertions[assertionIdx];
          const target = normalizeAssertionTarget(assertion.target);
          const op = assertion.op.trim().toLowerCase();
          const allowedOps = syntheticAssertionOpsByTarget[target];

          if (!op || !allowedOps.includes(op)) {
            newErrors.synthetic_api_steps = `Step '${id}' assertion #${assertionIdx + 1} has invalid operator`;
            break;
          }

          if (assertionPathRequired(target) && !assertion.path.trim()) {
            newErrors.synthetic_api_steps = `Step '${id}' assertion #${assertionIdx + 1} requires a path`;
            break;
          }

          const requiresValue = assertionValueRequired(target, op);
          const parsedValue = parseAssertionValueInput(assertion.value_input);

          if (requiresValue && (parsedValue === undefined || (typeof parsedValue === 'string' && !parsedValue.trim()))) {
            newErrors.synthetic_api_steps = `Step '${id}' assertion #${assertionIdx + 1} requires a value`;
            break;
          }

          if (target === 'status') {
            if (op === 'in') {
              if (!Array.isArray(parsedValue) || parsedValue.length === 0 || parsedValue.some((v) => !isIntegerLike(v))) {
                newErrors.synthetic_api_steps = `Step '${id}' assertion #${assertionIdx + 1} expects a non-empty array of status codes`;
                break;
              }
            } else if (!isIntegerLike(parsedValue)) {
              newErrors.synthetic_api_steps = `Step '${id}' assertion #${assertionIdx + 1} expects an integer status code`;
              break;
            }
          }

          if (target === 'json' && (op === 'number_gt' || op === 'number_gte' || op === 'number_lt' || op === 'number_lte')) {
            if (!isNumberLike(parsedValue)) {
              newErrors.synthetic_api_steps = `Step '${id}' assertion #${assertionIdx + 1} expects a numeric value`;
              break;
            }
          }

          if (target === 'json' && op === 'bool_is') {
            if (!isBooleanLike(parsedValue)) {
              newErrors.synthetic_api_steps = `Step '${id}' assertion #${assertionIdx + 1} expects true or false`;
              break;
            }
          }

          if ((op === 'regex' || op === 'not_regex') && parsedValue !== undefined) {
            try {
              // Validate regex syntax early to avoid backend validation round-trip.
              new RegExp(String(parsedValue));
            } catch {
              newErrors.synthetic_api_steps = `Step '${id}' assertion #${assertionIdx + 1} has invalid regex`;
              break;
            }
          }
        }
        if (newErrors.synthetic_api_steps) break;

        for (let extractIdx = 0; extractIdx < step.extracts.length; extractIdx += 1) {
          const extract = step.extracts[extractIdx];
          const name = extract.name.trim();
          const path = extract.path.trim();
          if (!name && !path) {
            continue;
          }
          if (!name) {
            newErrors.synthetic_api_steps = `Step '${id}' extract #${extractIdx + 1} requires a name`;
            break;
          }
          if (extractNames.has(name)) {
            newErrors.synthetic_api_steps = `Step '${id}' extract #${extractIdx + 1} name must be globally unique`;
            break;
          }
          extractNames.add(name);
          if (extract.from !== 'json' && extract.from !== 'header') {
            newErrors.synthetic_api_steps = `Step '${id}' extract #${extractIdx + 1} has invalid source`;
            break;
          }
          if (!path) {
            newErrors.synthetic_api_steps = `Step '${id}' extract #${extractIdx + 1} requires a path`;
            break;
          }
        }
        if (newErrors.synthetic_api_steps) break;
      }
    }
  } else if (type === 'synthetic_browser') {
    const startURL = (guidedSyntheticBrowser?.startURL || form.synthetic_browser_start_url).trim();
    if (!startURL) {
      newErrors.synthetic_browser_start_url = 'Start URL is required';
    } else if (!startURL.startsWith('http://') && !startURL.startsWith('https://')) {
      newErrors.synthetic_browser_start_url = 'Start URL must start with http:// or https://';
    }

    if (guidedMode) {
      if (normalizeSyntheticBrowserGuidedTemplate(form.synthetic_browser_guided_template) === 'login_flow') {
        if (!form.synthetic_browser_guided_submit_selector.trim()) {
          newErrors.synthetic_browser_steps = 'Submit selector is required';
        } else if (!form.synthetic_browser_guided_post_login_selector.trim()) {
          newErrors.synthetic_browser_steps = 'Post-login selector is required';
        } else if (!form.synthetic_browser_guided_expected_url.trim()) {
          newErrors.synthetic_browser_steps = 'Expected URL value is required';
        } else if (
          form.synthetic_browser_guided_username.trim() &&
          !form.synthetic_browser_guided_email_selector.trim()
        ) {
          newErrors.synthetic_browser_steps = 'Email selector is required when username is set';
        } else if (
          form.synthetic_browser_guided_password.trim() &&
          !form.synthetic_browser_guided_password_selector.trim()
        ) {
          newErrors.synthetic_browser_steps = 'Password selector is required when password is set';
        }
      }

      if (!guidedSyntheticBrowser || guidedSyntheticBrowser.steps.length === 0) {
        newErrors.synthetic_browser_steps = 'Unable to build browser steps from guided setup';
      }
    } else {
      const variableRows = (form.synthetic_browser_variables || []).filter(v => v.key.trim() || v.value.trim());
      for (const row of variableRows) {
        if (!row.key.trim()) {
          newErrors.synthetic_browser_variables = 'Each variable requires a key';
          break;
        }
      }

      const steps = form.synthetic_browser_steps || [];
      if (steps.length === 0) {
        newErrors.synthetic_browser_steps = 'At least one browser step is required';
      } else {
        const ids = new Set<string>();
        for (const step of steps) {
          const id = step.id.trim();
          if (!id) {
            newErrors.synthetic_browser_steps = 'Each browser step requires an ID';
            break;
          }
          if (ids.has(id)) {
            newErrors.synthetic_browser_steps = 'Browser step IDs must be unique';
            break;
          }
          ids.add(id);

          if (step.action === 'goto' && !step.url.trim()) {
            newErrors.synthetic_browser_steps = `Step '${id}' requires a URL`;
            break;
          }
          if ((step.action === 'click' || step.action === 'wait_for' || step.action === 'assert_visible' || step.action === 'fill' || step.action === 'assert_text') && !step.selector.trim()) {
            newErrors.synthetic_browser_steps = `Step '${id}' requires a selector`;
            break;
          }
          if ((step.action === 'fill' || step.action === 'assert_text' || step.action === 'assert_url') && !step.value.trim()) {
            newErrors.synthetic_browser_steps = `Step '${id}' requires a value`;
            break;
          }
          if (step.timeout_seconds.trim()) {
            const timeout = parseInt(step.timeout_seconds, 10);
            if (isNaN(timeout) || timeout <= 0) {
              newErrors.synthetic_browser_steps = `Step '${id}' has invalid timeout`;
              break;
            }
          }
        }
      }
    }
  }

  if (form.timeout_seconds >= form.interval_seconds) {
    newErrors.timeout_seconds = 'Timeout must be less than interval';
  }

  return newErrors;
}

/**
 * The config a submit sends, for a form that passed validateMonitorForm.
 * Only for an InlineMonitorType: any other type falls through to the
 * synthetic browser branch (MonitorForm delegates those before a submit).
 */
export function buildMonitorFormConfig(type: MonitorType, form: MonitorFormState, guidedMode: boolean) {
  let config:
    | HTTPMonitorConfig
    | PingMonitorConfig
    | DNSMonitorConfig
    | SyntheticAPIMonitorConfig
    | SyntheticBrowserMonitorConfig;

  if (type === 'http') {
    const parsed = parseStatusRules(form.status_rules);
    const httpConfig: HTTPMonitorConfig = {
      url: form.url.trim(),
      method: form.method,
    };

    const headers: Record<string, string> = {};
    for (const row of (form.request_headers || []).filter(h => h.key.trim() && h.value.trim())) {
      headers[row.key.trim()] = row.value;
    }
    if (Object.keys(headers).length > 0) {
      httpConfig.headers = headers;
    }

    if (parsed.codes.length > 0) httpConfig.expected_statuses = parsed.codes;
    if (parsed.ranges.length > 0) httpConfig.expected_status_ranges = parsed.ranges;
    if (parsed.classes.length > 0) httpConfig.expected_status_classes = parsed.classes;

    if (form.body.trim()) {
      httpConfig.body = form.body.trim();
    }

    const bodyAssertions = (form.body_assertions || [])
      .map(a => ({
        op: a.op,
        value: a.value.trim(),
        ...(a.case_insensitive ? { case_insensitive: true } : {}),
      } as HTTPBodyAssertion))
      .filter(a => a.value);
    if (bodyAssertions.length > 0) {
      httpConfig.body_assertions = bodyAssertions;
    }

    const headerAssertions = (form.response_header_assertions || [])
      .map(a => ({
        name: a.name.trim(),
        op: a.op,
        ...(a.value && a.value.trim() ? { value: a.value } : {}),
        ...(a.case_insensitive ? { case_insensitive: true } : {}),
      } as HTTPHeaderAssertion))
      .filter(a => a.name);
    if (headerAssertions.length > 0) {
      httpConfig.response_header_assertions = headerAssertions;
    }

    const jsonAssertions = (form.json_assertions || [])
      .map(a => ({
        path: a.path.trim(),
        op: a.op,
        ...(a.value && a.value.trim() ? { value: a.value } : {}),
        ...(a.case_insensitive ? { case_insensitive: true } : {}),
      } as HTTPJSONAssertion))
      .filter(a => a.path);
    if (jsonAssertions.length > 0) {
      httpConfig.json_assertions = jsonAssertions;
    }

    const displayFields = (form.display_fields || [])
      .map(f => ({
        ...(f.label?.trim() ? { label: f.label.trim() } : {}),
        path: f.path.trim(),
      } as HTTPDisplayField))
      .filter(f => f.path);
    if (displayFields.length > 0) {
      httpConfig.display_fields = displayFields;
    }

    if (form.max_latency_ms.trim()) {
      const maxLatency = parseInt(form.max_latency_ms, 10);
      if (!isNaN(maxLatency)) httpConfig.max_latency_ms = maxLatency;
    }

    httpConfig.follow_redirects = form.follow_redirects;
    httpConfig.max_redirects = form.max_redirects;
    httpConfig.collect_timing = form.collect_timing;

    if (form.tls_skip_verify) httpConfig.tls_skip_verify = true;
    if (form.tls_min_days_valid.trim()) {
      const days = parseInt(form.tls_min_days_valid, 10);
      if (!isNaN(days)) httpConfig.tls_min_days_valid = days;
    }
    if (form.tls_server_name.trim()) httpConfig.tls_server_name = form.tls_server_name.trim();
    if (form.tls_ca_pem.trim()) httpConfig.tls_ca_pem = form.tls_ca_pem.trim();

    config = httpConfig;
  } else if (type === 'ping') {
    config = { host: form.host.trim() };
  } else if (type === 'dns') {
    const dnsExpected = parseDNSExpectedAnswers(form.dns_expected_answers);
    const dnsConfig: DNSMonitorConfig = {
      host: form.host.trim(),
      record_type: form.dns_record_type || 'A',
    };
    if (dnsExpected.length > 0) {
      dnsConfig.expected_answers = dnsExpected;
    }
    if (form.dns_nameserver.trim()) {
      dnsConfig.nameserver = form.dns_nameserver.trim();
    }
    config = dnsConfig;
  } else if (type === 'synthetic_api') {
    const variables: Record<string, string> = {};
    for (const row of form.synthetic_api_variables || []) {
      const key = row.key.trim();
      if (key) {
        variables[key] = row.value;
      }
    }

    const steps: SyntheticAPIStepConfig[] = [];
    for (const step of form.synthetic_api_steps || []) {
      const headers: Record<string, string> = {};
      for (const row of step.headers.filter(h => h.key.trim() && h.value.trim())) {
        headers[row.key.trim()] = row.value;
      }

      const req: SyntheticAPIStepConfig['request'] = {
        method: step.method.trim().toUpperCase(),
        url: step.url.trim(),
      };
      if (Object.keys(headers).length > 0) req.headers = headers;
      if (step.body.trim()) req.body = step.body;
      if (step.timeout_seconds.trim()) {
        const timeout = parseInt(step.timeout_seconds, 10);
        if (!isNaN(timeout)) req.timeout_seconds = timeout;
      }
      req.follow_redirects = step.follow_redirects;
      if (step.max_redirects.trim()) {
        const maxRedirects = parseInt(step.max_redirects, 10);
        if (!isNaN(maxRedirects)) req.max_redirects = maxRedirects;
      }

      const stepConfig: SyntheticAPIStepConfig = {
        id: step.id.trim(),
        request: req,
      };
      if (step.name.trim()) stepConfig.name = step.name.trim();

      const assertions: SyntheticAPIAssertionConfig[] = [];
      for (const assertion of step.assertions) {
        const target = normalizeAssertionTarget(assertion.target);
        const op = assertion.op.trim().toLowerCase();
        if (!op) continue;
        const item: SyntheticAPIAssertionConfig = {
          target,
          op,
        };
        if (assertion.path.trim()) {
          item.path = assertion.path.trim();
        }
        if (assertion.value_input.trim()) {
          item.value = parseAssertionValueInput(assertion.value_input);
        }
        assertions.push(item);
      }
      if (assertions.length > 0) {
        stepConfig.assert = assertions;
      }

      const extracts: SyntheticAPIExtractConfig[] = [];
      for (const extract of step.extracts) {
        const name = extract.name.trim();
        const path = extract.path.trim();
        if (!name || !path) continue;
        const item: SyntheticAPIExtractConfig = {
          name,
          from: extract.from,
          path,
        };
        if (extract.sensitive) {
          item.sensitive = true;
        }
        extracts.push(item);
      }
      if (extracts.length > 0) {
        stepConfig.extract = extracts;
      }
      steps.push(stepConfig);
    }

    const syntheticAPIConfig: SyntheticAPIMonitorConfig = {
      failure_mode: form.synthetic_api_failure_mode as 'fail_fast' | 'continue',
      steps,
    };
    if (form.synthetic_api_base_url.trim()) {
      syntheticAPIConfig.base_url = form.synthetic_api_base_url.trim();
    }
    if (Object.keys(variables).length > 0) {
      syntheticAPIConfig.variables = variables;
    }
    config = syntheticAPIConfig;
  } else {
    if (guidedMode) {
      const guided = buildSyntheticBrowserGuidedDraft(syntheticBrowserGuidedDraftFromForm(form));

      const steps: SyntheticBrowserStepConfig[] = [];
      for (const step of guided.steps) {
        const stepConfig: SyntheticBrowserStepConfig = {
          id: step.id.trim(),
          action: step.action,
        };
        if (step.url.trim()) stepConfig.url = step.url.trim();
        if (step.selector.trim()) stepConfig.selector = step.selector.trim();
        if (step.value.trim()) stepConfig.value = step.value;
        steps.push(stepConfig);
      }

      const syntheticBrowserConfig: SyntheticBrowserMonitorConfig = {
        start_url: guided.startURL,
        device: 'Desktop Chrome',
        failure_mode: 'fail_fast',
        steps,
        artifacts: {
          screenshot_on_failure: true,
          trace_on_failure: false,
          har_on_failure: false,
        },
      };
      if (Object.keys(guided.variables).length > 0) {
        syntheticBrowserConfig.variables = guided.variables;
      }
      config = syntheticBrowserConfig;
    } else {
      const variables: Record<string, string> = {};
      for (const row of form.synthetic_browser_variables || []) {
        const key = row.key.trim();
        if (key) {
          variables[key] = row.value;
        }
      }

      const steps: SyntheticBrowserStepConfig[] = [];
      for (const step of form.synthetic_browser_steps || []) {
        const stepConfig: SyntheticBrowserStepConfig = {
          id: step.id.trim(),
          action: step.action,
        };
        if (step.url.trim()) stepConfig.url = step.url.trim();
        if (step.selector.trim()) stepConfig.selector = step.selector.trim();
        if (step.value.trim()) stepConfig.value = step.value;
        if (step.timeout_seconds.trim()) {
          const timeout = parseInt(step.timeout_seconds, 10);
          if (!isNaN(timeout)) stepConfig.timeout_seconds = timeout;
        }
        steps.push(stepConfig);
      }

      const syntheticBrowserConfig: SyntheticBrowserMonitorConfig = {
        start_url: form.synthetic_browser_start_url.trim(),
        device: form.synthetic_browser_device.trim() || undefined,
        failure_mode: form.synthetic_browser_failure_mode as 'fail_fast' | 'continue',
        steps,
        artifacts: {
          screenshot_on_failure: form.synthetic_browser_screenshot_on_failure,
          trace_on_failure: form.synthetic_browser_trace_on_failure,
          har_on_failure: form.synthetic_browser_har_on_failure,
        },
      };
      if (Object.keys(variables).length > 0) {
        syntheticBrowserConfig.variables = variables;
      }
      config = syntheticBrowserConfig;
    }
  }

  return config;
}

/** The create/update request a submit sends (MonitorForm's handleSubmit). */
export function buildMonitorFormRequest(
  type: MonitorType,
  form: MonitorFormState,
  guidedMode: boolean
): CreateMonitorRequest {
  return buildScheduledMonitorRequest(type, buildMonitorFormConfig(type, form, guidedMode), form);
}
