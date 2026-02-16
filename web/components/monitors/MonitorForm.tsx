'use client';

import { useState, useEffect, useRef } from 'react';
import {
  Monitor,
  CreateMonitorRequest,
  UpdateMonitorRequest,
  CheckResult,
  AlertPolicy,
  MonitorType,
  HTTPMonitorConfig,
  PingMonitorConfig,
  DNSMonitorConfig,
  HTTPBodyAssertion,
  HTTPBodyAssertionOp,
  HTTPHeaderAssertion,
  HTTPHeaderAssertionOp,
  HTTPJSONAssertion,
  HTTPJSONAssertionOp,
  HTTPStatusRange,
  SyntheticAPIAssertionConfig,
  SyntheticAPIAssertionTarget,
  SyntheticAPIExtractConfig,
  SyntheticAPIMonitorConfig,
  SyntheticAPIStepConfig,
  SyntheticBrowserAction,
  SyntheticBrowserMonitorConfig,
  SyntheticBrowserStepConfig,
} from '@/lib/types';
import { getAlertPolicies, getMonitorResults, runMonitorNow } from '@/lib/api';
import GroupForm from './GroupForm';
import AgentForm from './AgentForm';
import PushForm from './PushForm';
import SipForm from './SipForm';

interface MonitorFormProps {
  monitor?: Monitor;
  onSubmit: (data: CreateMonitorRequest | UpdateMonitorRequest) => Promise<void>;
  onCancel?: () => void;
  loading?: boolean;
}

// Reusable input component
function FormInput({
  label,
  type = 'text',
  value,
  onChange,
  placeholder,
  error,
  hint,
  ...props
}: {
  label: string;
  type?: string;
  value: string | number;
  onChange: (value: string) => void;
  placeholder?: string;
  error?: string;
  hint?: string;
  [key: string]: any;
}) {
  return (
    <div>
      <label className="block text-xs font-medium text-slate-400 mb-1.5">{label}</label>
      <input
        type={type}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        className="input"
        {...props}
      />
      {error && <p className="mt-1 text-xs text-rose-400">{error}</p>}
      {hint && !error && <p className="mt-1 text-xs text-slate-500">{hint}</p>}
    </div>
  );
}

function FormTextarea({
  label,
  value,
  onChange,
  placeholder,
  error,
  hint,
  rows = 4,
  ...props
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  error?: string;
  hint?: string;
  rows?: number;
  [key: string]: any;
}) {
  return (
    <div>
      <label className="block text-xs font-medium text-slate-400 mb-1.5">{label}</label>
      <textarea
        rows={rows}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        className="input min-h-[96px]"
        {...props}
      />
      {error && <p className="mt-1 text-xs text-rose-400">{error}</p>}
      {hint && !error && <p className="mt-1 text-xs text-slate-500">{hint}</p>}
    </div>
  );
}

// Reusable select component
function FormSelect({
  label,
  value,
  onChange,
  options,
  hint,
  disabled,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  options: { value: string; label: string }[];
  hint?: string;
  disabled?: boolean;
}) {
  return (
    <div>
      <label className="block text-xs font-medium text-slate-400 mb-1.5">{label}</label>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        disabled={disabled}
        className="input"
      >
        {options.map((opt) => (
          <option key={opt.value} value={opt.value}>{opt.label}</option>
        ))}
      </select>
      {hint && <p className="mt-1 text-xs text-slate-500">{hint}</p>}
    </div>
  );
}

// Toggle component
function FormToggle({
  label,
  description,
  checked,
  onChange,
}: {
  label: string;
  description?: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
}) {
  return (
    <div className="flex items-center justify-between rounded-lg border border-white/[0.08] bg-slate-800/30 px-4 py-3">
      <div>
        <p className="text-sm font-medium text-white">{label}</p>
        {description && <p className="text-xs text-slate-500">{description}</p>}
      </div>
      <button
        type="button"
        onClick={() => onChange(!checked)}
        className={`relative h-5 w-9 rounded-full transition-colors ${
          checked ? 'bg-cyan-500' : 'bg-slate-700'
        }`}
      >
        <span
          className={`absolute top-0.5 left-0.5 h-4 w-4 rounded-full bg-white transition-transform ${
            checked ? 'translate-x-4' : ''
          }`}
        />
      </button>
    </div>
  );
}

// Section header
function SectionHeader({ title, description }: { title: string; description?: string }) {
  return (
    <div className="mb-4">
      <h3 className="text-sm font-medium text-white">{title}</h3>
      {description && <p className="text-xs text-slate-500 mt-0.5">{description}</p>}
    </div>
  );
}

// Monitor type card
function TypeCard({
  type,
  icon,
  label,
  description,
  selected,
  onClick,
  disabled,
}: {
  type: string;
  icon: React.ReactNode;
  label: string;
  description: string;
  selected: boolean;
  onClick: () => void;
  disabled?: boolean;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      className={`flex items-start gap-3 rounded-lg border p-3 text-left transition-all ${
        selected
          ? 'border-cyan-500/50 bg-cyan-500/10'
          : 'border-white/[0.06] bg-slate-800/30 hover:border-white/[0.1] hover:bg-slate-800/50'
      } ${disabled ? 'opacity-50 cursor-not-allowed' : 'cursor-pointer'}`}
    >
      <div className={`rounded-lg p-2 ${selected ? 'bg-cyan-500/20 text-cyan-400' : 'bg-slate-700/50 text-slate-400'}`}>
        {icon}
      </div>
      <div>
        <p className={`text-sm font-medium ${selected ? 'text-white' : 'text-slate-300'}`}>{label}</p>
        <p className="text-xs text-slate-500 mt-0.5">{description}</p>
      </div>
    </button>
  );
}

type HeaderKV = { key: string; value: string; reveal?: boolean };
type SyntheticVarKV = { key: string; value: string };
type SyntheticAPIAssertionDraft = {
  target: SyntheticAPIAssertionTarget;
  op: string;
  path: string;
  value_input: string;
};
type SyntheticAPIExtractDraft = {
  name: string;
  from: SyntheticAPIExtractConfig['from'];
  path: string;
  sensitive: boolean;
};
type SyntheticAPIStepDraft = {
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
type SyntheticBrowserStepDraft = {
  id: string;
  action: SyntheticBrowserAction;
  url: string;
  selector: string;
  value: string;
  timeout_seconds: string;
};

type SyntheticEditorMode = 'basic' | 'advanced';
type SyntheticTestPhase = 'idle' | 'queueing' | 'polling' | 'success' | 'failure' | 'error' | 'timeout';
type SyntheticBrowserSetupMode = 'guided' | 'expert';
type SyntheticBrowserGuidedTemplate = 'homepage_smoke' | 'login_flow';
type SyntheticBrowserGuidedStep = 1 | 2 | 3;

type SyntheticTestState = {
  phase: SyntheticTestPhase;
  message?: string;
  queuedAt?: string;
  jobId?: string;
  result?: CheckResult;
};

type SyntheticBrowserGuidedDraft = {
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

const isSensitiveHeaderName = (name: string) => {
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

const buildStatusRulesFromHTTPConfig = (cfg?: HTTPMonitorConfig) => {
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

const parseStatusRules = (input: string): { codes: number[]; ranges: HTTPStatusRange[]; classes: string[]; error?: string } => {
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

const parseDNSExpectedAnswers = (input: string): string[] => {
  return input
    .split(/[\n,]/)
    .map((item) => item.trim())
    .filter((item) => item.length > 0);
};

const defaultHeaderRows = (): HeaderKV[] => [{ key: '', value: '', reveal: false }];

const mapHeadersToRows = (headers?: Record<string, string>): HeaderKV[] => {
  if (!headers || Object.keys(headers).length === 0) return defaultHeaderRows();
  const rows = Object.entries(headers).map(([key, value]) => ({ key, value, reveal: false }));
  return rows.length > 0 ? rows : defaultHeaderRows();
};

const mapVariablesToRows = (variables?: Record<string, string>): SyntheticVarKV[] => {
  if (!variables || Object.keys(variables).length === 0) return [{ key: '', value: '' }];
  const rows = Object.entries(variables).map(([key, value]) => ({ key, value: String(value) }));
  return rows.length > 0 ? rows : [{ key: '', value: '' }];
};

const syntheticAssertionOpsByTarget: Record<SyntheticAPIAssertionTarget, string[]> = {
  status: ['equals', 'not_equals', 'in'],
  header: ['exists', 'equals', 'not_equals', 'contains', 'not_contains', 'regex', 'not_regex'],
  body: ['exists', 'equals', 'not_equals', 'contains', 'not_contains', 'regex', 'not_regex'],
  json: ['exists', 'equals', 'not_equals', 'contains', 'not_contains', 'regex', 'not_regex', 'number_gt', 'number_gte', 'number_lt', 'number_lte', 'bool_is'],
};

const defaultSyntheticAssertionForTarget = (target: SyntheticAPIAssertionTarget): SyntheticAPIAssertionDraft => {
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

const defaultSyntheticAPIAssertion = (): SyntheticAPIAssertionDraft => defaultSyntheticAssertionForTarget('status');

const defaultSyntheticAPIExtract = (): SyntheticAPIExtractDraft => ({
  name: '',
  from: 'json',
  path: '',
  sensitive: false,
});

const normalizeAssertionTarget = (target: string): SyntheticAPIAssertionTarget => {
  if (target === 'status' || target === 'header' || target === 'body' || target === 'json') {
    return target;
  }
  return 'status';
};

const assertionPathRequired = (target: SyntheticAPIAssertionTarget): boolean => target === 'header' || target === 'json';

const assertionValueRequired = (target: SyntheticAPIAssertionTarget, op: string): boolean => {
  const normalizedOp = op.trim().toLowerCase();
  if (target === 'status') return true;
  return normalizedOp !== 'exists';
};

const parseAssertionValueInput = (input: string): unknown => {
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

const assertionValuePlaceholder = (target: SyntheticAPIAssertionTarget, op: string): string => {
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

const mapSyntheticAPIAssertionsToDraft = (assertions?: SyntheticAPIAssertionConfig[]): SyntheticAPIAssertionDraft[] => {
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
      value_input: stringifyAssertionValueInput(assertion.value),
    };
  });
};

const mapSyntheticAPIExtractsToDraft = (extracts?: SyntheticAPIExtractConfig[]): SyntheticAPIExtractDraft[] => {
  if (!extracts || extracts.length === 0) return [];
  return extracts.map((extract) => ({
    name: extract.name || '',
    from: extract.from === 'header' ? 'header' : 'json',
    path: extract.path || '',
    sensitive: !!extract.sensitive,
  }));
};

const defaultSyntheticAPIStep = (index: number): SyntheticAPIStepDraft => ({
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

const defaultSyntheticBrowserStep = (index: number): SyntheticBrowserStepDraft => ({
  id: `step_${index}`,
  action: 'goto',
  url: '',
  selector: '',
  value: '',
  timeout_seconds: '',
});

const syntheticBrowserActionMeta: Record<
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

const mapSyntheticAPIStepsToDraft = (steps?: SyntheticAPIStepConfig[]): SyntheticAPIStepDraft[] => {
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

const mapSyntheticBrowserStepsToDraft = (steps?: SyntheticBrowserStepConfig[]): SyntheticBrowserStepDraft[] => {
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

const defaultSyntheticBrowserGuidedDraft = (
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

const normalizeSyntheticBrowserGuidedTemplate = (value: string): SyntheticBrowserGuidedTemplate =>
  value === 'login_flow' ? 'login_flow' : 'homepage_smoke';

const buildSyntheticBrowserGuidedDraft = (draft: SyntheticBrowserGuidedDraft): {
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

export default function MonitorForm({
  monitor,
  onSubmit,
  onCancel,
  loading = false,
}: MonitorFormProps) {
  const [alertPolicies, setAlertPolicies] = useState<AlertPolicy[]>([]);
  const initialType: MonitorType = monitor?.type || 'http';
  const [monitorType, setMonitorType] = useState<MonitorType>(initialType);

  const initialHTTPConfig: HTTPMonitorConfig | undefined =
    monitor && monitor.type === 'http' ? (monitor.config as HTTPMonitorConfig) : undefined;
  const initialPingConfig: PingMonitorConfig | undefined =
    monitor && monitor.type === 'ping' ? (monitor.config as PingMonitorConfig) : undefined;
  const initialDNSConfig: DNSMonitorConfig | undefined =
    monitor && monitor.type === 'dns' ? (monitor.config as DNSMonitorConfig) : undefined;
  const initialSyntheticAPIConfig: SyntheticAPIMonitorConfig | undefined =
    monitor && monitor.type === 'synthetic_api' ? (monitor.config as SyntheticAPIMonitorConfig) : undefined;
  const initialSyntheticBrowserConfig: SyntheticBrowserMonitorConfig | undefined =
    monitor && monitor.type === 'synthetic_browser' ? (monitor.config as SyntheticBrowserMonitorConfig) : undefined;
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

  const [formData, setFormData] = useState({
    name: monitor?.name || '',
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
    interval_seconds: monitor?.interval_seconds || 60,
    timeout_seconds: monitor?.timeout_seconds || 30,
    alert_policy_ids: monitor?.alert_policy_ids || (monitor?.alert_policy_id ? [monitor.alert_policy_id] : []),
    enabled: monitor?.enabled ?? true,
    tags: monitor?.tags?.join(', ') || '',
  });

  const [errors, setErrors] = useState<Record<string, string>>({});
  const [syntheticAPIMode, setSyntheticAPIMode] = useState<SyntheticEditorMode>('basic');
  const [syntheticBrowserMode, setSyntheticBrowserMode] = useState<SyntheticEditorMode>('basic');
  const [syntheticBrowserSetupMode, setSyntheticBrowserSetupMode] = useState<SyntheticBrowserSetupMode>(
    monitor ? 'expert' : 'guided'
  );
  const [syntheticBrowserGuidedStep, setSyntheticBrowserGuidedStep] = useState<SyntheticBrowserGuidedStep>(1);
  const [syntheticTestState, setSyntheticTestState] = useState<SyntheticTestState>({ phase: 'idle' });
  const testSequenceRef = useRef(0);
  const mountedRef = useRef(true);

  const isSavedMonitor = Boolean(monitor?.id);
  const isSyntheticMonitor = monitorType === 'synthetic_api' || monitorType === 'synthetic_browser';
  const isTestRunning = syntheticTestState.phase === 'queueing' || syntheticTestState.phase === 'polling';
  const isSyntheticBrowserGuidedMode =
    monitorType === 'synthetic_browser' && syntheticBrowserSetupMode === 'guided';

  const syntheticBrowserGuidedDraft: SyntheticBrowserGuidedDraft = {
    template: normalizeSyntheticBrowserGuidedTemplate(formData.synthetic_browser_guided_template),
    start_url: formData.synthetic_browser_start_url,
    must_see_selector: formData.synthetic_browser_guided_must_see_selector,
    email_selector: formData.synthetic_browser_guided_email_selector,
    password_selector: formData.synthetic_browser_guided_password_selector,
    submit_selector: formData.synthetic_browser_guided_submit_selector,
    post_login_selector: formData.synthetic_browser_guided_post_login_selector,
    expected_url: formData.synthetic_browser_guided_expected_url,
    username: formData.synthetic_browser_guided_username,
    password: formData.synthetic_browser_guided_password,
  };
  const syntheticBrowserGuidedPreview = buildSyntheticBrowserGuidedDraft(syntheticBrowserGuidedDraft);

  const syncGuidedBrowserIntoExpert = () => {
    const next = buildSyntheticBrowserGuidedDraft({
      template: normalizeSyntheticBrowserGuidedTemplate(formData.synthetic_browser_guided_template),
      start_url: formData.synthetic_browser_start_url,
      must_see_selector: formData.synthetic_browser_guided_must_see_selector,
      email_selector: formData.synthetic_browser_guided_email_selector,
      password_selector: formData.synthetic_browser_guided_password_selector,
      submit_selector: formData.synthetic_browser_guided_submit_selector,
      post_login_selector: formData.synthetic_browser_guided_post_login_selector,
      expected_url: formData.synthetic_browser_guided_expected_url,
      username: formData.synthetic_browser_guided_username,
      password: formData.synthetic_browser_guided_password,
    });

    setFormData((prev) => ({
      ...prev,
      synthetic_browser_start_url: next.startURL,
      synthetic_browser_device: 'Desktop Chrome',
      synthetic_browser_failure_mode: 'fail_fast',
      synthetic_browser_steps: next.steps,
      synthetic_browser_variables: mapVariablesToRows(next.variables),
      synthetic_browser_screenshot_on_failure: true,
      synthetic_browser_trace_on_failure: false,
      synthetic_browser_har_on_failure: false,
    }));
    setErrors((prev) => ({
      ...prev,
      synthetic_browser_steps: '',
      synthetic_browser_start_url: '',
      synthetic_browser_variables: '',
    }));
  };

  const switchSyntheticBrowserSetupMode = (nextMode: SyntheticBrowserSetupMode) => {
    if (syntheticBrowserSetupMode === nextMode) return;
    if (nextMode === 'expert' && monitorType === 'synthetic_browser') {
      syncGuidedBrowserIntoExpert();
    }
    setSyntheticBrowserSetupMode(nextMode);
    if (nextMode === 'guided') {
      setSyntheticBrowserGuidedStep(1);
    }
  };

  const getSyntheticResultBadgeClasses = (phase: SyntheticTestPhase) => {
    switch (phase) {
      case 'success':
        return 'border-emerald-500/30 bg-emerald-500/10 text-emerald-300';
      case 'failure':
        return 'border-rose-500/30 bg-rose-500/10 text-rose-300';
      case 'error':
      case 'timeout':
        return 'border-amber-500/30 bg-amber-500/10 text-amber-300';
      case 'queueing':
      case 'polling':
        return 'border-cyan-500/30 bg-cyan-500/10 text-cyan-300';
      default:
        return 'border-white/[0.12] bg-slate-900/50 text-slate-300';
    }
  };

  const startSyntheticLiveTest = async () => {
    if (!monitor?.id || !isSyntheticMonitor || isTestRunning) return;

    const sequence = ++testSequenceRef.current;
    setSyntheticTestState({
      phase: 'queueing',
      message: 'Queuing on-demand run...',
    });

    try {
      const runResponse = await runMonitorNow(monitor.id);
      if (!mountedRef.current || sequence !== testSequenceRef.current) return;

      const queuedAtISO = runResponse.queued_at || new Date().toISOString();
      const queuedAtMs = new Date(queuedAtISO).getTime();
      setSyntheticTestState({
        phase: 'polling',
        message: 'Run queued. Waiting for worker result...',
        queuedAt: queuedAtISO,
        jobId: runResponse.job_id,
      });

      let matchedResult: CheckResult | null = null;
      const maxAttempts = 30;

      for (let attempt = 1; attempt <= maxAttempts; attempt++) {
        if (!mountedRef.current || sequence !== testSequenceRef.current) return;

        const response = await getMonitorResults(monitor.id, { limit: 10, since: queuedAtISO });
        const candidate = (response.results || []).find((result) => {
          const resultMs = new Date(result.created_at).getTime();
          return resultMs >= queuedAtMs - 1500;
        });

        if (candidate) {
          matchedResult = candidate;
          break;
        }

        setSyntheticTestState((prev) => ({
          ...prev,
          phase: 'polling',
          message: `Waiting for worker result... (${attempt}/${maxAttempts})`,
        }));

        await new Promise((resolve) => setTimeout(resolve, 2000));
      }

      if (!mountedRef.current || sequence !== testSequenceRef.current) return;

      if (!matchedResult) {
        setSyntheticTestState((prev) => ({
          ...prev,
          phase: 'timeout',
          message: 'No fresh result found yet. Check worker/scheduler health and try again.',
        }));
        return;
      }

      const finalPhase: SyntheticTestPhase = matchedResult.status === 'success' ? 'success' : 'failure';
      setSyntheticTestState({
        phase: finalPhase,
        queuedAt: queuedAtISO,
        jobId: runResponse.job_id,
        result: matchedResult,
        message: matchedResult.error_message || (finalPhase === 'success' ? 'Run completed successfully.' : 'Run failed.'),
      });
    } catch (err: any) {
      if (!mountedRef.current || sequence !== testSequenceRef.current) return;
      setSyntheticTestState({
        phase: 'error',
        message: err?.message || 'Failed to run monitor on demand.',
      });
    }
  };

  useEffect(() => {
    loadAlertPolicies();
  }, []);

  useEffect(() => {
    if (initialSyntheticAPIConfig) {
      const hasAdvancedAPIConfig =
        Object.keys(initialSyntheticAPIConfig.variables || {}).length > 0 ||
        (initialSyntheticAPIConfig.steps || []).some((step) =>
          Object.keys(step.request?.headers || {}).length > 0 ||
          Boolean(step.request?.timeout_seconds) ||
          Boolean(step.request?.max_redirects) ||
          (step.assert || []).length > 0 ||
          (step.extract || []).length > 0
        );
      if (hasAdvancedAPIConfig) {
        setSyntheticAPIMode('advanced');
      }
    }

    if (initialSyntheticBrowserConfig) {
      const hasAdvancedBrowserConfig =
        Object.keys(initialSyntheticBrowserConfig.variables || {}).length > 0 ||
        Boolean(initialSyntheticBrowserConfig.device) ||
        (initialSyntheticBrowserConfig.steps || []).some((step) => Boolean(step.timeout_seconds));
      if (hasAdvancedBrowserConfig) {
        setSyntheticBrowserMode('advanced');
      }
    }
  }, [initialSyntheticAPIConfig, initialSyntheticBrowserConfig]);

  useEffect(() => {
    return () => {
      mountedRef.current = false;
      testSequenceRef.current += 1;
    };
  }, []);

  useEffect(() => {
    setSyntheticTestState({ phase: 'idle' });
    testSequenceRef.current += 1;
    if (monitorType === 'synthetic_browser' && !monitor) {
      setSyntheticBrowserSetupMode('guided');
      setSyntheticBrowserGuidedStep(1);
    }
  }, [monitorType, monitor]);

  const loadAlertPolicies = async () => {
    try {
      const response = await getAlertPolicies({ page_size: 100 });
      setAlertPolicies(response?.items || []);
    } catch (error) {
      console.error('Failed to load alert policies:', error);
    }
  };

  const applySyntheticAPITemplate = (template: 'health_auth' | 'single_health') => {
    setFormData((prev) => {
      if (template === 'single_health') {
        return {
          ...prev,
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
        ...prev,
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
    });

    setErrors((prev) => ({
      ...prev,
      synthetic_api_steps: '',
      synthetic_api_variables: '',
      synthetic_api_base_url: '',
    }));
  };

  const applySyntheticBrowserTemplate = (template: 'login_flow' | 'homepage_smoke') => {
    setFormData((prev) => {
      if (template === 'homepage_smoke') {
        return {
          ...prev,
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
        ...prev,
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
    });

    setErrors((prev) => ({
      ...prev,
      synthetic_browser_steps: '',
      synthetic_browser_variables: '',
      synthetic_browser_start_url: '',
    }));
  };

  const applySyntheticBrowserGuidedTemplate = (template: SyntheticBrowserGuidedTemplate) => {
    setFormData((prev) => {
      const defaults = defaultSyntheticBrowserGuidedDraft(
        template === 'login_flow' ? 'https://app.example.com/login' : 'https://app.example.com'
      );
      return {
        ...prev,
        synthetic_browser_guided_template: template,
        synthetic_browser_start_url: prev.synthetic_browser_start_url || defaults.start_url,
        synthetic_browser_guided_must_see_selector: defaults.must_see_selector,
        synthetic_browser_guided_email_selector: defaults.email_selector,
        synthetic_browser_guided_password_selector: defaults.password_selector,
        synthetic_browser_guided_submit_selector: defaults.submit_selector,
        synthetic_browser_guided_post_login_selector: defaults.post_login_selector,
        synthetic_browser_guided_expected_url: defaults.expected_url,
      };
    });

    setErrors((prev) => ({
      ...prev,
      synthetic_browser_start_url: '',
      synthetic_browser_steps: '',
      synthetic_browser_variables: '',
    }));
    setSyntheticBrowserGuidedStep(2);
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setErrors({});

    const newErrors: Record<string, string> = {};
    const guidedSyntheticBrowser = isSyntheticBrowserGuidedMode
      ? buildSyntheticBrowserGuidedDraft({
          template: normalizeSyntheticBrowserGuidedTemplate(formData.synthetic_browser_guided_template),
          start_url: formData.synthetic_browser_start_url,
          must_see_selector: formData.synthetic_browser_guided_must_see_selector,
          email_selector: formData.synthetic_browser_guided_email_selector,
          password_selector: formData.synthetic_browser_guided_password_selector,
          submit_selector: formData.synthetic_browser_guided_submit_selector,
          post_login_selector: formData.synthetic_browser_guided_post_login_selector,
          expected_url: formData.synthetic_browser_guided_expected_url,
          username: formData.synthetic_browser_guided_username,
          password: formData.synthetic_browser_guided_password,
        })
      : null;
    if (!formData.name.trim()) newErrors.name = 'Name is required';

    if (monitorType === 'http') {
      const url = formData.url.trim();
      const isHTTP = url.startsWith('http://');
      const isHTTPS = url.startsWith('https://');

      if (!url) {
        newErrors.url = 'URL is required';
      } else if (!isHTTP && !isHTTPS) {
        newErrors.url = 'URL must start with http:// or https://';
      }

      const parsed = parseStatusRules(formData.status_rules);
      if (parsed.error) newErrors.status_rules = parsed.error;

      if (formData.max_latency_ms.trim()) {
        const maxLatency = parseInt(formData.max_latency_ms, 10);
        if (isNaN(maxLatency) || maxLatency <= 0) {
          newErrors.max_latency_ms = 'Max latency must be a positive number (ms)';
        }
      }

      if (formData.max_redirects < 0) {
        newErrors.max_redirects = 'Max redirects must be 0 or greater';
      }

      const tlsDaysStr = formData.tls_min_days_valid.trim();
      if (tlsDaysStr) {
        const days = parseInt(tlsDaysStr, 10);
        if (isNaN(days) || days < 0) {
          newErrors.tls_min_days_valid = 'TLS min days valid must be 0 or greater';
        } else if (!isHTTPS) {
          newErrors.tls_min_days_valid = 'TLS checks require an https:// URL';
        }
      }

      const hasTLSSettings =
        formData.tls_skip_verify ||
        !!formData.tls_ca_pem.trim() ||
        !!formData.tls_server_name.trim();

      if (hasTLSSettings && url && !isHTTPS) {
        newErrors.url = 'TLS settings require an https:// URL';
      }

      const headerRows = (formData.request_headers || []).filter(h => h.key.trim() || h.value.trim());
      for (const row of headerRows) {
        if (!row.key.trim() || !row.value.trim()) {
          newErrors.request_headers = 'Headers must have both a name and a value';
          break;
        }
      }
    } else if (monitorType === 'ping') {
      if (!formData.host.trim()) newErrors.host = 'Host is required';
    } else if (monitorType === 'dns') {
      if (!formData.host.trim()) newErrors.host = 'Host is required';
    } else if (monitorType === 'synthetic_api') {
      if (formData.synthetic_api_base_url.trim()) {
        const baseURL = formData.synthetic_api_base_url.trim();
        if (!baseURL.startsWith('http://') && !baseURL.startsWith('https://')) {
          newErrors.synthetic_api_base_url = 'Base URL must start with http:// or https://';
        }
      }

      const variableRows = (formData.synthetic_api_variables || []).filter(v => v.key.trim() || v.value.trim());
      for (const row of variableRows) {
        if (!row.key.trim()) {
          newErrors.synthetic_api_variables = 'Each variable requires a key';
          break;
        }
      }

      const steps = formData.synthetic_api_steps || [];
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
    } else if (monitorType === 'synthetic_browser') {
      const startURL = (guidedSyntheticBrowser?.startURL || formData.synthetic_browser_start_url).trim();
      if (!startURL) {
        newErrors.synthetic_browser_start_url = 'Start URL is required';
      } else if (!startURL.startsWith('http://') && !startURL.startsWith('https://')) {
        newErrors.synthetic_browser_start_url = 'Start URL must start with http:// or https://';
      }

      if (isSyntheticBrowserGuidedMode) {
        if (normalizeSyntheticBrowserGuidedTemplate(formData.synthetic_browser_guided_template) === 'login_flow') {
          if (!formData.synthetic_browser_guided_submit_selector.trim()) {
            newErrors.synthetic_browser_steps = 'Submit selector is required';
          } else if (!formData.synthetic_browser_guided_post_login_selector.trim()) {
            newErrors.synthetic_browser_steps = 'Post-login selector is required';
          } else if (!formData.synthetic_browser_guided_expected_url.trim()) {
            newErrors.synthetic_browser_steps = 'Expected URL value is required';
          } else if (
            formData.synthetic_browser_guided_username.trim() &&
            !formData.synthetic_browser_guided_email_selector.trim()
          ) {
            newErrors.synthetic_browser_steps = 'Email selector is required when username is set';
          } else if (
            formData.synthetic_browser_guided_password.trim() &&
            !formData.synthetic_browser_guided_password_selector.trim()
          ) {
            newErrors.synthetic_browser_steps = 'Password selector is required when password is set';
          }
        }

        if (!guidedSyntheticBrowser || guidedSyntheticBrowser.steps.length === 0) {
          newErrors.synthetic_browser_steps = 'Unable to build browser steps from guided setup';
        }
      } else {
        const variableRows = (formData.synthetic_browser_variables || []).filter(v => v.key.trim() || v.value.trim());
        for (const row of variableRows) {
          if (!row.key.trim()) {
            newErrors.synthetic_browser_variables = 'Each variable requires a key';
            break;
          }
        }

        const steps = formData.synthetic_browser_steps || [];
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

    if (formData.timeout_seconds >= formData.interval_seconds) {
      newErrors.timeout_seconds = 'Timeout must be less than interval';
    }

    if (Object.keys(newErrors).length > 0) {
      setErrors(newErrors);
      return;
    }

    let config:
      | HTTPMonitorConfig
      | PingMonitorConfig
      | DNSMonitorConfig
      | SyntheticAPIMonitorConfig
      | SyntheticBrowserMonitorConfig;

    if (monitorType === 'http') {
      const parsed = parseStatusRules(formData.status_rules);
      const httpConfig: HTTPMonitorConfig = {
        url: formData.url.trim(),
        method: formData.method,
      };

      const headers: Record<string, string> = {};
      for (const row of (formData.request_headers || []).filter(h => h.key.trim() && h.value.trim())) {
        headers[row.key.trim()] = row.value;
      }
      if (Object.keys(headers).length > 0) {
        httpConfig.headers = headers;
      }

      if (parsed.codes.length > 0) httpConfig.expected_statuses = parsed.codes;
      if (parsed.ranges.length > 0) httpConfig.expected_status_ranges = parsed.ranges;
      if (parsed.classes.length > 0) httpConfig.expected_status_classes = parsed.classes;

      if (formData.body.trim()) {
        httpConfig.body = formData.body.trim();
      }

      const bodyAssertions = (formData.body_assertions || [])
        .map(a => ({
          op: a.op,
          value: a.value.trim(),
          ...(a.case_insensitive ? { case_insensitive: true } : {}),
        } as HTTPBodyAssertion))
        .filter(a => a.value);
      if (bodyAssertions.length > 0) {
        httpConfig.body_assertions = bodyAssertions;
      }

      const headerAssertions = (formData.response_header_assertions || [])
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

      const jsonAssertions = (formData.json_assertions || [])
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

      if (formData.max_latency_ms.trim()) {
        const maxLatency = parseInt(formData.max_latency_ms, 10);
        if (!isNaN(maxLatency)) httpConfig.max_latency_ms = maxLatency;
      }

      httpConfig.follow_redirects = formData.follow_redirects;
      httpConfig.max_redirects = formData.max_redirects;
      httpConfig.collect_timing = formData.collect_timing;

      if (formData.tls_skip_verify) httpConfig.tls_skip_verify = true;
      if (formData.tls_min_days_valid.trim()) {
        const days = parseInt(formData.tls_min_days_valid, 10);
        if (!isNaN(days)) httpConfig.tls_min_days_valid = days;
      }
      if (formData.tls_server_name.trim()) httpConfig.tls_server_name = formData.tls_server_name.trim();
      if (formData.tls_ca_pem.trim()) httpConfig.tls_ca_pem = formData.tls_ca_pem.trim();

      config = httpConfig;
    } else if (monitorType === 'ping') {
      config = { host: formData.host.trim() };
    } else if (monitorType === 'dns') {
      const dnsExpected = parseDNSExpectedAnswers(formData.dns_expected_answers);
      const dnsConfig: DNSMonitorConfig = {
        host: formData.host.trim(),
        record_type: formData.dns_record_type || 'A',
      };
      if (dnsExpected.length > 0) {
        dnsConfig.expected_answers = dnsExpected;
      }
      config = dnsConfig;
    } else if (monitorType === 'synthetic_api') {
      const variables: Record<string, string> = {};
      for (const row of formData.synthetic_api_variables || []) {
        const key = row.key.trim();
        if (key) {
          variables[key] = row.value;
        }
      }

      const steps: SyntheticAPIStepConfig[] = [];
      for (const step of formData.synthetic_api_steps || []) {
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
        failure_mode: formData.synthetic_api_failure_mode as 'fail_fast' | 'continue',
        steps,
      };
      if (formData.synthetic_api_base_url.trim()) {
        syntheticAPIConfig.base_url = formData.synthetic_api_base_url.trim();
      }
      if (Object.keys(variables).length > 0) {
        syntheticAPIConfig.variables = variables;
      }
      config = syntheticAPIConfig;
    } else {
      if (isSyntheticBrowserGuidedMode) {
        const guided = guidedSyntheticBrowser || buildSyntheticBrowserGuidedDraft({
          template: normalizeSyntheticBrowserGuidedTemplate(formData.synthetic_browser_guided_template),
          start_url: formData.synthetic_browser_start_url,
          must_see_selector: formData.synthetic_browser_guided_must_see_selector,
          email_selector: formData.synthetic_browser_guided_email_selector,
          password_selector: formData.synthetic_browser_guided_password_selector,
          submit_selector: formData.synthetic_browser_guided_submit_selector,
          post_login_selector: formData.synthetic_browser_guided_post_login_selector,
          expected_url: formData.synthetic_browser_guided_expected_url,
          username: formData.synthetic_browser_guided_username,
          password: formData.synthetic_browser_guided_password,
        });

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
        for (const row of formData.synthetic_browser_variables || []) {
          const key = row.key.trim();
          if (key) {
            variables[key] = row.value;
          }
        }

        const steps: SyntheticBrowserStepConfig[] = [];
        for (const step of formData.synthetic_browser_steps || []) {
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
          start_url: formData.synthetic_browser_start_url.trim(),
          device: formData.synthetic_browser_device.trim() || undefined,
          failure_mode: formData.synthetic_browser_failure_mode as 'fail_fast' | 'continue',
          steps,
          artifacts: {
            screenshot_on_failure: formData.synthetic_browser_screenshot_on_failure,
            trace_on_failure: formData.synthetic_browser_trace_on_failure,
            har_on_failure: formData.synthetic_browser_har_on_failure,
          },
        };
        if (Object.keys(variables).length > 0) {
          syntheticBrowserConfig.variables = variables;
        }
        config = syntheticBrowserConfig;
      }
    }

    const requestData: CreateMonitorRequest | UpdateMonitorRequest = {
      name: formData.name.trim(),
      type: monitorType,
      config,
      interval_seconds: formData.interval_seconds,
      timeout_seconds: formData.timeout_seconds,
      enabled: formData.enabled,
    };

    requestData.alert_policy_ids = formData.alert_policy_ids;
    if (formData.tags.trim()) {
      requestData.tags = formData.tags.split(',').map(t => t.trim()).filter(t => t);
    }

    await onSubmit(requestData);
  };

  // Type icons
    const typeIcons = {
      http: <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M21 12a9 9 0 01-9 9m9-9a9 9 0 00-9-9m9 9H3m9 9a9 9 0 01-9-9m9 9c1.657 0 3-4.03 3-9s-1.343-9-3-9m0 18c-1.657 0-3-4.03-3-9s1.343-9 3-9m-9 9a9 9 0 019-9" /></svg>,
      ping: <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M8.111 16.404a5.5 5.5 0 017.778 0M12 20h.01m-7.08-7.071c3.904-3.905 10.236-3.905 14.141 0M1.394 9.393c5.857-5.857 15.355-5.857 21.213 0" /></svg>,
      dns: <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M11 5a7 7 0 105.196 11.95l3.427 3.428a1 1 0 001.414-1.414l-3.428-3.427A7 7 0 0011 5z" /></svg>,
      group: <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 11H5m14 0a2 2 0 012 2v6a2 2 0 01-2 2H5a2 2 0 01-2-2v-6a2 2 0 012-2m14 0V9a2 2 0 00-2-2M5 11V9a2 2 0 012-2m0 0V5a2 2 0 012-2h6a2 2 0 012 2v2M7 7h10" /></svg>,
      agent: <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 3v2m6-2v2M9 19v2m6-2v2M5 9H3m2 6H3m18-6h-2m2 6h-2M7 19h10a2 2 0 002-2V7a2 2 0 00-2-2H7a2 2 0 00-2 2v10a2 2 0 002 2zM9 9h6v6H9V9z" /></svg>,
      push: <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-8l-4-4m0 0L8 8m4-4v12" /></svg>,
      sip: <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M3 5a2 2 0 012-2h3.28a1 1 0 01.948.684l1.498 4.493a1 1 0 01-.502 1.21l-2.257 1.13a11.042 11.042 0 005.516 5.516l1.13-2.257a1 1 0 011.21-.502l4.493 1.498a1 1 0 01.684.949V19a2 2 0 01-2 2h-1C9.716 21 3 14.284 3 6V5z" /></svg>,
      synthetic_api: <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M8 9l3 3-3 3m5 0h3M5 4h14a2 2 0 012 2v12a2 2 0 01-2 2H5a2 2 0 01-2-2V6a2 2 0 012-2z" /></svg>,
      synthetic_browser: <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9.75 17L9 20l6-3m-7-9h8m-8 4h6m-7-8h10a2 2 0 012 2v10a2 2 0 01-2 2H7a2 2 0 01-2-2V6a2 2 0 012-2z" /></svg>,
    };

    const monitorTypes: MonitorType[] = ['http', 'ping', 'dns', 'group', 'agent', 'push', 'sip', 'synthetic_api', 'synthetic_browser'];

    const getTypeLabel = (type: MonitorType) => {
      switch (type) {
        case 'http':
          return 'HTTP';
        case 'ping':
          return 'Ping';
        case 'dns':
          return 'DNS';
        case 'group':
          return 'Group';
        case 'agent':
          return 'Agent';
        case 'push':
          return 'Push';
        case 'sip':
          return 'SIP';
        case 'synthetic_api':
          return 'Synthetic API';
        case 'synthetic_browser':
          return 'Synthetic Browser';
        default:
          return 'Monitor';
      }
    };

    const getTypeDescription = (type: MonitorType) => {
      switch (type) {
        case 'http':
          return 'Monitor HTTP endpoints';
        case 'ping':
          return 'ICMP ping checks';
        case 'dns':
          return 'DNS record checks';
        case 'group':
          return 'Group multiple monitors';
        case 'agent':
          return 'System metrics agent';
        case 'push':
          return 'Webhook-based monitoring';
        case 'sip':
          return 'SIP OPTIONS checks';
        case 'synthetic_api':
          return 'Simulate real API user flows';
        case 'synthetic_browser':
          return 'Simulate end-to-end browser journeys';
        default:
          return '';
      }
    };

  // Delegate to specialized forms
  if (monitorType === 'group') {
    return (
      <div className="space-y-6">
        <div>
          <SectionHeader title="Monitor Type" />
          <div className="grid grid-cols-2 gap-3">
            {monitorTypes.map((type) => (
              <TypeCard
                key={type}
                type={type}
                icon={typeIcons[type]}
                label={getTypeLabel(type)}
                description={getTypeDescription(type)}
                selected={monitorType === type}
                onClick={() => setMonitorType(type)}
                disabled={!!monitor}
              />
            ))}
          </div>
        </div>
        <GroupForm monitor={monitor} onSubmit={onSubmit} onCancel={onCancel} loading={loading} />
      </div>
    );
  }

  if (monitorType === 'agent') {
    return (
      <div className="space-y-6">
        <div>
          <SectionHeader title="Monitor Type" />
          <div className="grid grid-cols-2 gap-3">
            {monitorTypes.map((type) => (
              <TypeCard
                key={type}
                type={type}
                icon={typeIcons[type]}
                label={getTypeLabel(type)}
                description={getTypeDescription(type)}
                selected={monitorType === type}
                onClick={() => setMonitorType(type)}
                disabled={!!monitor}
              />
            ))}
          </div>
        </div>
        <AgentForm monitor={monitor} onSubmit={onSubmit} onCancel={onCancel} loading={loading} />
      </div>
    );
  }

  if (monitorType === 'push') {
    return (
      <div className="space-y-6">
        <div>
          <SectionHeader title="Monitor Type" />
          <div className="grid grid-cols-2 gap-3">
            {monitorTypes.map((type) => (
              <TypeCard
                key={type}
                type={type}
                icon={typeIcons[type]}
                label={getTypeLabel(type)}
                description={getTypeDescription(type)}
                selected={monitorType === type}
                onClick={() => setMonitorType(type)}
                disabled={!!monitor}
              />
            ))}
          </div>
        </div>
        <PushForm monitor={monitor} onSubmit={onSubmit} onCancel={onCancel} loading={loading} />
      </div>
    );
  }

  if (monitorType === 'sip') {
    return (
      <div className="space-y-6">
        <div>
          <SectionHeader title="Monitor Type" />
          <div className="grid grid-cols-2 gap-3">
            {monitorTypes.map((type) => (
              <TypeCard
                key={type}
                type={type}
                icon={typeIcons[type]}
                label={getTypeLabel(type)}
                description={getTypeDescription(type)}
                selected={monitorType === type}
                onClick={() => setMonitorType(type)}
                disabled={!!monitor}
              />
            ))}
          </div>
        </div>
        <SipForm monitor={monitor} onSubmit={onSubmit} onCancel={onCancel} loading={loading} />
      </div>
    );
  }

  return (
    <form onSubmit={handleSubmit} className="space-y-6">
      {/* Monitor Type Selection */}
      <div>
        <SectionHeader title="Monitor Type" description="Choose how you want to monitor your service" />
        <div className="grid grid-cols-2 gap-3">
          {monitorTypes.map((type) => (
            <TypeCard
              key={type}
              type={type}
              icon={typeIcons[type]}
              label={getTypeLabel(type)}
              description={getTypeDescription(type)}
              selected={monitorType === type}
              onClick={() => setMonitorType(type)}
              disabled={!!monitor}
            />
          ))}
        </div>
      </div>

      {/* Basic Info */}
      <div>
        <SectionHeader title="Basic Information" />
        <div className="space-y-4">
          <FormInput
            label="Monitor Name"
            value={formData.name}
            onChange={(v) => setFormData({ ...formData, name: v })}
            placeholder="My API Health Check"
            error={errors.name}
            hint="A descriptive name for this monitor"
          />
        </div>
      </div>

      {/* HTTP Configuration */}
      {monitorType === 'http' && (
        <div>
          <SectionHeader title="HTTP Configuration" description="Configure the HTTP request" />
          <div className="space-y-4">
            <FormInput
              label="URL"
              type="url"
              value={formData.url}
              onChange={(v) => setFormData({ ...formData, url: v })}
              placeholder="https://api.example.com/health"
              error={errors.url}
            />

            <div className="grid grid-cols-2 gap-4">
              <FormSelect
                label="Method"
                value={formData.method}
                onChange={(v) => setFormData({ ...formData, method: v })}
                options={[
                  { value: 'GET', label: 'GET' },
                  { value: 'HEAD', label: 'HEAD' },
                  { value: 'POST', label: 'POST' },
                  { value: 'PUT', label: 'PUT' },
                  { value: 'DELETE', label: 'DELETE' },
                  { value: 'PATCH', label: 'PATCH' },
                  { value: 'OPTIONS', label: 'OPTIONS' },
                ]}
              />
              <FormInput
                label="Expected Status Rules (optional)"
                value={formData.status_rules}
                onChange={(v) => setFormData({ ...formData, status_rules: v })}
                placeholder="2xx, 304, 200-299"
                error={errors.status_rules}
                hint="Leave empty for default success: 2xx"
              />
            </div>

            <div>
              <label className="block text-xs font-medium text-slate-400 mb-1.5">Request Headers (optional)</label>
              {errors.request_headers && <p className="mt-1 text-xs text-rose-400">{errors.request_headers}</p>}
              <div className="space-y-2">
                {formData.request_headers.map((h: HeaderKV, idx: number) => {
                  const sensitive = isSensitiveHeaderName(h.key);
                  const showToggle = sensitive && h.value.trim().length > 0;
                  return (
                    <div key={`${idx}-${h.key}`} className="grid grid-cols-12 gap-2">
                      <input
                        className="input col-span-4"
                        placeholder="Header name"
                        value={h.key}
                        onChange={(e) => {
                          const next = [...formData.request_headers];
                          next[idx] = { ...next[idx], key: e.target.value };
                          setFormData({ ...formData, request_headers: next });
                        }}
                      />
                      <input
                        className="input col-span-6"
                        placeholder="Header value"
                        type={sensitive && !h.reveal ? 'password' : 'text'}
                        value={h.value}
                        onChange={(e) => {
                          const next = [...formData.request_headers];
                          next[idx] = { ...next[idx], value: e.target.value };
                          setFormData({ ...formData, request_headers: next });
                        }}
                      />
                      <button
                        type="button"
                        disabled={!showToggle}
                        onClick={() => {
                          const next = [...formData.request_headers];
                          next[idx] = { ...next[idx], reveal: !next[idx].reveal };
                          setFormData({ ...formData, request_headers: next });
                        }}
                        className="btn btn-secondary btn-sm col-span-1 disabled:opacity-50"
                        title={showToggle ? (h.reveal ? 'Hide value' : 'Show value') : 'Add a value to show/hide'}
                      >
                        {h.reveal ? 'Hide' : 'Show'}
                      </button>
                      <button
                        type="button"
                        onClick={() => {
                          const next = [...formData.request_headers];
                          if (next.length === 1) {
                            next[0] = { key: '', value: '', reveal: false };
                          } else {
                            next.splice(idx, 1);
                          }
                          setFormData({ ...formData, request_headers: next });
                        }}
                        className="btn btn-secondary btn-sm col-span-1"
                      >
                        Remove
                      </button>
                    </div>
                  );
                })}
              </div>
              <div className="mt-2">
                <button
                  type="button"
                  className="btn btn-secondary btn-sm"
                  onClick={() => setFormData({ ...formData, request_headers: [...formData.request_headers, { key: '', value: '', reveal: false }] })}
                >
                  + Add header
                </button>
              </div>
              <p className="mt-2 text-xs text-slate-500">Tip: Authorization / API key headers are hidden by default.</p>
            </div>

            <FormTextarea
              label="Request Body (optional)"
              value={formData.body}
              onChange={(v) => setFormData({ ...formData, body: v })}
              placeholder='{"status":"ok"}'
              hint="Sent as-is for POST/PUT/PATCH. Set Content-Type in headers if needed."
              rows={5}
            />

            <div className="rounded-lg border border-white/[0.06] bg-slate-800/20 p-4 space-y-4">
              <div className="text-xs font-medium text-slate-300">Response Checks (optional)</div>

              {/* Body Assertions */}
              <div>
                <label className="block text-xs font-medium text-slate-400 mb-1.5">Body assertions</label>
                <div className="space-y-2">
                  {(formData.body_assertions || []).map((a: HTTPBodyAssertion, idx: number) => (
                    <div key={`body-${idx}`} className="grid grid-cols-12 gap-2">
                      <select
                        className="input col-span-3"
                        value={a.op}
                        onChange={(e) => {
                          const next = [...formData.body_assertions];
                          next[idx] = { ...next[idx], op: e.target.value as HTTPBodyAssertionOp };
                          setFormData({ ...formData, body_assertions: next });
                        }}
                      >
                        <option value="contains">contains</option>
                        <option value="not_contains">not contains</option>
                        <option value="regex">regex</option>
                        <option value="not_regex">not regex</option>
                      </select>
                      <input
                        className="input col-span-7"
                        placeholder="Value / pattern"
                        value={a.value}
                        onChange={(e) => {
                          const next = [...formData.body_assertions];
                          next[idx] = { ...next[idx], value: e.target.value };
                          setFormData({ ...formData, body_assertions: next });
                        }}
                      />
                      <label className="col-span-1 flex items-center justify-center gap-1 text-xs text-slate-400">
                        <input
                          type="checkbox"
                          checked={!!a.case_insensitive}
                          onChange={(e) => {
                            const next = [...formData.body_assertions];
                            next[idx] = { ...next[idx], case_insensitive: e.target.checked };
                            setFormData({ ...formData, body_assertions: next });
                          }}
                        />
                        CI
                      </label>
                      <button
                        type="button"
                        className="btn btn-secondary btn-sm col-span-1"
                        onClick={() => {
                          const next = [...formData.body_assertions];
                          next.splice(idx, 1);
                          setFormData({ ...formData, body_assertions: next });
                        }}
                      >
                        ×
                      </button>
                    </div>
                  ))}
                </div>
                <div className="mt-2">
                  <button
                    type="button"
                    className="btn btn-secondary btn-sm"
                    onClick={() =>
                      setFormData({
                        ...formData,
                        body_assertions: [...(formData.body_assertions || []), { op: 'contains', value: '', case_insensitive: false }],
                      })
                    }
                  >
                    + Add body assertion
                  </button>
                </div>
              </div>

              {/* Response Header Assertions */}
              <div>
                <label className="block text-xs font-medium text-slate-400 mb-1.5">Response header assertions</label>
                <div className="space-y-2">
                  {(formData.response_header_assertions || []).map((a: HTTPHeaderAssertion, idx: number) => {
                    const needsValue = a.op !== 'exists';
                    return (
                      <div key={`hdr-${idx}`} className="grid grid-cols-12 gap-2">
                        <input
                          className="input col-span-4"
                          placeholder="Header name (e.g. Content-Type)"
                          value={a.name}
                          onChange={(e) => {
                            const next = [...formData.response_header_assertions];
                            next[idx] = { ...next[idx], name: e.target.value };
                            setFormData({ ...formData, response_header_assertions: next });
                          }}
                        />
                        <select
                          className="input col-span-3"
                          value={a.op}
                          onChange={(e) => {
                            const next = [...formData.response_header_assertions];
                            next[idx] = { ...next[idx], op: e.target.value as HTTPHeaderAssertionOp };
                            setFormData({ ...formData, response_header_assertions: next });
                          }}
                        >
                          <option value="exists">exists</option>
                          <option value="equals">equals</option>
                          <option value="contains">contains</option>
                          <option value="regex">regex</option>
                          <option value="not_equals">not equals</option>
                          <option value="not_contains">not contains</option>
                          <option value="not_regex">not regex</option>
                        </select>
                        <input
                          className="input col-span-3"
                          placeholder={needsValue ? 'Value / pattern' : '—'}
                          disabled={!needsValue}
                          value={a.value || ''}
                          onChange={(e) => {
                            const next = [...formData.response_header_assertions];
                            next[idx] = { ...next[idx], value: e.target.value };
                            setFormData({ ...formData, response_header_assertions: next });
                          }}
                        />
                        <label className="col-span-1 flex items-center justify-center gap-1 text-xs text-slate-400">
                          <input
                            type="checkbox"
                            checked={!!a.case_insensitive}
                            onChange={(e) => {
                              const next = [...formData.response_header_assertions];
                              next[idx] = { ...next[idx], case_insensitive: e.target.checked };
                              setFormData({ ...formData, response_header_assertions: next });
                            }}
                          />
                          CI
                        </label>
                        <button
                          type="button"
                          className="btn btn-secondary btn-sm col-span-1"
                          onClick={() => {
                            const next = [...formData.response_header_assertions];
                            next.splice(idx, 1);
                            setFormData({ ...formData, response_header_assertions: next });
                          }}
                        >
                          ×
                        </button>
                      </div>
                    );
                  })}
                </div>
                <div className="mt-2">
                  <button
                    type="button"
                    className="btn btn-secondary btn-sm"
                    onClick={() =>
                      setFormData({
                        ...formData,
                        response_header_assertions: [
                          ...(formData.response_header_assertions || []),
                          { name: '', op: 'exists', value: '', case_insensitive: false },
                        ],
                      })
                    }
                  >
                    + Add header assertion
                  </button>
                </div>
              </div>

              {/* JSON Assertions */}
              <div>
                <label className="block text-xs font-medium text-slate-400 mb-1.5">JSON assertions (gjson path)</label>
                <div className="space-y-2">
                  {(formData.json_assertions || []).map((a: HTTPJSONAssertion, idx: number) => {
                    const needsValue = a.op !== 'exists';
                    return (
                      <div key={`json-${idx}`} className="grid grid-cols-12 gap-2">
                        <input
                          className="input col-span-4"
                          placeholder="Path (e.g. data.status)"
                          value={a.path}
                          onChange={(e) => {
                            const next = [...formData.json_assertions];
                            next[idx] = { ...next[idx], path: e.target.value };
                            setFormData({ ...formData, json_assertions: next });
                          }}
                        />
                        <select
                          className="input col-span-3"
                          value={a.op}
                          onChange={(e) => {
                            const next = [...formData.json_assertions];
                            next[idx] = { ...next[idx], op: e.target.value as HTTPJSONAssertionOp };
                            setFormData({ ...formData, json_assertions: next });
                          }}
                        >
                          <option value="exists">exists</option>
                          <option value="equals">equals</option>
                          <option value="not_equals">not equals</option>
                          <option value="contains">contains</option>
                          <option value="not_contains">not contains</option>
                          <option value="regex">regex</option>
                          <option value="number_gt">number &gt;</option>
                          <option value="number_gte">number ≥</option>
                          <option value="number_lt">number &lt;</option>
                          <option value="number_lte">number ≤</option>
                          <option value="bool_is">bool is</option>
                        </select>
                        <input
                          className="input col-span-3"
                          placeholder={needsValue ? 'Value / pattern' : '—'}
                          disabled={!needsValue}
                          value={a.value || ''}
                          onChange={(e) => {
                            const next = [...formData.json_assertions];
                            next[idx] = { ...next[idx], value: e.target.value };
                            setFormData({ ...formData, json_assertions: next });
                          }}
                        />
                        <label className="col-span-1 flex items-center justify-center gap-1 text-xs text-slate-400">
                          <input
                            type="checkbox"
                            checked={!!a.case_insensitive}
                            onChange={(e) => {
                              const next = [...formData.json_assertions];
                              next[idx] = { ...next[idx], case_insensitive: e.target.checked };
                              setFormData({ ...formData, json_assertions: next });
                            }}
                          />
                          CI
                        </label>
                        <button
                          type="button"
                          className="btn btn-secondary btn-sm col-span-1"
                          onClick={() => {
                            const next = [...formData.json_assertions];
                            next.splice(idx, 1);
                            setFormData({ ...formData, json_assertions: next });
                          }}
                        >
                          ×
                        </button>
                      </div>
                    );
                  })}
                </div>
                <div className="mt-2">
                  <button
                    type="button"
                    className="btn btn-secondary btn-sm"
                    onClick={() =>
                      setFormData({
                        ...formData,
                        json_assertions: [...(formData.json_assertions || []), { path: '', op: 'exists', value: '', case_insensitive: false }],
                      })
                    }
                  >
                    + Add JSON assertion
                  </button>
                </div>
                <p className="mt-2 text-xs text-slate-500">Paths use the gjson syntax (e.g. <code>data.items.#.id</code>).</p>
              </div>

              <FormInput
                label="Max Latency (ms)"
                type="number"
                value={formData.max_latency_ms}
                onChange={(v) => setFormData({ ...formData, max_latency_ms: v })}
                placeholder="500"
                min={1}
                error={errors.max_latency_ms}
                hint="Fail the check if total latency exceeds this threshold"
              />
            </div>

            <div className="rounded-lg border border-white/[0.06] bg-slate-800/20 p-4 space-y-4">
              <div className="text-xs font-medium text-slate-300">Redirects & TLS (optional)</div>

              <div className="grid grid-cols-2 gap-4">
                <FormToggle
                  label="Follow redirects"
                  description="Stop following if you want to validate 3xx responses"
                  checked={formData.follow_redirects}
                  onChange={(v) => setFormData({ ...formData, follow_redirects: v })}
                />
                <FormInput
                  label="Max Redirects"
                  type="number"
                  value={formData.max_redirects}
                  onChange={(v) => setFormData({ ...formData, max_redirects: parseInt(v) || 0 })}
                  min={0}
                  error={errors.max_redirects}
                />
              </div>

              <FormToggle
                label="Skip TLS verification"
                description="Useful for self-signed certs (not recommended for public endpoints)"
                checked={formData.tls_skip_verify}
                onChange={(v) => setFormData({ ...formData, tls_skip_verify: v })}
              />

              <div className="grid grid-cols-2 gap-4">
                <FormInput
                  label="TLS min days valid"
                  type="number"
                  value={formData.tls_min_days_valid}
                  onChange={(v) => setFormData({ ...formData, tls_min_days_valid: v })}
                  placeholder="14"
                  min={0}
                  error={errors.tls_min_days_valid}
                  hint="Fail if cert expires sooner"
                />
                <FormInput
                  label="TLS server name (SNI)"
                  value={formData.tls_server_name}
                  onChange={(v) => setFormData({ ...formData, tls_server_name: v })}
                  placeholder="api.example.com"
                  hint="Override SNI / hostname verification"
                />
              </div>

              <FormTextarea
                label="Custom CA bundle (PEM)"
                value={formData.tls_ca_pem}
                onChange={(v) => setFormData({ ...formData, tls_ca_pem: v })}
                placeholder="-----BEGIN CERTIFICATE-----\n...\n-----END CERTIFICATE-----"
                hint="Optional: add trusted root CAs for this monitor"
                rows={6}
              />

              <FormToggle
                label="Collect timing breakdown"
                description="Store DNS/connect/TLS/TTFB timings in check results"
                checked={formData.collect_timing}
                onChange={(v) => setFormData({ ...formData, collect_timing: v })}
              />
            </div>
          </div>
        </div>
      )}

      {/* Ping Configuration */}
      {monitorType === 'ping' && (
        <div>
          <SectionHeader title="Ping Configuration" />
          <FormInput
            label="Host"
            value={formData.host}
            onChange={(v) => setFormData({ ...formData, host: v })}
            placeholder="example.com or 8.8.8.8"
            error={errors.host}
          />
        </div>
      )}

      {monitorType === 'dns' && (
        <div>
          <SectionHeader title="DNS Configuration" description="Resolve DNS records and optionally verify answers" />
          <div className="space-y-4">
            <FormInput
              label="Host"
              value={formData.host}
              onChange={(v) => setFormData({ ...formData, host: v })}
              placeholder="example.com"
              error={errors.host}
            />
            <FormSelect
              label="Record Type"
              value={formData.dns_record_type}
              onChange={(v) => setFormData({ ...formData, dns_record_type: v })}
              options={[
                { value: 'A', label: 'A (IPv4)' },
                { value: 'AAAA', label: 'AAAA (IPv6)' },
                { value: 'CNAME', label: 'CNAME' },
                { value: 'TXT', label: 'TXT' },
                { value: 'MX', label: 'MX' },
                { value: 'NS', label: 'NS' },
              ]}
              hint="Defaults to A if not set."
            />
            <FormInput
              label="Expected Answers (optional)"
              value={formData.dns_expected_answers}
              onChange={(v) => setFormData({ ...formData, dns_expected_answers: v })}
              placeholder="1.2.3.4, 5.6.7.8"
              hint="Comma or newline separated. Leave empty to accept any answer."
            />
          </div>
        </div>
      )}

      {monitorType === 'synthetic_api' && (
        <div>
          <SectionHeader title="Synthetic API Configuration" description="Build a multi-step API workflow with request, assertions, and extracts." />
          <div className="space-y-4">
            <div className="rounded-lg border border-cyan-500/20 bg-cyan-500/5 p-4">
              <div className="flex flex-wrap items-center justify-between gap-3">
                <div>
                  <p className="text-sm font-medium text-white">Quick start templates</p>
                  <p className="text-xs text-slate-400">Load a starter workflow, then customize fields for your environment.</p>
                </div>
                <div className="flex flex-wrap gap-2">
                  <button
                    type="button"
                    className="btn btn-secondary btn-sm"
                    onClick={() => applySyntheticAPITemplate('single_health')}
                  >
                    Health Check
                  </button>
                  <button
                    type="button"
                    className="btn btn-secondary btn-sm"
                    onClick={() => applySyntheticAPITemplate('health_auth')}
                  >
                    Health + Auth Flow
                  </button>
                </div>
              </div>
              <div className="mt-3 flex flex-wrap gap-2 text-xs">
                <span className="rounded-full border border-white/[0.1] bg-slate-900/50 px-2 py-1 text-slate-300">
                  {formData.synthetic_api_steps.length} steps
                </span>
                <span className="rounded-full border border-white/[0.1] bg-slate-900/50 px-2 py-1 text-slate-300">
                  {(formData.synthetic_api_variables || []).filter((v) => v.key.trim()).length} variables
                </span>
                <span className="rounded-full border border-white/[0.1] bg-slate-900/50 px-2 py-1 text-slate-300">
                  {formData.synthetic_api_steps.reduce((sum, step) => sum + step.extracts.length, 0)} extracts
                </span>
              </div>
              <div className="mt-3">
                <div className="inline-flex rounded-lg border border-white/[0.1] bg-slate-900/50 p-1">
                  <button
                    type="button"
                    onClick={() => setSyntheticAPIMode('basic')}
                    className={`rounded-md px-3 py-1 text-xs transition-colors ${
                      syntheticAPIMode === 'basic' ? 'bg-white/[0.12] text-white' : 'text-slate-400 hover:text-slate-200'
                    }`}
                  >
                    Basic
                  </button>
                  <button
                    type="button"
                    onClick={() => setSyntheticAPIMode('advanced')}
                    className={`rounded-md px-3 py-1 text-xs transition-colors ${
                      syntheticAPIMode === 'advanced' ? 'bg-white/[0.12] text-white' : 'text-slate-400 hover:text-slate-200'
                    }`}
                  >
                    Advanced
                  </button>
                </div>
                <p className="mt-2 text-xs text-slate-500">
                  Basic keeps the journey builder focused. Advanced unlocks headers, extracts, and assertion tuning.
                </p>
              </div>
            </div>

            <div className="rounded-lg border border-white/[0.08] bg-slate-900/40 p-4">
              <div className="flex flex-wrap items-start justify-between gap-3">
                <div>
                  <p className="text-sm font-medium text-white">Realtime Test</p>
                  <p className="text-xs text-slate-500">Runs the saved monitor immediately and streams the latest result state here.</p>
                </div>
                <button
                  type="button"
                  className="btn btn-secondary btn-sm disabled:opacity-50"
                  onClick={startSyntheticLiveTest}
                  disabled={!isSavedMonitor || isTestRunning}
                >
                  {isTestRunning ? 'Testing...' : 'Test now'}
                </button>
              </div>
              {!isSavedMonitor && (
                <p className="mt-2 text-xs text-amber-400">Save this monitor first to enable realtime tests.</p>
              )}
              {isSavedMonitor && (
                <p className="mt-2 text-xs text-slate-500">Test runs use the currently saved monitor config.</p>
              )}
              {syntheticTestState.phase !== 'idle' && (
                <div className={`mt-3 rounded-lg border px-3 py-2 text-xs ${getSyntheticResultBadgeClasses(syntheticTestState.phase)}`}>
                  <p>{syntheticTestState.message || 'Running...'}</p>
                  {syntheticTestState.result && (
                    <p className="mt-1">
                      Status: <span className="font-medium uppercase">{syntheticTestState.result.status}</span>
                      {typeof syntheticTestState.result.latency_ms === 'number' ? ` · ${syntheticTestState.result.latency_ms}ms` : ''}
                    </p>
                  )}
                </div>
              )}
            </div>

            <div className="grid grid-cols-2 gap-4">
              <FormInput
                label="Base URL (optional)"
                value={formData.synthetic_api_base_url}
                onChange={(v) => setFormData({ ...formData, synthetic_api_base_url: v })}
                placeholder="https://api.example.com"
                error={errors.synthetic_api_base_url}
                hint="Relative step URLs resolve against this base URL."
              />
              <FormSelect
                label="Failure Mode"
                value={formData.synthetic_api_failure_mode}
                onChange={(v) => setFormData({ ...formData, synthetic_api_failure_mode: v as 'fail_fast' | 'continue' })}
                options={[
                  { value: 'fail_fast', label: 'Fail fast' },
                  { value: 'continue', label: 'Continue after failures' },
                ]}
                hint="Fail fast is recommended for linear user journeys."
              />
            </div>

            {syntheticAPIMode === 'advanced' && (
            <div>
              <label className="block text-xs font-medium text-slate-400 mb-1.5">Variables (optional)</label>
              {errors.synthetic_api_variables && <p className="mt-1 text-xs text-rose-400">{errors.synthetic_api_variables}</p>}
              <div className="space-y-2">
                {formData.synthetic_api_variables.map((row: SyntheticVarKV, idx: number) => (
                  <div key={`synthetic-api-var-${idx}`} className="grid grid-cols-12 gap-2">
                    <input
                      className="input col-span-5"
                      placeholder="Variable name"
                      value={row.key}
                      onChange={(e) => {
                        const next = [...formData.synthetic_api_variables];
                        next[idx] = { ...next[idx], key: e.target.value };
                        setFormData({ ...formData, synthetic_api_variables: next });
                      }}
                    />
                    <input
                      className="input col-span-6"
                      placeholder="Variable value"
                      value={row.value}
                      onChange={(e) => {
                        const next = [...formData.synthetic_api_variables];
                        next[idx] = { ...next[idx], value: e.target.value };
                        setFormData({ ...formData, synthetic_api_variables: next });
                      }}
                    />
                    <button
                      type="button"
                      className="btn btn-secondary btn-sm col-span-1"
                      onClick={() => {
                        const next = [...formData.synthetic_api_variables];
                        if (next.length === 1) {
                          next[0] = { key: '', value: '' };
                        } else {
                          next.splice(idx, 1);
                        }
                        setFormData({ ...formData, synthetic_api_variables: next });
                      }}
                    >
                      ×
                    </button>
                  </div>
                ))}
              </div>
              <div className="mt-2">
                <button
                  type="button"
                  className="btn btn-secondary btn-sm"
                  onClick={() =>
                    setFormData({
                      ...formData,
                      synthetic_api_variables: [...formData.synthetic_api_variables, { key: '', value: '' }],
                    })
                  }
                >
                  + Add variable
                </button>
              </div>
              <p className="mt-2 text-xs text-slate-500">Use <code>{'{{variable_name}}'}</code> in URLs, headers, and request bodies.</p>
            </div>
            )}

            <div className="space-y-3">
              <div className="flex items-center justify-between">
                <label className="block text-xs font-medium text-slate-400">Steps</label>
                <button
                  type="button"
                  className="btn btn-secondary btn-sm"
                  onClick={() =>
                    setFormData({
                      ...formData,
                      synthetic_api_steps: [...formData.synthetic_api_steps, defaultSyntheticAPIStep(formData.synthetic_api_steps.length + 1)],
                    })
                  }
                >
                  + Add step
                </button>
              </div>
              {errors.synthetic_api_steps && <p className="text-xs text-rose-400">{errors.synthetic_api_steps}</p>}
              {formData.synthetic_api_steps.map((step: SyntheticAPIStepDraft, stepIdx: number) => (
                <div key={`synthetic-api-step-${stepIdx}`} className="rounded-lg border border-white/[0.08] bg-slate-800/20 p-4 space-y-4">
                  <div className="flex items-center justify-between">
                    <div>
                      <p className="text-xs font-medium text-slate-300">Step {stepIdx + 1}</p>
                      <p className="text-[11px] text-slate-500">{step.method || 'GET'} {step.url || '(missing URL)'}</p>
                    </div>
                    <div className="flex gap-2">
                      <button
                        type="button"
                        className="btn btn-secondary btn-sm"
                        disabled={stepIdx === 0}
                        onClick={() => {
                          if (stepIdx === 0) return;
                          const next = [...formData.synthetic_api_steps];
                          [next[stepIdx - 1], next[stepIdx]] = [next[stepIdx], next[stepIdx - 1]];
                          setFormData({ ...formData, synthetic_api_steps: next });
                        }}
                      >
                        ↑
                      </button>
                      <button
                        type="button"
                        className="btn btn-secondary btn-sm"
                        disabled={stepIdx === formData.synthetic_api_steps.length - 1}
                        onClick={() => {
                          if (stepIdx === formData.synthetic_api_steps.length - 1) return;
                          const next = [...formData.synthetic_api_steps];
                          [next[stepIdx + 1], next[stepIdx]] = [next[stepIdx], next[stepIdx + 1]];
                          setFormData({ ...formData, synthetic_api_steps: next });
                        }}
                      >
                        ↓
                      </button>
                      <button
                        type="button"
                        className="btn btn-secondary btn-sm"
                        onClick={() => {
                          const next = [...formData.synthetic_api_steps];
                          if (next.length === 1) {
                            next[0] = defaultSyntheticAPIStep(1);
                          } else {
                            next.splice(stepIdx, 1);
                          }
                          setFormData({ ...formData, synthetic_api_steps: next });
                        }}
                      >
                        Remove
                      </button>
                    </div>
                  </div>

                  <div className="grid grid-cols-3 gap-3">
                    <FormInput
                      label="Step ID"
                      value={step.id}
                      onChange={(v) => {
                        const next = [...formData.synthetic_api_steps];
                        next[stepIdx] = { ...next[stepIdx], id: v };
                        setFormData({ ...formData, synthetic_api_steps: next });
                      }}
                      placeholder="health"
                    />
                    <FormInput
                      label="Name (optional)"
                      value={step.name}
                      onChange={(v) => {
                        const next = [...formData.synthetic_api_steps];
                        next[stepIdx] = { ...next[stepIdx], name: v };
                        setFormData({ ...formData, synthetic_api_steps: next });
                      }}
                      placeholder="Health check"
                    />
                    <FormSelect
                      label="Method"
                      value={step.method}
                      onChange={(v) => {
                        const next = [...formData.synthetic_api_steps];
                        next[stepIdx] = { ...next[stepIdx], method: v };
                        setFormData({ ...formData, synthetic_api_steps: next });
                      }}
                      options={[
                        { value: 'GET', label: 'GET' },
                        { value: 'HEAD', label: 'HEAD' },
                        { value: 'POST', label: 'POST' },
                        { value: 'PUT', label: 'PUT' },
                        { value: 'DELETE', label: 'DELETE' },
                        { value: 'PATCH', label: 'PATCH' },
                        { value: 'OPTIONS', label: 'OPTIONS' },
                      ]}
                    />
                  </div>

                  <FormInput
                    label="URL"
                    value={step.url}
                    onChange={(v) => {
                      const next = [...formData.synthetic_api_steps];
                      next[stepIdx] = { ...next[stepIdx], url: v };
                      setFormData({ ...formData, synthetic_api_steps: next });
                    }}
                    placeholder="/health or https://service.example.com/health"
                  />

                  {syntheticAPIMode === 'advanced' && (
                  <div>
                    <label className="block text-xs font-medium text-slate-400 mb-1.5">Request Headers (optional)</label>
                    <div className="space-y-2">
                      {step.headers.map((h: HeaderKV, headerIdx: number) => {
                        const sensitive = isSensitiveHeaderName(h.key);
                        const showToggle = sensitive && h.value.trim().length > 0;
                        return (
                          <div key={`synthetic-api-step-${stepIdx}-header-${headerIdx}`} className="grid grid-cols-12 gap-2">
                            <input
                              className="input col-span-4"
                              placeholder="Header name"
                              value={h.key}
                              onChange={(e) => {
                                const next = [...formData.synthetic_api_steps];
                                const headers = [...next[stepIdx].headers];
                                headers[headerIdx] = { ...headers[headerIdx], key: e.target.value };
                                next[stepIdx] = { ...next[stepIdx], headers };
                                setFormData({ ...formData, synthetic_api_steps: next });
                              }}
                            />
                            <input
                              className="input col-span-6"
                              placeholder="Header value"
                              type={sensitive && !h.reveal ? 'password' : 'text'}
                              value={h.value}
                              onChange={(e) => {
                                const next = [...formData.synthetic_api_steps];
                                const headers = [...next[stepIdx].headers];
                                headers[headerIdx] = { ...headers[headerIdx], value: e.target.value };
                                next[stepIdx] = { ...next[stepIdx], headers };
                                setFormData({ ...formData, synthetic_api_steps: next });
                              }}
                            />
                            <button
                              type="button"
                              disabled={!showToggle}
                              onClick={() => {
                                const next = [...formData.synthetic_api_steps];
                                const headers = [...next[stepIdx].headers];
                                headers[headerIdx] = { ...headers[headerIdx], reveal: !headers[headerIdx].reveal };
                                next[stepIdx] = { ...next[stepIdx], headers };
                                setFormData({ ...formData, synthetic_api_steps: next });
                              }}
                              className="btn btn-secondary btn-sm col-span-1 disabled:opacity-50"
                              title={showToggle ? (h.reveal ? 'Hide value' : 'Show value') : 'Add a value to show/hide'}
                            >
                              {h.reveal ? 'Hide' : 'Show'}
                            </button>
                            <button
                              type="button"
                              className="btn btn-secondary btn-sm col-span-1"
                              onClick={() => {
                                const next = [...formData.synthetic_api_steps];
                                const headers = [...next[stepIdx].headers];
                                if (headers.length === 1) {
                                  headers[0] = { key: '', value: '', reveal: false };
                                } else {
                                  headers.splice(headerIdx, 1);
                                }
                                next[stepIdx] = { ...next[stepIdx], headers };
                                setFormData({ ...formData, synthetic_api_steps: next });
                              }}
                            >
                              ×
                            </button>
                          </div>
                        );
                      })}
                    </div>
                    <div className="mt-2">
                      <button
                        type="button"
                        className="btn btn-secondary btn-sm"
                        onClick={() => {
                          const next = [...formData.synthetic_api_steps];
                          next[stepIdx] = { ...next[stepIdx], headers: [...next[stepIdx].headers, { key: '', value: '', reveal: false }] };
                          setFormData({ ...formData, synthetic_api_steps: next });
                        }}
                      >
                        + Add header
                      </button>
                    </div>
                  </div>
                  )}

                  <FormTextarea
                    label="Request Body (optional)"
                    value={step.body}
                    onChange={(v) => {
                      const next = [...formData.synthetic_api_steps];
                      next[stepIdx] = { ...next[stepIdx], body: v };
                      setFormData({ ...formData, synthetic_api_steps: next });
                    }}
                    placeholder='{"email":"{{email}}","password":"{{password}}"}'
                    rows={4}
                  />

                  {syntheticAPIMode === 'advanced' && (
                  <div className="grid grid-cols-3 gap-3">
                    <FormInput
                      label="Step Timeout (seconds, optional)"
                      type="number"
                      value={step.timeout_seconds}
                      onChange={(v) => {
                        const next = [...formData.synthetic_api_steps];
                        next[stepIdx] = { ...next[stepIdx], timeout_seconds: v };
                        setFormData({ ...formData, synthetic_api_steps: next });
                      }}
                      placeholder="20"
                      min={1}
                    />
                    <FormInput
                      label="Max Redirects (optional)"
                      type="number"
                      value={step.max_redirects}
                      onChange={(v) => {
                        const next = [...formData.synthetic_api_steps];
                        next[stepIdx] = { ...next[stepIdx], max_redirects: v };
                        setFormData({ ...formData, synthetic_api_steps: next });
                      }}
                      placeholder="5"
                      min={0}
                    />
                    <FormToggle
                      label="Follow Redirects"
                      checked={step.follow_redirects}
                      onChange={(v) => {
                        const next = [...formData.synthetic_api_steps];
                        next[stepIdx] = { ...next[stepIdx], follow_redirects: v };
                        setFormData({ ...formData, synthetic_api_steps: next });
                      }}
                    />
                  </div>
                  )}

                  {syntheticAPIMode === 'advanced' && (
                  <div className="space-y-2">
                    <div className="flex items-center justify-between">
                      <label className="block text-xs font-medium text-slate-400">Assertions (optional)</label>
                      <button
                        type="button"
                        className="btn btn-secondary btn-sm"
                        onClick={() => {
                          const next = [...formData.synthetic_api_steps];
                          next[stepIdx] = { ...next[stepIdx], assertions: [...next[stepIdx].assertions, defaultSyntheticAPIAssertion()] };
                          setFormData({ ...formData, synthetic_api_steps: next });
                        }}
                      >
                        + Add assertion
                      </button>
                    </div>

                    <div className="space-y-2">
                      {step.assertions.map((assertion: SyntheticAPIAssertionDraft, assertionIdx: number) => {
                        const target = normalizeAssertionTarget(assertion.target);
                        const allowedOps = syntheticAssertionOpsByTarget[target];
                        const op = allowedOps.includes(assertion.op) ? assertion.op : allowedOps[0];
                        const needsPath = assertionPathRequired(target);
                        const needsValue = assertionValueRequired(target, op);

                        return (
                          <div key={`synthetic-api-step-${stepIdx}-assertion-${assertionIdx}`} className="grid grid-cols-12 gap-2">
                            <select
                              className="input col-span-2"
                              value={target}
                              onChange={(e) => {
                                const nextTarget = normalizeAssertionTarget(e.target.value);
                                const nextAssertion = defaultSyntheticAssertionForTarget(nextTarget);
                                const next = [...formData.synthetic_api_steps];
                                const assertions = [...next[stepIdx].assertions];
                                const prev = assertions[assertionIdx];
                                assertions[assertionIdx] = {
                                  ...nextAssertion,
                                  value_input: prev.value_input,
                                };
                                next[stepIdx] = { ...next[stepIdx], assertions };
                                setFormData({ ...formData, synthetic_api_steps: next });
                              }}
                            >
                              <option value="status">status</option>
                              <option value="header">header</option>
                              <option value="body">body</option>
                              <option value="json">json</option>
                            </select>

                            <select
                              className="input col-span-2"
                              value={op}
                              onChange={(e) => {
                                const next = [...formData.synthetic_api_steps];
                                const assertions = [...next[stepIdx].assertions];
                                assertions[assertionIdx] = { ...assertions[assertionIdx], op: e.target.value };
                                next[stepIdx] = { ...next[stepIdx], assertions };
                                setFormData({ ...formData, synthetic_api_steps: next });
                              }}
                            >
                              {allowedOps.map((option) => (
                                <option key={option} value={option}>{option}</option>
                              ))}
                            </select>

                            <input
                              className="input col-span-3"
                              placeholder={target === 'header' ? 'Header name' : target === 'json' ? 'JSON path' : 'Path (unused)'}
                              value={assertion.path}
                              disabled={!needsPath}
                              onChange={(e) => {
                                const next = [...formData.synthetic_api_steps];
                                const assertions = [...next[stepIdx].assertions];
                                assertions[assertionIdx] = { ...assertions[assertionIdx], path: e.target.value };
                                next[stepIdx] = { ...next[stepIdx], assertions };
                                setFormData({ ...formData, synthetic_api_steps: next });
                              }}
                            />

                            <input
                              className="input col-span-4"
                              placeholder={assertionValuePlaceholder(target, op)}
                              value={assertion.value_input}
                              disabled={!needsValue}
                              onChange={(e) => {
                                const next = [...formData.synthetic_api_steps];
                                const assertions = [...next[stepIdx].assertions];
                                assertions[assertionIdx] = { ...assertions[assertionIdx], value_input: e.target.value };
                                next[stepIdx] = { ...next[stepIdx], assertions };
                                setFormData({ ...formData, synthetic_api_steps: next });
                              }}
                            />

                            <button
                              type="button"
                              className="btn btn-secondary btn-sm col-span-1"
                              onClick={() => {
                                const next = [...formData.synthetic_api_steps];
                                const assertions = [...next[stepIdx].assertions];
                                assertions.splice(assertionIdx, 1);
                                next[stepIdx] = { ...next[stepIdx], assertions };
                                setFormData({ ...formData, synthetic_api_steps: next });
                              }}
                            >
                              ×
                            </button>
                          </div>
                        );
                      })}
                    </div>
                    <p className="text-xs text-slate-500">Value accepts plain text or JSON literals (e.g. <code>200</code>, <code>true</code>, <code>[200,201]</code>).</p>
                  </div>
                  )}

                  {syntheticAPIMode === 'advanced' && (
                  <div className="space-y-2">
                    <div className="flex items-center justify-between">
                      <label className="block text-xs font-medium text-slate-400">Extracts (optional)</label>
                      <button
                        type="button"
                        className="btn btn-secondary btn-sm"
                        onClick={() => {
                          const next = [...formData.synthetic_api_steps];
                          next[stepIdx] = { ...next[stepIdx], extracts: [...next[stepIdx].extracts, defaultSyntheticAPIExtract()] };
                          setFormData({ ...formData, synthetic_api_steps: next });
                        }}
                      >
                        + Add extract
                      </button>
                    </div>

                    <div className="space-y-2">
                      {step.extracts.map((extract: SyntheticAPIExtractDraft, extractIdx: number) => (
                        <div key={`synthetic-api-step-${stepIdx}-extract-${extractIdx}`} className="grid grid-cols-12 gap-2">
                          <input
                            className="input col-span-3"
                            placeholder="Variable name"
                            value={extract.name}
                            onChange={(e) => {
                              const next = [...formData.synthetic_api_steps];
                              const extracts = [...next[stepIdx].extracts];
                              extracts[extractIdx] = { ...extracts[extractIdx], name: e.target.value };
                              next[stepIdx] = { ...next[stepIdx], extracts };
                              setFormData({ ...formData, synthetic_api_steps: next });
                            }}
                          />
                          <select
                            className="input col-span-2"
                            value={extract.from}
                            onChange={(e) => {
                              const next = [...formData.synthetic_api_steps];
                              const extracts = [...next[stepIdx].extracts];
                              extracts[extractIdx] = { ...extracts[extractIdx], from: e.target.value as SyntheticAPIExtractConfig['from'] };
                              next[stepIdx] = { ...next[stepIdx], extracts };
                              setFormData({ ...formData, synthetic_api_steps: next });
                            }}
                          >
                            <option value="json">json</option>
                            <option value="header">header</option>
                          </select>
                          <input
                            className="input col-span-5"
                            placeholder={extract.from === 'header' ? 'Header name' : 'JSON path'}
                            value={extract.path}
                            onChange={(e) => {
                              const next = [...formData.synthetic_api_steps];
                              const extracts = [...next[stepIdx].extracts];
                              extracts[extractIdx] = { ...extracts[extractIdx], path: e.target.value };
                              next[stepIdx] = { ...next[stepIdx], extracts };
                              setFormData({ ...formData, synthetic_api_steps: next });
                            }}
                          />
                          <label className="col-span-1 flex items-center justify-center gap-1 text-xs text-slate-400">
                            <input
                              type="checkbox"
                              checked={extract.sensitive}
                              onChange={(e) => {
                                const next = [...formData.synthetic_api_steps];
                                const extracts = [...next[stepIdx].extracts];
                                extracts[extractIdx] = { ...extracts[extractIdx], sensitive: e.target.checked };
                                next[stepIdx] = { ...next[stepIdx], extracts };
                                setFormData({ ...formData, synthetic_api_steps: next });
                              }}
                            />
                            Secret
                          </label>
                          <button
                            type="button"
                            className="btn btn-secondary btn-sm col-span-1"
                            onClick={() => {
                              const next = [...formData.synthetic_api_steps];
                              const extracts = [...next[stepIdx].extracts];
                              extracts.splice(extractIdx, 1);
                              next[stepIdx] = { ...next[stepIdx], extracts };
                              setFormData({ ...formData, synthetic_api_steps: next });
                            }}
                          >
                            ×
                          </button>
                        </div>
                      ))}
                    </div>
                    <p className="text-xs text-slate-500">Extracted values become variables for subsequent steps.</p>
                  </div>
                  )}
                </div>
              ))}
            </div>
          </div>
        </div>
      )}

      {monitorType === 'synthetic_browser' && (
        <div>
          <SectionHeader title="Synthetic Browser Configuration" description="Guided setup for normal users, expert editor for full control." />
          <div className="space-y-4">
            <div className="rounded-lg border border-cyan-500/20 bg-cyan-500/5 p-4">
              <div className="flex flex-wrap items-start justify-between gap-3">
                <div>
                  <p className="text-sm font-medium text-white">Setup Mode</p>
                  <p className="text-xs text-slate-400">
                    Guided mode hides step-level complexity and applies safe defaults.
                  </p>
                </div>
                <div className="inline-flex rounded-lg border border-white/[0.1] bg-slate-900/50 p-1">
                  <button
                    type="button"
                    onClick={() => switchSyntheticBrowserSetupMode('guided')}
                    className={`rounded-md px-3 py-1 text-xs transition-colors ${
                      syntheticBrowserSetupMode === 'guided' ? 'bg-white/[0.12] text-white' : 'text-slate-400 hover:text-slate-200'
                    }`}
                  >
                    Guided
                  </button>
                  <button
                    type="button"
                    onClick={() => switchSyntheticBrowserSetupMode('expert')}
                    className={`rounded-md px-3 py-1 text-xs transition-colors ${
                      syntheticBrowserSetupMode === 'expert' ? 'bg-white/[0.12] text-white' : 'text-slate-400 hover:text-slate-200'
                    }`}
                  >
                    Expert
                  </button>
                </div>
              </div>
              {syntheticBrowserSetupMode === 'guided' ? (
                <p className="mt-2 text-xs text-slate-500">
                  Guided enforces: fail-fast, Desktop Chrome, screenshot-on-failure only.
                </p>
              ) : (
                <p className="mt-2 text-xs text-slate-500">
                  Expert mode exposes full step/action configuration.
                </p>
              )}
            </div>

            {syntheticBrowserSetupMode === 'guided' && (
              <div className="space-y-4">
                <div className="rounded-lg border border-white/[0.08] bg-slate-900/40 p-4">
                  <div className="mb-4 flex items-center gap-2">
                    {[1, 2, 3].map((step) => (
                      <button
                        key={`guided-step-${step}`}
                        type="button"
                        onClick={() => setSyntheticBrowserGuidedStep(step as SyntheticBrowserGuidedStep)}
                        className={`rounded-full px-2.5 py-1 text-xs transition-colors ${
                          syntheticBrowserGuidedStep === step
                            ? 'bg-cyan-500/20 text-cyan-200 border border-cyan-400/40'
                            : 'bg-slate-800/60 text-slate-400 border border-white/[0.08]'
                        }`}
                      >
                        {step === 1 ? '1. Journey' : step === 2 ? '2. Inputs' : '3. Review'}
                      </button>
                    ))}
                  </div>

                  {syntheticBrowserGuidedStep === 1 && (
                    <div className="space-y-3">
                      <p className="text-xs text-slate-400">Pick the journey type you want to monitor.</p>
                      <div className="grid grid-cols-1 gap-3 md:grid-cols-3">
                        <button
                          type="button"
                          className={`rounded-lg border px-3 py-3 text-left transition-colors ${
                            formData.synthetic_browser_guided_template === 'homepage_smoke'
                              ? 'border-cyan-400/50 bg-cyan-500/10'
                              : 'border-white/[0.08] bg-slate-800/40 hover:border-white/[0.16]'
                          }`}
                          onClick={() => applySyntheticBrowserGuidedTemplate('homepage_smoke')}
                        >
                          <p className="text-sm font-medium text-white">Homepage Smoke</p>
                          <p className="mt-1 text-xs text-slate-500">Open page and assert key element is visible.</p>
                        </button>
                        <button
                          type="button"
                          className={`rounded-lg border px-3 py-3 text-left transition-colors ${
                            formData.synthetic_browser_guided_template === 'login_flow'
                              ? 'border-cyan-400/50 bg-cyan-500/10'
                              : 'border-white/[0.08] bg-slate-800/40 hover:border-white/[0.16]'
                          }`}
                          onClick={() => applySyntheticBrowserGuidedTemplate('login_flow')}
                        >
                          <p className="text-sm font-medium text-white">Login Flow</p>
                          <p className="mt-1 text-xs text-slate-500">Open login page, submit form, and validate destination.</p>
                        </button>
                        <button
                          type="button"
                          className="rounded-lg border border-white/[0.08] bg-slate-800/40 px-3 py-3 text-left transition-colors hover:border-white/[0.16]"
                          onClick={() => switchSyntheticBrowserSetupMode('expert')}
                        >
                          <p className="text-sm font-medium text-white">Custom</p>
                          <p className="mt-1 text-xs text-slate-500">Switch to Expert for full step control.</p>
                        </button>
                      </div>
                    </div>
                  )}

                  {syntheticBrowserGuidedStep === 2 && (
                    <div className="space-y-4">
                      <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
                        <FormInput
                          label={formData.synthetic_browser_guided_template === 'login_flow' ? 'Login URL' : 'Start URL'}
                          value={formData.synthetic_browser_start_url}
                          onChange={(v) => setFormData({ ...formData, synthetic_browser_start_url: v })}
                          placeholder={formData.synthetic_browser_guided_template === 'login_flow' ? 'https://app.example.com/login' : 'https://app.example.com'}
                          error={errors.synthetic_browser_start_url}
                        />
                        {formData.synthetic_browser_guided_template === 'homepage_smoke' ? (
                          <FormInput
                            label="Must-see selector"
                            value={formData.synthetic_browser_guided_must_see_selector}
                            onChange={(v) => setFormData({ ...formData, synthetic_browser_guided_must_see_selector: v })}
                            placeholder="main"
                            hint="CSS selector that must be visible."
                          />
                        ) : (
                          <FormInput
                            label="Expected URL contains"
                            value={formData.synthetic_browser_guided_expected_url}
                            onChange={(v) => setFormData({ ...formData, synthetic_browser_guided_expected_url: v })}
                            placeholder="/dashboard"
                          />
                        )}
                      </div>

                      {formData.synthetic_browser_guided_template === 'login_flow' && (
                        <>
                          <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
                            <FormInput
                              label="Email selector"
                              value={formData.synthetic_browser_guided_email_selector}
                              onChange={(v) => setFormData({ ...formData, synthetic_browser_guided_email_selector: v })}
                              placeholder="[name=email]"
                            />
                            <FormInput
                              label="Password selector"
                              value={formData.synthetic_browser_guided_password_selector}
                              onChange={(v) => setFormData({ ...formData, synthetic_browser_guided_password_selector: v })}
                              placeholder="[name=password]"
                            />
                          </div>
                          <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
                            <FormInput
                              label="Submit selector"
                              value={formData.synthetic_browser_guided_submit_selector}
                              onChange={(v) => setFormData({ ...formData, synthetic_browser_guided_submit_selector: v })}
                              placeholder="button[type=submit]"
                            />
                            <FormInput
                              label="Post-login selector"
                              value={formData.synthetic_browser_guided_post_login_selector}
                              onChange={(v) => setFormData({ ...formData, synthetic_browser_guided_post_login_selector: v })}
                              placeholder="[data-test=dashboard]"
                            />
                          </div>
                          <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
                            <FormInput
                              label="Username (optional)"
                              value={formData.synthetic_browser_guided_username}
                              onChange={(v) => setFormData({ ...formData, synthetic_browser_guided_username: v })}
                              placeholder="user@example.com"
                              hint="If empty, fill step for username is skipped."
                            />
                            <FormInput
                              label="Password (optional)"
                              type="password"
                              value={formData.synthetic_browser_guided_password}
                              onChange={(v) => setFormData({ ...formData, synthetic_browser_guided_password: v })}
                              placeholder="********"
                              hint="If empty, fill step for password is skipped."
                            />
                          </div>
                        </>
                      )}

                      {errors.synthetic_browser_steps && (
                        <p className="text-xs text-rose-400">{errors.synthetic_browser_steps}</p>
                      )}
                    </div>
                  )}

                  {syntheticBrowserGuidedStep === 3 && (
                    <div className="space-y-3">
                      <p className="text-xs text-slate-400">Review generated journey before creating the monitor.</p>
                      <div className="flex flex-wrap gap-2 text-xs">
                        <span className="rounded-full border border-white/[0.1] bg-slate-900/60 px-2 py-1 text-slate-300">
                          {syntheticBrowserGuidedPreview.steps.length} steps
                        </span>
                        <span className="rounded-full border border-white/[0.1] bg-slate-900/60 px-2 py-1 text-slate-300">
                          {Object.keys(syntheticBrowserGuidedPreview.variables).length} variables
                        </span>
                        <span className="rounded-full border border-white/[0.1] bg-slate-900/60 px-2 py-1 text-slate-300">
                          Screenshot on failure
                        </span>
                      </div>
                      <div className="space-y-2">
                        {syntheticBrowserGuidedPreview.steps.map((step, idx) => (
                          <div
                            key={`guided-preview-step-${idx}`}
                            className="rounded-lg border border-white/[0.08] bg-slate-800/40 px-3 py-2"
                          >
                            <p className="text-xs font-medium text-slate-300">
                              Step {idx + 1}: {syntheticBrowserActionMeta[step.action].label}
                            </p>
                            <p className="text-[11px] text-slate-500">
                              {step.action === 'goto'
                                ? step.url
                                : step.action === 'assert_url'
                                ? step.value
                                : step.selector}
                            </p>
                          </div>
                        ))}
                      </div>
                      <p className="text-xs text-slate-500">Click Create Monitor at the bottom to save this guided setup.</p>
                    </div>
                  )}

                  <div className="mt-4 flex items-center justify-between border-t border-white/[0.08] pt-3">
                    <button
                      type="button"
                      className="btn btn-secondary btn-sm"
                      onClick={() => setSyntheticBrowserGuidedStep((prev) => (prev > 1 ? ((prev - 1) as SyntheticBrowserGuidedStep) : prev))}
                      disabled={syntheticBrowserGuidedStep === 1}
                    >
                      Back
                    </button>
                    <button
                      type="button"
                      className="btn btn-secondary btn-sm"
                      onClick={() =>
                        setSyntheticBrowserGuidedStep((prev) => (prev < 3 ? ((prev + 1) as SyntheticBrowserGuidedStep) : prev))
                      }
                      disabled={syntheticBrowserGuidedStep === 3}
                    >
                      Next
                    </button>
                  </div>
                </div>
              </div>
            )}

            {syntheticBrowserSetupMode === 'expert' && (
              <>
            <div className="rounded-lg border border-cyan-500/20 bg-cyan-500/5 p-4">
              <div className="flex flex-wrap items-center justify-between gap-3">
                <div>
                  <p className="text-sm font-medium text-white">Quick start templates</p>
                  <p className="text-xs text-slate-400">Start from a realistic browser flow and adjust selectors/URLs.</p>
                </div>
                <div className="flex flex-wrap gap-2">
                  <button
                    type="button"
                    className="btn btn-secondary btn-sm"
                    onClick={() => applySyntheticBrowserTemplate('homepage_smoke')}
                  >
                    Homepage Smoke
                  </button>
                  <button
                    type="button"
                    className="btn btn-secondary btn-sm"
                    onClick={() => applySyntheticBrowserTemplate('login_flow')}
                  >
                    Login Flow
                  </button>
                </div>
              </div>
              <div className="mt-3 flex flex-wrap gap-2 text-xs">
                <span className="rounded-full border border-white/[0.1] bg-slate-900/50 px-2 py-1 text-slate-300">
                  {formData.synthetic_browser_steps.length} steps
                </span>
                <span className="rounded-full border border-white/[0.1] bg-slate-900/50 px-2 py-1 text-slate-300">
                  {(formData.synthetic_browser_variables || []).filter((v) => v.key.trim()).length} variables
                </span>
                <span className="rounded-full border border-white/[0.1] bg-slate-900/50 px-2 py-1 text-slate-300">
                  Artifacts: {formData.synthetic_browser_screenshot_on_failure ? 'Screenshot' : 'No screenshot'}
                </span>
              </div>
              <div className="mt-3">
                <div className="inline-flex rounded-lg border border-white/[0.1] bg-slate-900/50 p-1">
                  <button
                    type="button"
                    onClick={() => setSyntheticBrowserMode('basic')}
                    className={`rounded-md px-3 py-1 text-xs transition-colors ${
                      syntheticBrowserMode === 'basic' ? 'bg-white/[0.12] text-white' : 'text-slate-400 hover:text-slate-200'
                    }`}
                  >
                    Basic
                  </button>
                  <button
                    type="button"
                    onClick={() => setSyntheticBrowserMode('advanced')}
                    className={`rounded-md px-3 py-1 text-xs transition-colors ${
                      syntheticBrowserMode === 'advanced' ? 'bg-white/[0.12] text-white' : 'text-slate-400 hover:text-slate-200'
                    }`}
                  >
                    Advanced
                  </button>
                </div>
                <p className="mt-2 text-xs text-slate-500">
                  Basic is optimized for common user journeys. Advanced unlocks device/variables/step timeout tuning.
                </p>
              </div>
            </div>

            <div className="rounded-lg border border-white/[0.08] bg-slate-900/40 p-4">
              <div className="flex flex-wrap items-start justify-between gap-3">
                <div>
                  <p className="text-sm font-medium text-white">Realtime Test</p>
                  <p className="text-xs text-slate-500">Runs the saved monitor immediately and streams the latest result state here.</p>
                </div>
                <button
                  type="button"
                  className="btn btn-secondary btn-sm disabled:opacity-50"
                  onClick={startSyntheticLiveTest}
                  disabled={!isSavedMonitor || isTestRunning}
                >
                  {isTestRunning ? 'Testing...' : 'Test now'}
                </button>
              </div>
              {!isSavedMonitor && (
                <p className="mt-2 text-xs text-amber-400">Save this monitor first to enable realtime tests.</p>
              )}
              {isSavedMonitor && (
                <p className="mt-2 text-xs text-slate-500">Test runs use the currently saved monitor config.</p>
              )}
              {syntheticTestState.phase !== 'idle' && (
                <div className={`mt-3 rounded-lg border px-3 py-2 text-xs ${getSyntheticResultBadgeClasses(syntheticTestState.phase)}`}>
                  <p>{syntheticTestState.message || 'Running...'}</p>
                  {syntheticTestState.result && (
                    <p className="mt-1">
                      Status: <span className="font-medium uppercase">{syntheticTestState.result.status}</span>
                      {typeof syntheticTestState.result.latency_ms === 'number' ? ` · ${syntheticTestState.result.latency_ms}ms` : ''}
                    </p>
                  )}
                </div>
              )}
            </div>

            <div className="grid grid-cols-2 gap-4">
              <FormInput
                label="Start URL"
                value={formData.synthetic_browser_start_url}
                onChange={(v) => setFormData({ ...formData, synthetic_browser_start_url: v })}
                placeholder="https://app.example.com/login"
                error={errors.synthetic_browser_start_url}
              />
              {syntheticBrowserMode === 'advanced' ? (
                <FormInput
                  label="Device (optional)"
                  value={formData.synthetic_browser_device}
                  onChange={(v) => setFormData({ ...formData, synthetic_browser_device: v })}
                  placeholder="Desktop Chrome"
                />
              ) : (
                <div className="rounded-lg border border-white/[0.08] bg-slate-900/40 px-3 py-3">
                  <p className="text-xs font-medium text-slate-300">Device profile</p>
                  <p className="mt-1 text-xs text-slate-500">Using saved/default device. Switch to Advanced to customize it.</p>
                </div>
              )}
            </div>

            <div className="grid grid-cols-2 gap-4">
              <FormSelect
                label="Failure Mode"
                value={formData.synthetic_browser_failure_mode}
                onChange={(v) => setFormData({ ...formData, synthetic_browser_failure_mode: v as 'fail_fast' | 'continue' })}
                options={[
                  { value: 'fail_fast', label: 'Fail fast' },
                  { value: 'continue', label: 'Continue after failures' },
                ]}
                hint="Fail fast is recommended for user journey checks."
              />
              <div className="space-y-2">
                <label className="block text-xs font-medium text-slate-400 mb-1.5">Artifacts on Failure</label>
                <div className="grid grid-cols-1 gap-2">
                  <FormToggle
                    label="Screenshot"
                    description="Best default for quick triage in UI."
                    checked={formData.synthetic_browser_screenshot_on_failure}
                    onChange={(v) => setFormData({ ...formData, synthetic_browser_screenshot_on_failure: v })}
                  />
                  <FormToggle
                    label="Trace"
                    description="Deep Playwright timeline for debugging."
                    checked={formData.synthetic_browser_trace_on_failure}
                    onChange={(v) => setFormData({ ...formData, synthetic_browser_trace_on_failure: v })}
                  />
                  <FormToggle
                    label="HAR"
                    description="Capture network waterfall and payload metadata."
                    checked={formData.synthetic_browser_har_on_failure}
                    onChange={(v) => setFormData({ ...formData, synthetic_browser_har_on_failure: v })}
                  />
                </div>
              </div>
            </div>

            {syntheticBrowserMode === 'advanced' && (
            <div>
              <label className="block text-xs font-medium text-slate-400 mb-1.5">Variables (optional)</label>
              {errors.synthetic_browser_variables && <p className="mt-1 text-xs text-rose-400">{errors.synthetic_browser_variables}</p>}
              <div className="space-y-2">
                {formData.synthetic_browser_variables.map((row: SyntheticVarKV, idx: number) => (
                  <div key={`synthetic-browser-var-${idx}`} className="grid grid-cols-12 gap-2">
                    <input
                      className="input col-span-5"
                      placeholder="Variable name"
                      value={row.key}
                      onChange={(e) => {
                        const next = [...formData.synthetic_browser_variables];
                        next[idx] = { ...next[idx], key: e.target.value };
                        setFormData({ ...formData, synthetic_browser_variables: next });
                      }}
                    />
                    <input
                      className="input col-span-6"
                      placeholder="Variable value"
                      value={row.value}
                      onChange={(e) => {
                        const next = [...formData.synthetic_browser_variables];
                        next[idx] = { ...next[idx], value: e.target.value };
                        setFormData({ ...formData, synthetic_browser_variables: next });
                      }}
                    />
                    <button
                      type="button"
                      className="btn btn-secondary btn-sm col-span-1"
                      onClick={() => {
                        const next = [...formData.synthetic_browser_variables];
                        if (next.length === 1) {
                          next[0] = { key: '', value: '' };
                        } else {
                          next.splice(idx, 1);
                        }
                        setFormData({ ...formData, synthetic_browser_variables: next });
                      }}
                    >
                      ×
                    </button>
                  </div>
                ))}
              </div>
              <div className="mt-2">
                <button
                  type="button"
                  className="btn btn-secondary btn-sm"
                  onClick={() =>
                    setFormData({
                      ...formData,
                      synthetic_browser_variables: [...formData.synthetic_browser_variables, { key: '', value: '' }],
                    })
                  }
                >
                  + Add variable
                </button>
              </div>
              <p className="mt-2 text-xs text-slate-500">Use <code>{'{{variable_name}}'}</code> inside selectors and values.</p>
            </div>
            )}

            <div className="space-y-3">
              <div className="flex items-center justify-between">
                <label className="block text-xs font-medium text-slate-400">Steps</label>
                <button
                  type="button"
                  className="btn btn-secondary btn-sm"
                  onClick={() =>
                    setFormData({
                      ...formData,
                      synthetic_browser_steps: [...formData.synthetic_browser_steps, defaultSyntheticBrowserStep(formData.synthetic_browser_steps.length + 1)],
                    })
                  }
                >
                  + Add step
                </button>
              </div>
              {errors.synthetic_browser_steps && <p className="text-xs text-rose-400">{errors.synthetic_browser_steps}</p>}
              {formData.synthetic_browser_steps.map((step: SyntheticBrowserStepDraft, stepIdx: number) => {
                const needsURL = step.action === 'goto';
                const needsSelector =
                  step.action === 'click' ||
                  step.action === 'fill' ||
                  step.action === 'wait_for' ||
                  step.action === 'assert_visible' ||
                  step.action === 'assert_text';
                const needsValue = step.action === 'fill' || step.action === 'assert_text' || step.action === 'assert_url';
                const actionMeta = syntheticBrowserActionMeta[step.action];
                const stepSummary =
                  step.action === 'goto'
                    ? step.url || '(missing URL)'
                    : step.action === 'assert_url'
                    ? step.value || '(missing expected URL)'
                    : step.selector || '(missing selector)';

                return (
                  <div key={`synthetic-browser-step-${stepIdx}`} className="rounded-lg border border-white/[0.08] bg-slate-800/20 p-4 space-y-4">
                    <div className="flex items-center justify-between">
                      <div>
                        <p className="text-xs font-medium text-slate-300">Step {stepIdx + 1}</p>
                        <p className="text-[11px] text-slate-500">{actionMeta.label} · {stepSummary}</p>
                      </div>
                      <div className="flex gap-2">
                        <button
                          type="button"
                          className="btn btn-secondary btn-sm"
                          disabled={stepIdx === 0}
                          onClick={() => {
                            if (stepIdx === 0) return;
                            const next = [...formData.synthetic_browser_steps];
                            [next[stepIdx - 1], next[stepIdx]] = [next[stepIdx], next[stepIdx - 1]];
                            setFormData({ ...formData, synthetic_browser_steps: next });
                          }}
                        >
                          ↑
                        </button>
                        <button
                          type="button"
                          className="btn btn-secondary btn-sm"
                          disabled={stepIdx === formData.synthetic_browser_steps.length - 1}
                          onClick={() => {
                            if (stepIdx === formData.synthetic_browser_steps.length - 1) return;
                            const next = [...formData.synthetic_browser_steps];
                            [next[stepIdx + 1], next[stepIdx]] = [next[stepIdx], next[stepIdx + 1]];
                            setFormData({ ...formData, synthetic_browser_steps: next });
                          }}
                        >
                          ↓
                        </button>
                        <button
                          type="button"
                          className="btn btn-secondary btn-sm"
                          onClick={() => {
                            const next = [...formData.synthetic_browser_steps];
                            if (next.length === 1) {
                              next[0] = defaultSyntheticBrowserStep(1);
                            } else {
                              next.splice(stepIdx, 1);
                            }
                            setFormData({ ...formData, synthetic_browser_steps: next });
                          }}
                        >
                          Remove
                        </button>
                      </div>
                    </div>

                    <div className="grid grid-cols-2 gap-3">
                      <FormInput
                        label="Step ID"
                        value={step.id}
                        onChange={(v) => {
                          const next = [...formData.synthetic_browser_steps];
                          next[stepIdx] = { ...next[stepIdx], id: v };
                          setFormData({ ...formData, synthetic_browser_steps: next });
                        }}
                        placeholder="open_login"
                      />
                      <FormSelect
                        label="Action"
                        value={step.action}
                        onChange={(v) => {
                          const next = [...formData.synthetic_browser_steps];
                          next[stepIdx] = { ...next[stepIdx], action: v as SyntheticBrowserAction };
                          setFormData({ ...formData, synthetic_browser_steps: next });
                        }}
                        options={[
                          { value: 'goto', label: syntheticBrowserActionMeta.goto.label },
                          { value: 'click', label: syntheticBrowserActionMeta.click.label },
                          { value: 'fill', label: syntheticBrowserActionMeta.fill.label },
                          { value: 'wait_for', label: syntheticBrowserActionMeta.wait_for.label },
                          { value: 'assert_visible', label: syntheticBrowserActionMeta.assert_visible.label },
                          { value: 'assert_text', label: syntheticBrowserActionMeta.assert_text.label },
                          { value: 'assert_url', label: syntheticBrowserActionMeta.assert_url.label },
                        ]}
                        hint={actionMeta.hint}
                      />
                    </div>

                    {needsURL && (
                      <FormInput
                        label="URL"
                        value={step.url}
                        onChange={(v) => {
                          const next = [...formData.synthetic_browser_steps];
                          next[stepIdx] = { ...next[stepIdx], url: v };
                          setFormData({ ...formData, synthetic_browser_steps: next });
                        }}
                        placeholder="https://app.example.com/login"
                      />
                    )}

                    {needsSelector && (
                      <FormInput
                        label="Selector"
                        value={step.selector}
                        onChange={(v) => {
                          const next = [...formData.synthetic_browser_steps];
                          next[stepIdx] = { ...next[stepIdx], selector: v };
                          setFormData({ ...formData, synthetic_browser_steps: next });
                        }}
                        placeholder={actionMeta.selectorPlaceholder || '[data-test=target]'}
                        hint="Supports any valid CSS selector."
                      />
                    )}

                    {needsValue && (
                      <FormInput
                        label={step.action === 'assert_url' ? 'Expected URL / pattern' : 'Value'}
                        value={step.value}
                        onChange={(v) => {
                          const next = [...formData.synthetic_browser_steps];
                          next[stepIdx] = { ...next[stepIdx], value: v };
                          setFormData({ ...formData, synthetic_browser_steps: next });
                        }}
                        placeholder={step.action === 'assert_url' ? '/dashboard' : actionMeta.valuePlaceholder || 'Text or value'}
                      />
                    )}

                    {syntheticBrowserMode === 'advanced' && (
                      <FormInput
                        label="Step Timeout (seconds, optional)"
                        type="number"
                        value={step.timeout_seconds}
                        onChange={(v) => {
                          const next = [...formData.synthetic_browser_steps];
                          next[stepIdx] = { ...next[stepIdx], timeout_seconds: v };
                          setFormData({ ...formData, synthetic_browser_steps: next });
                        }}
                        placeholder="20"
                        min={1}
                      />
                    )}
                  </div>
                );
              })}
            </div>
              </>
            )}
          </div>
        </div>
      )}

      {/* Schedule */}
      <div>
        <SectionHeader title="Schedule" description="How often to run checks" />
        <div className="grid grid-cols-2 gap-4">
          <FormInput
            label="Interval (seconds)"
            type="number"
            value={formData.interval_seconds}
            onChange={(v) => setFormData({ ...formData, interval_seconds: parseInt(v) || 60 })}
            min={10}
            step={5}
          />
          <FormInput
            label="Timeout (seconds)"
            type="number"
            value={formData.timeout_seconds}
            onChange={(v) => setFormData({ ...formData, timeout_seconds: parseInt(v) || 30 })}
            min={1}
            error={errors.timeout_seconds}
          />
        </div>
      </div>

      {/* Alerting */}
      <div>
        <SectionHeader title="Alerting" />
        <div className="space-y-4">
          <div>
            <label className="mb-2 block text-sm font-medium text-slate-200">Alert Policies</label>
            <div className="space-y-2">
              {alertPolicies.length === 0 ? (
                <div className="text-sm text-slate-500">No alert policies configured.</div>
              ) : (
                alertPolicies.map((policy) => {
                  const checked = formData.alert_policy_ids.includes(policy.id);
                  return (
                    <label key={policy.id} className="flex items-center gap-2 text-sm text-slate-300">
                      <input
                        type="checkbox"
                        checked={checked}
                        onChange={(e) => {
                          const next = e.target.checked
                            ? [...formData.alert_policy_ids, policy.id]
                            : formData.alert_policy_ids.filter((id) => id !== policy.id);
                          setFormData({ ...formData, alert_policy_ids: next });
                        }}
                      />
                      {policy.name}
                    </label>
                  );
                })
              )}
            </div>
            <p className="mt-2 text-xs text-slate-500">Get notified when this monitor fails</p>
          </div>
          <FormInput
            label="Tags"
            value={formData.tags}
            onChange={(v) => setFormData({ ...formData, tags: v })}
            placeholder="production, api, critical"
            hint="Comma-separated tags for filtering"
          />
        </div>
      </div>

      {/* Status */}
      <div>
        <SectionHeader title="Status" />
        <FormToggle
          label="Monitor Enabled"
          description="Run checks on the configured schedule"
          checked={formData.enabled}
          onChange={(v) => setFormData({ ...formData, enabled: v })}
        />
      </div>

      {/* Actions */}
      <div className="flex items-center justify-end gap-3 pt-4 border-t border-white/[0.06]">
        {onCancel && (
          <button
            type="button"
            onClick={onCancel}
            disabled={loading}
            className="btn btn-secondary btn-sm disabled:opacity-50"
          >
            Cancel
          </button>
        )}
        <button
          type="submit"
          disabled={loading}
          className="btn btn-primary btn-sm disabled:opacity-50"
        >
          {loading ? 'Saving...' : monitor ? 'Save Changes' : 'Create Monitor'}
        </button>
      </div>
    </form>
  );
}
