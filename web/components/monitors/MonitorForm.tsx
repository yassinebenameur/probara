'use client';

import { ReactNode, useState, useEffect, useId, useRef } from 'react';
import {
  Monitor,
  CreateMonitorRequest,
  UpdateMonitorRequest,
  CheckResult,
  MonitorType,
  SyntheticAPIExtractConfig,
  SyntheticAPIMonitorConfig,
  SyntheticBrowserAction,
  SyntheticBrowserMonitorConfig,
  Location,
} from '@/lib/types';
import { HeaderKV, isSensitiveHeaderName } from '@/lib/monitor-form/http';
import {
  SyntheticAPIAssertionDraft,
  SyntheticAPIExtractDraft,
  SyntheticAPIStepDraft,
  SyntheticBrowserGuidedStep,
  SyntheticBrowserGuidedTemplate,
  SyntheticBrowserSetupMode,
  SyntheticBrowserStepDraft,
  SyntheticEditorMode,
  SyntheticTestPhase,
  SyntheticTestState,
  SyntheticVarKV,
  assertionPathRequired,
  assertionValuePlaceholder,
  assertionValueRequired,
  buildSyntheticBrowserGuidedDraft,
  defaultSyntheticAPIAssertion,
  defaultSyntheticAPIExtract,
  defaultSyntheticAPIStep,
  defaultSyntheticAssertionForTarget,
  defaultSyntheticBrowserStep,
  normalizeAssertionTarget,
  syntheticAssertionOpsByTarget,
  syntheticBrowserActionMeta,
} from '@/lib/monitor-form/synthetic';
import {
  buildMonitorFormRequest,
  defaultTimeoutFor,
  hasAdvancedSyntheticAPIConfig,
  hasAdvancedSyntheticBrowserConfig,
  initialMonitorFormState,
  syntheticAPITemplateFields,
  syntheticBrowserGuidedDraftFromForm,
  syntheticBrowserGuidedTemplateFields,
  syntheticBrowserGuidedToExpertFields,
  syntheticBrowserTemplateFields,
  validateMonitorForm,
} from '@/lib/monitor-form/inline';
import { Globe, Radio, Search, Folder, Server, Webhook, Phone, Network, Code, MousePointer2, Lock, Database, Leaf, Zap, MessageSquare, PlugZap, Cylinder, Cable, Flame, ChevronUp, ChevronDown, Trash2, X, Plus, Eye, EyeOff, type LucideIcon } from 'lucide-react';
import { getMonitorResults, runMonitorNow } from '@/lib/api';
import FormSection from '@/components/ui/FormSection';
import DisplayFieldsEditor from '@/components/monitors/DisplayFieldsEditor';
import FormActions from '@/components/ui/FormActions';
import Button from '@/components/ui/Button';
import IconButton from '@/components/ui/IconButton';
import Callout, { type CalloutTone } from '@/components/ui/Callout';
import FilterChip from '@/components/ui/FilterChip';
import Tabs from '@/components/ui/Tabs';
import { AlertingSection } from './AlertingSection';
import GroupForm from './GroupForm';
import AgentForm from './AgentForm';
import PushForm from './PushForm';
import SipForm from './SipForm';
import GrpcForm from './GrpcForm';
import TcpForm from './TcpForm';
import PrometheusForm from './PrometheusForm';
import WebsocketForm from './WebsocketForm';
import DatabaseForm, { type DatabaseMonitorType } from './DatabaseForm';
import HttpMonitorForm, { MethodUrlRow, sectionsWithErrors } from './HttpMonitorForm';
import ScheduleSection from './ScheduleSection';
import DependenciesField from './DependenciesField';
import { FormPanel, LabelsSection, PlanSummary, PlanTarget, downAfterLabel, everyLabel, revealFirstError, reportConstraintViolation, useClearErrorsOnEdit, useFollowEnabled } from './form-kit';
import Switch from '@/components/ui/Switch';
import { monitorTypeLabel } from '@/lib/monitor-type-labels';
import { formatLatency } from '@/lib/format';

const FIELD_LABEL = 'block text-ui font-medium text-slate-300';

const EDITOR_MODE_TABS = [
  { id: 'basic', label: 'Basic' },
  { id: 'advanced', label: 'Advanced' },
] as const;

const BROWSER_SETUP_TABS = [
  { id: 'guided', label: 'Guided' },
  { id: 'expert', label: 'Expert' },
] as const;

// Selected is neutral raised glass, never orange (orange is for actions).
const TILE_SELECTED = 'bg-glass-strong shadow-segment';


interface MonitorFormProps {
  monitor?: Monitor;
  initialData?: CreateMonitorRequest;
  onSubmit: (data: CreateMonitorRequest | UpdateMonitorRequest) => Promise<void>;
  onCancel?: () => void;
  loading?: boolean;
}

// Field helpers for the HTTP, ping, DNS and synthetic sections. Each links its
// label to the control and marks it invalid, so a failed submit can find and
// focus the first broken field (see revealFirstError).
function useFieldIds(error?: string, hint?: string) {
  const id = useId();
  const errorId = `${id}-error`;
  const hintId = `${id}-hint`;
  return {
    id,
    errorId,
    hintId,
    control: {
      id,
      'aria-invalid': error ? (true as const) : undefined,
      'aria-describedby': error ? errorId : hint ? hintId : undefined,
    },
  };
}

function FormInput({
  label,
  type = 'text',
  value,
  onChange,
  placeholder,
  error,
  hint,
  required,
  ...props
}: {
  label: string;
  type?: string;
  value: string | number;
  onChange: (value: string) => void;
  placeholder?: string;
  error?: string;
  hint?: string;
  required?: boolean;
  [key: string]: any;
}) {
  const f = useFieldIds(error, hint);
  return (
    <div>
      <label htmlFor={f.id} className={`${FIELD_LABEL} mb-1.5`}>
        {label}
        {required && <span className="ml-1 text-rose-300" aria-hidden="true">*</span>}
      </label>
      <input
        type={type}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        className="input"
        // The asterisk is aria-hidden; this is the required cue for AT.
        aria-required={required || undefined}
        {...f.control}
        {...props}
      />
      {error && <p id={f.errorId} className="mt-1 text-xs text-rose-300">{error}</p>}
      {hint && !error && <p id={f.hintId} className="mt-1 text-xs text-slate-500">{hint}</p>}
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
  const f = useFieldIds(error, hint);
  return (
    <div>
      <label htmlFor={f.id} className={`${FIELD_LABEL} mb-1.5`}>{label}</label>
      <textarea
        rows={rows}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        className="input min-h-[96px]"
        {...f.control}
        {...props}
      />
      {error && <p id={f.errorId} className="mt-1 text-xs text-rose-300">{error}</p>}
      {hint && !error && <p id={f.hintId} className="mt-1 text-xs text-slate-500">{hint}</p>}
    </div>
  );
}

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
  const f = useFieldIds(undefined, hint);
  return (
    <div>
      <label htmlFor={f.id} className={`${FIELD_LABEL} mb-1.5`}>{label}</label>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        disabled={disabled}
        className="input"
        {...f.control}
      >
        {options.map((opt) => (
          <option key={opt.value} value={opt.value}>{opt.label}</option>
        ))}
      </select>
      {hint && <p id={f.hintId} className="mt-1 text-xs text-slate-500">{hint}</p>}
    </div>
  );
}

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
    <div className="flex items-center justify-between gap-4">
      <div className="min-w-0">
        <p className="text-sm font-medium text-slate-200">{label}</p>
        {description && <p className="text-xs text-slate-500">{description}</p>}
      </div>
      <Switch checked={checked} onChange={onChange} label={label} />
    </div>
  );
}

// Monitor type registry. Adding a new type (redis, postgres, websocket, …)
// only requires a new entry here — the picker, categories, search, and
// name placeholder all derive from it. Display names come from
// lib/monitor-type-labels so the picker matches the list and detail pages.
type MonitorTypeMeta = {
  type: MonitorType;
  label: string;
  /** One line at the picker's column width (~30 characters). */
  description: string;
  icon: LucideIcon;
  category: (typeof MONITOR_TYPE_CATEGORIES)[number];
  namePlaceholder: string;
};

const MONITOR_TYPE_CATEGORIES = ['Web & API', 'Network', 'Databases & brokers', 'Infrastructure & grouping'] as const;

const typeMeta = (type: MonitorType, rest: Omit<MonitorTypeMeta, 'type' | 'label'>): MonitorTypeMeta => ({
  type,
  label: monitorTypeLabel(type),
  ...rest,
});

const MONITOR_TYPE_META: MonitorTypeMeta[] = [
  typeMeta('http', { description: 'Check an HTTP endpoint', icon: Globe, category: 'Web & API', namePlaceholder: 'My API health check' }),
  typeMeta('websocket', { description: 'Handshake and reply checks', icon: Cable, category: 'Web & API', namePlaceholder: 'Live updates socket' }),
  typeMeta('synthetic_api', { description: 'Multi-step API journey', icon: Code, category: 'Web & API', namePlaceholder: 'Checkout API journey' }),
  typeMeta('synthetic_browser', { description: 'Real-browser user flow', icon: MousePointer2, category: 'Web & API', namePlaceholder: 'Login flow check' }),
  typeMeta('ping', { description: 'ICMP reachability', icon: Radio, category: 'Network', namePlaceholder: 'Edge gateway ping' }),
  typeMeta('dns', { description: 'Resolve and verify records', icon: Search, category: 'Network', namePlaceholder: 'example.com DNS' }),
  typeMeta('grpc', { description: 'gRPC health checks', icon: Network, category: 'Network', namePlaceholder: 'My gRPC service' }),
  typeMeta('tcp', { description: 'Connect to a host and port', icon: PlugZap, category: 'Network', namePlaceholder: 'Postgres port reachability' }),
  typeMeta('sip', { description: 'OPTIONS, REGISTER, test calls', icon: Phone, category: 'Network', namePlaceholder: 'My SIP server' }),
  typeMeta('postgres', { description: 'Connect, auth and query', icon: Database, category: 'Databases & brokers', namePlaceholder: 'Postgres production' }),
  typeMeta('mysql', { description: 'Connect, auth and query', icon: Cylinder, category: 'Databases & brokers', namePlaceholder: 'MySQL production' }),
  typeMeta('redis', { description: 'Connect and PING latency', icon: Zap, category: 'Databases & brokers', namePlaceholder: 'Redis cache' }),
  typeMeta('mongodb', { description: 'Connect, auth and ping', icon: Leaf, category: 'Databases & brokers', namePlaceholder: 'Mongo cluster' }),
  typeMeta('rabbitmq', { description: 'AMQP connect and auth', icon: MessageSquare, category: 'Databases & brokers', namePlaceholder: 'RabbitMQ production' }),
  typeMeta('prometheus', { description: 'PromQL with thresholds', icon: Flame, category: 'Infrastructure & grouping', namePlaceholder: 'API error rate' }),
  typeMeta('agent', { description: 'Host metrics from a collector', icon: Server, category: 'Infrastructure & grouping', namePlaceholder: 'Production server' }),
  typeMeta('push', { description: 'Heartbeat sent by your service', icon: Webhook, category: 'Infrastructure & grouping', namePlaceholder: 'My service health' }),
  typeMeta('group', { description: 'Roll monitors up into one status', icon: Folder, category: 'Infrastructure & grouping', namePlaceholder: 'Production services' }),
];

const getTypeMeta = (type: MonitorType): MonitorTypeMeta =>
  MONITOR_TYPE_META.find((m) => m.type === type) ?? MONITOR_TYPE_META[0];

// Errors cleared by edits to another field (useClearErrorsOnEdit): the
// timeout is checked against the interval, the URL against the TLS options,
// the TLS expiry check against the URL scheme.
const MONITOR_FORM_RELATED_FIELDS: Record<string, string[]> = {
  timeout_seconds: ['interval_seconds'],
  url: ['tls_skip_verify', 'tls_ca_pem', 'tls_server_name'],
  tls_min_days_valid: ['url'],
};

/**
 * Step one of a fresh create: choose the type. Categories sit side by side as
 * columns, so the grid has no orphan tiles and the whole catalogue fits in one
 * short block. `onCancel` (when a type was already chosen) closes it again.
 */
function MonitorTypePicker({
  value,
  onSelect,
  onCancel,
}: {
  value: MonitorType | null;
  onSelect: (type: MonitorType) => void;
  onCancel?: () => void;
}) {
  const [query, setQuery] = useState('');
  const headingId = useId();
  const searchRef = useRef<HTMLInputElement>(null);
  // Reopened from "Change type" (onCancel set): the button that had focus is
  // gone, so focus moves to the search. A fresh create leaves focus alone.
  const reopened = Boolean(onCancel);
  useEffect(() => {
    if (reopened) searchRef.current?.focus();
  }, [reopened]);
  const q = query.trim().toLowerCase();
  const matches = (m: MonitorTypeMeta) =>
    !q ||
    m.label.toLowerCase().includes(q) ||
    m.type.includes(q) ||
    m.description.toLowerCase().includes(q) ||
    m.category.toLowerCase().includes(q);
  const visible = MONITOR_TYPE_META.filter(matches);

  return (
    <section
      aria-labelledby={headingId}
      className="glass p-5 sm:p-6"
      onKeyDown={(e) => {
        if (e.key === 'Escape' && onCancel) {
          e.preventDefault();
          onCancel();
        }
      }}
    >
      <div className="mb-5 flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 id={headingId} className="text-title font-semibold text-white">Monitor type</h2>
          <p className="mt-0.5 text-ui text-slate-400">Choose what to watch.</p>
        </div>
        <div className="flex w-full items-center gap-2 sm:w-auto">
          <div className="relative min-w-0 flex-1 sm:flex-none">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-slate-500" strokeWidth={1.75} aria-hidden="true" />
            <input
              ref={searchRef}
              type="search"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              onKeyDown={(e) => {
                // Enter picks the only (or first) match: "redis⏎".
                if (e.key === 'Enter' && visible.length > 0) {
                  e.preventDefault();
                  onSelect(visible[0].type);
                }
              }}
              placeholder="Search types…"
              aria-label="Search monitor types"
              className="input input-sm !pl-8 sm:!w-52"
            />
          </div>
          {onCancel && (
            <Button type="button" variant="ghost" size="sm" onClick={onCancel}>
              Cancel
            </Button>
          )}
        </div>
      </div>
      <div className="grid gap-x-5 gap-y-5 sm:grid-cols-2 xl:grid-cols-4">
        {MONITOR_TYPE_CATEGORIES.map((category) => {
          const items = visible.filter((m) => m.category === category);
          if (items.length === 0) return null;
          return (
            <div key={category} role="group" aria-label={category}>
              <p className="mb-2 text-xs font-medium text-slate-500">{category}</p>
              <ul className="grid grid-cols-2 gap-1.5 sm:grid-cols-1">
                {items.map((m) => {
                  const Icon = m.icon;
                  const isSelected = m.type === value;
                  return (
                    <li key={m.type}>
                      <button
                        type="button"
                        onClick={() => onSelect(m.type)}
                        aria-current={isSelected ? 'true' : undefined}
                        title={m.label}
                        className={`glass-well flex w-full items-center gap-3 px-3 py-2.5 text-left transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-500/50 ${
                          isSelected ? TILE_SELECTED : 'hover:bg-well-hover'
                        }`}
                      >
                        <Icon className="h-4 w-4 shrink-0 text-slate-400" strokeWidth={1.75} aria-hidden="true" />
                        <span className="min-w-0">
                          {/* Wraps in the two-column phone grid, where truncation would hide the name. */}
                          <span className="block break-words text-ui font-medium leading-tight text-slate-100 sm:truncate sm:leading-normal">{m.label}</span>
                          <span className="hidden truncate text-xs text-slate-500 sm:block">{m.description}</span>
                        </span>
                      </button>
                    </li>
                  );
                })}
              </ul>
            </div>
          );
        })}
      </div>
      {visible.length === 0 && (
        <p className="py-2 text-center text-ui text-slate-500">No monitor types match &ldquo;{query}&rdquo;.</p>
      )}
    </section>
  );
}

/** The chosen type, collapsed to one line above the form. */
function MonitorTypeBar({
  type,
  locked,
  onChange,
}: {
  type: MonitorType;
  locked: boolean;
  onChange: () => void;
}) {
  const selected = getTypeMeta(type);
  const Icon = selected.icon;
  return (
    <div className="glass flex flex-wrap items-center justify-between gap-3 px-6 py-4">
      <div className="flex min-w-0 items-center gap-3">
        <Icon className="h-4 w-4 shrink-0 text-slate-400" strokeWidth={1.75} aria-hidden="true" />
        <div className="min-w-0">
          <p className="text-sm font-medium text-white">{selected.label} monitor</p>
          <p className="truncate text-xs text-slate-500">{selected.description}</p>
        </div>
      </div>
      {locked ? (
        <span className="flex shrink-0 items-center gap-1.5 text-xs text-slate-500">
          <Lock className="h-3.5 w-3.5" strokeWidth={1.75} aria-hidden="true" />
          Type is fixed after creation
        </span>
      ) : (
        <Button type="button" variant="ghost" size="sm" onClick={onChange}>
          Change type
        </Button>
      )}
    </div>
  );
}

export default function MonitorForm({
  monitor,
  initialData,
  onSubmit,
  onCancel,
  loading = false,
}: MonitorFormProps) {
  const isEditMode = Boolean(monitor);
  const sourceType: MonitorType = monitor?.type || initialData?.type || 'http';
  const sourceConfig = monitor?.config || initialData?.config;

  const initialType: MonitorType = sourceType;
  const [monitorType, setMonitorType] = useState<MonitorType>(initialType);

  const initialSyntheticAPIConfig: SyntheticAPIMonitorConfig | undefined =
    sourceType === 'synthetic_api' ? (sourceConfig as SyntheticAPIMonitorConfig) : undefined;
  const initialSyntheticBrowserConfig: SyntheticBrowserMonitorConfig | undefined =
    sourceType === 'synthetic_browser' ? (sourceConfig as SyntheticBrowserMonitorConfig) : undefined;

  const [formData, setFormData] = useState(() => initialMonitorFormState(monitor, initialData));

  const [errors, setErrors] = useState<Record<string, string>>({});
  useClearErrorsOnEdit(formData, setErrors, MONITOR_FORM_RELATED_FIELDS);
  useFollowEnabled(monitor?.enabled, setFormData);
  const [openSections, setOpenSections] = useState<Record<string, boolean>>({});
  // A fresh create starts at the type step, with no form until a type is
  // picked; edit and clone already have their type.
  const [typeChosen, setTypeChosen] = useState(isEditMode || Boolean(initialData));
  const [typePickerOpen, setTypePickerOpen] = useState(!typeChosen);
  const formRef = useRef<HTMLFormElement>(null);
  const formBodyRef = useRef<HTMLDivElement>(null);
  const typeBarRef = useRef<HTMLDivElement>(null);
  const [syntheticAPIMode, setSyntheticAPIMode] = useState<SyntheticEditorMode>('basic');
  const [syntheticBrowserMode, setSyntheticBrowserMode] = useState<SyntheticEditorMode>('basic');
  const [syntheticBrowserSetupMode, setSyntheticBrowserSetupMode] = useState<SyntheticBrowserSetupMode>(
    isEditMode ? 'expert' : 'guided'
  );
  const [syntheticBrowserGuidedStep, setSyntheticBrowserGuidedStep] = useState<SyntheticBrowserGuidedStep>(1);
  const [syntheticTestState, setSyntheticTestState] = useState<SyntheticTestState>({ phase: 'idle' });
  const testSequenceRef = useRef(0);
  const mountedRef = useRef(true);
  const [availableLocations, setAvailableLocations] = useState<Location[]>([]);
  // '' = fan out to all the monitor's locations (or default fleet when none).
  const [testLocationId, setTestLocationId] = useState('');

  const isSavedMonitor = Boolean(monitor?.id);
  const isSyntheticMonitor = monitorType === 'synthetic_api' || monitorType === 'synthetic_browser';
  const isTestRunning = syntheticTestState.phase === 'queueing' || syntheticTestState.phase === 'polling';
  const isSyntheticBrowserGuidedMode =
    monitorType === 'synthetic_browser' && syntheticBrowserSetupMode === 'guided';

  const syntheticBrowserGuidedPreview = buildSyntheticBrowserGuidedDraft(syntheticBrowserGuidedDraftFromForm(formData));

  const syncGuidedBrowserIntoExpert = () => {
    const next = syntheticBrowserGuidedToExpertFields(formData);
    setFormData((prev) => ({ ...prev, ...next }));
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

  const getSyntheticResultTone = (phase: SyntheticTestPhase): CalloutTone => {
    switch (phase) {
      case 'success':
        return 'success';
      case 'failure':
        return 'danger';
      case 'error':
      case 'timeout':
        return 'warning';
      default:
        return 'info';
    }
  };

  const startSyntheticLiveTest = async () => {
    if (!monitor?.id || !isSyntheticMonitor || isTestRunning) return;

    // Ignore a stale pick if the location was deselected since.
    const chosenLocationId = formData.location_ids.includes(testLocationId) ? testLocationId : '';
    const sequence = ++testSequenceRef.current;
    setSyntheticTestState({
      phase: 'queueing',
      message: 'Queuing on-demand run...',
    });

    try {
      const runResponse = await runMonitorNow(
        monitor.id,
        chosenLocationId ? { location_id: chosenLocationId } : undefined
      );
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
          if (chosenLocationId && result.location_id !== chosenLocationId) return false;
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
    if (initialSyntheticAPIConfig && hasAdvancedSyntheticAPIConfig(initialSyntheticAPIConfig)) {
      setSyntheticAPIMode('advanced');
    }
    if (initialSyntheticBrowserConfig && hasAdvancedSyntheticBrowserConfig(initialSyntheticBrowserConfig)) {
      setSyntheticBrowserMode('advanced');
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
    if (monitorType === 'synthetic_browser' && !isEditMode) {
      setSyntheticBrowserSetupMode('guided');
      setSyntheticBrowserGuidedStep(1);
    }
  }, [monitorType, isEditMode]);

  const applySyntheticAPITemplate = (template: 'health_auth' | 'single_health') => {
    setFormData((prev) => ({ ...prev, ...syntheticAPITemplateFields(template) }));

    setErrors((prev) => ({
      ...prev,
      synthetic_api_steps: '',
      synthetic_api_variables: '',
      synthetic_api_base_url: '',
    }));
  };

  const applySyntheticBrowserTemplate = (template: 'login_flow' | 'homepage_smoke') => {
    setFormData((prev) => ({ ...prev, ...syntheticBrowserTemplateFields(template) }));

    setErrors((prev) => ({
      ...prev,
      synthetic_browser_steps: '',
      synthetic_browser_variables: '',
      synthetic_browser_start_url: '',
    }));
  };

  const applySyntheticBrowserGuidedTemplate = (template: SyntheticBrowserGuidedTemplate) => {
    setFormData((prev) => ({ ...prev, ...syntheticBrowserGuidedTemplateFields(prev, template) }));

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
    if (reportConstraintViolation(formRef.current)) return;
    setErrors({});

    const newErrors = validateMonitorForm(monitorType, formData, isSyntheticBrowserGuidedMode);
    if (Object.keys(newErrors).length > 0) {
      setErrors(newErrors);
      setOpenSections((prev) => ({ ...prev, ...sectionsWithErrors(newErrors) }));
      revealFirstError(formRef.current);
      return;
    }

    await onSubmit(buildMonitorFormRequest(monitorType, formData, isSyntheticBrowserGuidedMode));
  };

  const selectType = (type: MonitorType) => {
    // Keep a timeout the operator never touched in step with the new type.
    if (formData.timeout_seconds === defaultTimeoutFor(monitorType)) {
      setFormData((prev) => ({ ...prev, timeout_seconds: defaultTimeoutFor(type) }));
    }
    setMonitorType(type);
    setTypeChosen(true);
    setTypePickerOpen(false);
    setErrors({});
    // Straight on to naming it.
    requestAnimationFrame(() =>
      formBodyRef.current?.querySelector<HTMLElement>('input:not([type="hidden"]), textarea, select')?.focus()
    );
  };

  const typeHeader = typePickerOpen ? (
    <MonitorTypePicker
      value={typeChosen ? monitorType : null}
      onSelect={selectType}
      onCancel={
        typeChosen
          ? () => {
              setTypePickerOpen(false);
              // Back to the "Change type" button the picker replaced.
              requestAnimationFrame(() => typeBarRef.current?.querySelector<HTMLElement>('button')?.focus());
            }
          : undefined
      }
    />
  ) : (
    <div ref={typeBarRef} className="max-w-3xl">
      <MonitorTypeBar type={monitorType} locked={isEditMode} onChange={() => setTypePickerOpen(true)} />
    </div>
  );

  // The picker spans the page; the form itself is capped to a readable width.
  const shell = (body: ReactNode) => (
    <div className="space-y-5">
      {typeHeader}
      {typeChosen && (
        // Hidden rather than unmounted while the picker is reopened, so a
        // half-filled form survives a look at the other types.
        <div ref={formBodyRef} hidden={typePickerOpen} className="max-w-3xl">
          {body}
        </div>
      )}
    </div>
  );

  // Delegate to specialized forms
  if (monitorType === 'group') {
    return shell(<GroupForm monitor={monitor} initialData={initialData} onSubmit={onSubmit} onCancel={onCancel} loading={loading} />);
  }

  if (monitorType === 'agent') {
    return shell(<AgentForm monitor={monitor} initialData={initialData} onSubmit={onSubmit} onCancel={onCancel} loading={loading} />);
  }

  if (monitorType === 'push') {
    return shell(<PushForm monitor={monitor} initialData={initialData} onSubmit={onSubmit} onCancel={onCancel} loading={loading} />);
  }

  if (monitorType === 'sip') {
    return shell(<SipForm monitor={monitor} initialData={initialData} onSubmit={onSubmit} onCancel={onCancel} loading={loading} />);
  }

  if (monitorType === 'grpc') {
    return shell(<GrpcForm monitor={monitor} initialData={initialData} onSubmit={onSubmit} onCancel={onCancel} loading={loading} />);
  }

  if (monitorType === 'prometheus') {
    return shell(<PrometheusForm monitor={monitor} initialData={initialData} onSubmit={onSubmit} onCancel={onCancel} loading={loading} />);
  }

  if (monitorType === 'tcp') {
    return shell(<TcpForm monitor={monitor} initialData={initialData} onSubmit={onSubmit} onCancel={onCancel} loading={loading} />);
  }

  if (monitorType === 'websocket') {
    return shell(<WebsocketForm monitor={monitor} initialData={initialData} onSubmit={onSubmit} onCancel={onCancel} loading={loading} />);
  }

  if (monitorType === 'redis' || monitorType === 'postgres' || monitorType === 'mongodb' || monitorType === 'rabbitmq' || monitorType === 'mysql') {
    return shell(
      <DatabaseForm
        key={monitorType}
        type={monitorType as DatabaseMonitorType}
        monitor={monitor}
        initialData={initialData}
        onSubmit={onSubmit}
        onCancel={onCancel}
        loading={loading}
      />
    );
  }

  const httpData = {
    url: formData.url,
    method: formData.method,
    status_rules: formData.status_rules,
    request_headers: formData.request_headers,
    body: formData.body,
    body_assertions: formData.body_assertions,
    response_header_assertions: formData.response_header_assertions,
    json_assertions: formData.json_assertions,
    max_latency_ms: formData.max_latency_ms,
    follow_redirects: formData.follow_redirects,
    max_redirects: formData.max_redirects,
    tls_skip_verify: formData.tls_skip_verify,
    tls_min_days_valid: formData.tls_min_days_valid,
    tls_server_name: formData.tls_server_name,
    tls_ca_pem: formData.tls_ca_pem,
    collect_timing: formData.collect_timing,
  };

  // Plain-language summary of what this monitor will do, shown in the sticky
  // action bar so misconfigurations are visible right before submitting.
  const planTarget = ((): ReactNode[] | null => {
    switch (monitorType) {
      case 'http': {
        if (!formData.url.trim()) return null;
        let host = formData.url.trim();
        try {
          const u = new URL(host.startsWith('http') ? host : `https://${host}`);
          host = `${u.hostname}${u.pathname !== '/' ? u.pathname : ''}`;
        } catch {
          /* show as typed */
        }
        return [<PlanTarget key="t">{formData.method} {host}</PlanTarget>, `expects ${formData.status_rules.trim() || '2xx'}`];
      }
      case 'ping':
        return formData.host.trim() ? [<PlanTarget key="t">ping {formData.host.trim()}</PlanTarget>] : null;
      case 'dns':
        return formData.host.trim()
          ? [<PlanTarget key="t">{formData.dns_record_type} {formData.host.trim()}</PlanTarget>]
          : null;
      case 'synthetic_api':
        return [`${formData.synthetic_api_steps.length}-step API journey`];
      case 'synthetic_browser': {
        const steps = isSyntheticBrowserGuidedMode
          ? syntheticBrowserGuidedPreview.steps.length
          : formData.synthetic_browser_steps.length;
        return [`${steps}-step browser journey`];
      }
      default:
        return null;
    }
  })();
  const planParts = planTarget
    ? [everyLabel(formData.interval_seconds), ...planTarget, downAfterLabel(formData.consecutive_failures_threshold)]
    : [];

  return shell(
    <form ref={formRef} onSubmit={handleSubmit} noValidate className="space-y-5">
      <FormSection title="Target">
        <FormInput
          label="Monitor name"
          required
          value={formData.name}
          onChange={(v) => setFormData({ ...formData, name: v })}
          placeholder={getTypeMeta(monitorType).namePlaceholder}
          error={errors.name}
        />

        {monitorType === 'http' && (
          <MethodUrlRow
            method={formData.method}
            url={formData.url}
            onMethodChange={(v) => setFormData({ ...formData, method: v })}
            onUrlChange={(v) => setFormData({ ...formData, url: v })}
            error={errors.url}
          />
        )}

        {(monitorType === 'ping' || monitorType === 'dns') && (
          <FormInput
            label="Host"
            required
            value={formData.host}
            onChange={(v) => setFormData({ ...formData, host: v })}
            placeholder={monitorType === 'ping' ? 'example.com or 8.8.8.8' : 'example.com'}
            error={errors.host}
            className="input font-mono"
          />
        )}
      </FormSection>

      {monitorType === 'http' && (
        <HttpMonitorForm
          data={httpData}
          onChange={(patch) => setFormData({ ...formData, ...patch })}
          errors={errors}
          openSections={openSections}
          onToggleSection={(id, open) => setOpenSections((prev) => ({ ...prev, [id]: open }))}
        />
      )}

      {monitorType === 'dns' && (
        <FormSection title="Check">
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <FormSelect
              label="Record type"
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
              label="Expected answers"
              value={formData.dns_expected_answers}
              onChange={(v) => setFormData({ ...formData, dns_expected_answers: v })}
              placeholder="1.2.3.4, 5.6.7.8"
              hint="Optional. Comma or newline separated; empty accepts any answer."
              className="input font-mono"
            />
          </div>
          <FormInput
            label="Nameserver"
            value={formData.dns_nameserver}
            onChange={(v) => setFormData({ ...formData, dns_nameserver: v })}
            placeholder="10.0.0.2 or resolver.internal:53"
            hint="Optional. Query this DNS server instead of the worker's system resolver, for private zones and VPC resolvers."
            className="input font-mono"
          />
        </FormSection>
      )}

      {monitorType === 'synthetic_api' && (
        <FormSection title="Check">
          <div>
            <div className="flex flex-wrap items-center justify-between gap-3">
              <Tabs tabs={EDITOR_MODE_TABS} active={syntheticAPIMode} onChange={setSyntheticAPIMode} />
              <p className="flex flex-wrap gap-x-3 text-xs tabular-nums text-slate-500">
                <span>{formData.synthetic_api_steps.length} steps</span>
                <span>{(formData.synthetic_api_variables || []).filter((v) => v.key.trim()).length} variables</span>
                <span>{formData.synthetic_api_steps.reduce((sum, step) => sum + step.extracts.length, 0)} extracts</span>
              </p>
            </div>
            <p className="mt-2 text-xs text-slate-500">
              Basic keeps the journey builder focused. Advanced unlocks headers, extracts, and assertion tuning.
            </p>
          </div>

          <div className="glass-well flex flex-wrap items-center justify-between gap-3 p-4">
            <div>
              <p className="text-sm font-medium text-white">Quick start templates</p>
              <p className="text-ui text-slate-400">Load a starter workflow, then customize fields for your environment.</p>
            </div>
            <div className="flex flex-wrap gap-2">
              <Button type="button" variant="ghost" size="xs" onClick={() => applySyntheticAPITemplate('single_health')}>
                Health check
              </Button>
              <Button type="button" variant="ghost" size="xs" onClick={() => applySyntheticAPITemplate('health_auth')}>
                Health + auth flow
              </Button>
            </div>
          </div>

          <div className="glass-well p-4">
            <div className="flex flex-wrap items-start justify-between gap-3">
              <div>
                <p className="text-sm font-medium text-white">Realtime test</p>
                <p className="text-ui text-slate-400">Runs the saved monitor immediately and streams the latest result state here.</p>
              </div>
              <div className="flex flex-wrap items-center gap-2">
                {formData.location_ids.length > 0 && (
                  <select
                    aria-label="Run test from location"
                    value={formData.location_ids.includes(testLocationId) ? testLocationId : ''}
                    onChange={(e) => setTestLocationId(e.target.value)}
                    className="input input-sm !w-auto"
                  >
                    <option value="">Run from: all locations</option>
                    {availableLocations
                      .filter((loc) => formData.location_ids.includes(loc.id))
                      .map((loc) => (
                        <option key={loc.id} value={loc.id}>Run from: {loc.name}</option>
                      ))}
                  </select>
                )}
                <Button
                  type="button"
                  variant="ghost"
                  onClick={startSyntheticLiveTest}
                  disabled={!isSavedMonitor || isTestRunning}
                >
                  {isTestRunning ? 'Testing...' : 'Test now'}
                </Button>
              </div>
            </div>
            {!isSavedMonitor && (
              <p className="mt-2 text-xs text-amber-300">Save this monitor first to enable realtime tests.</p>
            )}
            {isSavedMonitor && (
              <p className="mt-2 text-xs text-slate-500">Test runs use the currently saved monitor config.</p>
            )}
            {syntheticTestState.phase !== 'idle' && (
              <Callout tone={getSyntheticResultTone(syntheticTestState.phase)} className="mt-3">
                <p>{syntheticTestState.message || 'Running...'}</p>
                {syntheticTestState.result && (
                  <p className="mt-1 text-ui">
                    Status: <span className="font-medium text-white">{syntheticTestState.result.status}</span>
                    {typeof syntheticTestState.result.latency_ms === 'number' && (
                      <span className="ml-3 tabular-nums">{formatLatency(syntheticTestState.result.latency_ms)}</span>
                    )}
                  </p>
                )}
              </Callout>
            )}
          </div>

          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <FormInput
              label="Base URL (optional)"
              value={formData.synthetic_api_base_url}
              onChange={(v) => setFormData({ ...formData, synthetic_api_base_url: v })}
              placeholder="https://api.example.com"
              error={errors.synthetic_api_base_url}
              hint="Relative step URLs resolve against this base URL."
            />
            <FormSelect
              label="Failure mode"
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
            <label className={`${FIELD_LABEL} mb-1.5`}>Variables (optional)</label>
            {errors.synthetic_api_variables && <p className="mb-2 text-xs text-rose-300">{errors.synthetic_api_variables}</p>}
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
                  <div className="col-span-1 flex items-center justify-center">
                    <IconButton
                      icon={<X strokeWidth={1.75} />}
                      label="Remove variable"
                      onClick={() => {
                        const next = [...formData.synthetic_api_variables];
                        if (next.length === 1) {
                          next[0] = { key: '', value: '' };
                        } else {
                          next.splice(idx, 1);
                        }
                        setFormData({ ...formData, synthetic_api_variables: next });
                      }}
                    />
                  </div>
                </div>
              ))}
            </div>
            <div className="mt-2">
              <Button
                type="button"
                variant="ghost"
                size="xs"
                icon={<Plus className="h-3.5 w-3.5" strokeWidth={1.75} />}
                onClick={() =>
                  setFormData({
                    ...formData,
                    synthetic_api_variables: [...formData.synthetic_api_variables, { key: '', value: '' }],
                  })
                }
              >
                Add variable
              </Button>
            </div>
            <p className="mt-2 text-xs text-slate-500">Use <code>{'{{variable_name}}'}</code> in URLs, headers, and request bodies.</p>
          </div>
          )}

          <div className="space-y-3">
            <div className="flex items-center justify-between">
              <label className={FIELD_LABEL}>Steps</label>
              <Button
                type="button"
                variant="ghost"
                size="xs"
                icon={<Plus className="h-3.5 w-3.5" strokeWidth={1.75} />}
                onClick={() =>
                  setFormData({
                    ...formData,
                    synthetic_api_steps: [...formData.synthetic_api_steps, defaultSyntheticAPIStep(formData.synthetic_api_steps.length + 1)],
                  })
                }
              >
                Add step
              </Button>
            </div>
            {errors.synthetic_api_steps && <p className="text-xs text-rose-300">{errors.synthetic_api_steps}</p>}
            {formData.synthetic_api_steps.map((step: SyntheticAPIStepDraft, stepIdx: number) => (
              <div key={`synthetic-api-step-${stepIdx}`} className="glass-well space-y-4 p-4">
                <div className="flex items-center justify-between">
                  <div>
                    <p className="text-sm font-medium text-white">Step {stepIdx + 1}</p>
                    <p className="font-mono text-xs text-slate-500">{step.method || 'GET'} {step.url || '(missing URL)'}</p>
                  </div>
                  <div className="flex gap-1">
                    <IconButton
                      icon={<ChevronUp strokeWidth={1.75} />}
                      label="Move step up"
                      disabled={stepIdx === 0}
                      onClick={() => {
                        if (stepIdx === 0) return;
                        const next = [...formData.synthetic_api_steps];
                        [next[stepIdx - 1], next[stepIdx]] = [next[stepIdx], next[stepIdx - 1]];
                        setFormData({ ...formData, synthetic_api_steps: next });
                      }}
                    />
                    <IconButton
                      icon={<ChevronDown strokeWidth={1.75} />}
                      label="Move step down"
                      disabled={stepIdx === formData.synthetic_api_steps.length - 1}
                      onClick={() => {
                        if (stepIdx === formData.synthetic_api_steps.length - 1) return;
                        const next = [...formData.synthetic_api_steps];
                        [next[stepIdx + 1], next[stepIdx]] = [next[stepIdx], next[stepIdx + 1]];
                        setFormData({ ...formData, synthetic_api_steps: next });
                      }}
                    />
                    <IconButton
                      icon={<Trash2 strokeWidth={1.75} />}
                      label="Remove step"
                      danger
                      onClick={() => {
                        const next = [...formData.synthetic_api_steps];
                        if (next.length === 1) {
                          next[0] = defaultSyntheticAPIStep(1);
                        } else {
                          next.splice(stepIdx, 1);
                        }
                        setFormData({ ...formData, synthetic_api_steps: next });
                      }}
                    />
                  </div>
                </div>

                <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
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
                  <label className={`${FIELD_LABEL} mb-1.5`}>Request headers (optional)</label>
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
                          <div className="col-span-1 flex items-center justify-center">
                            <IconButton
                              icon={h.reveal ? <EyeOff strokeWidth={1.75} /> : <Eye strokeWidth={1.75} />}
                              label={showToggle ? (h.reveal ? 'Hide value' : 'Show value') : 'Add a value to show/hide'}
                              disabled={!showToggle}
                              onClick={() => {
                                const next = [...formData.synthetic_api_steps];
                                const headers = [...next[stepIdx].headers];
                                headers[headerIdx] = { ...headers[headerIdx], reveal: !headers[headerIdx].reveal };
                                next[stepIdx] = { ...next[stepIdx], headers };
                                setFormData({ ...formData, synthetic_api_steps: next });
                              }}
                            />
                          </div>
                          <div className="col-span-1 flex items-center justify-center">
                            <IconButton
                              icon={<X strokeWidth={1.75} />}
                              label="Remove header"
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
                            />
                          </div>
                        </div>
                      );
                    })}
                  </div>
                  <div className="mt-2">
                    <Button
                      type="button"
                      variant="ghost"
                      size="xs"
                      icon={<Plus className="h-3.5 w-3.5" strokeWidth={1.75} />}
                      onClick={() => {
                        const next = [...formData.synthetic_api_steps];
                        next[stepIdx] = { ...next[stepIdx], headers: [...next[stepIdx].headers, { key: '', value: '', reveal: false }] };
                        setFormData({ ...formData, synthetic_api_steps: next });
                      }}
                    >
                      Add header
                    </Button>
                  </div>
                </div>
                )}

                <FormTextarea
                  label="Request body (optional)"
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
                <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
                  <FormInput
                    label="Step timeout (seconds, optional)"
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
                    label="Max redirects (optional)"
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
                  <div className="sm:flex sm:flex-col sm:justify-end">
                    <div className="sm:flex sm:h-10 sm:flex-col sm:justify-center">
                      <FormToggle
                        label="Follow redirects"
                        checked={step.follow_redirects}
                        onChange={(v) => {
                          const next = [...formData.synthetic_api_steps];
                          next[stepIdx] = { ...next[stepIdx], follow_redirects: v };
                          setFormData({ ...formData, synthetic_api_steps: next });
                        }}
                      />
                    </div>
                  </div>
                </div>
                )}

                {syntheticAPIMode === 'advanced' && (
                <div className="space-y-2">
                  <div className="flex items-center justify-between">
                    <label className={FIELD_LABEL}>Assertions (optional)</label>
                    <Button
                      type="button"
                      variant="ghost"
                      size="xs"
                      icon={<Plus className="h-3.5 w-3.5" strokeWidth={1.75} />}
                      onClick={() => {
                        const next = [...formData.synthetic_api_steps];
                        next[stepIdx] = { ...next[stepIdx], assertions: [...next[stepIdx].assertions, defaultSyntheticAPIAssertion()] };
                        setFormData({ ...formData, synthetic_api_steps: next });
                      }}
                    >
                      Add assertion
                    </Button>
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

                          <div className="col-span-1 flex items-center justify-center">
                            <IconButton
                              icon={<X strokeWidth={1.75} />}
                              label="Remove assertion"
                              onClick={() => {
                                const next = [...formData.synthetic_api_steps];
                                const assertions = [...next[stepIdx].assertions];
                                assertions.splice(assertionIdx, 1);
                                next[stepIdx] = { ...next[stepIdx], assertions };
                                setFormData({ ...formData, synthetic_api_steps: next });
                              }}
                            />
                          </div>
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
                    <label className={FIELD_LABEL}>Extracts (optional)</label>
                    <Button
                      type="button"
                      variant="ghost"
                      size="xs"
                      icon={<Plus className="h-3.5 w-3.5" strokeWidth={1.75} />}
                      onClick={() => {
                        const next = [...formData.synthetic_api_steps];
                        next[stepIdx] = { ...next[stepIdx], extracts: [...next[stepIdx].extracts, defaultSyntheticAPIExtract()] };
                        setFormData({ ...formData, synthetic_api_steps: next });
                      }}
                    >
                      Add extract
                    </Button>
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
                            className="accent-cyan-500"
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
                        <div className="col-span-1 flex items-center justify-center">
                          <IconButton
                            icon={<X strokeWidth={1.75} />}
                            label="Remove extract"
                            onClick={() => {
                              const next = [...formData.synthetic_api_steps];
                              const extracts = [...next[stepIdx].extracts];
                              extracts.splice(extractIdx, 1);
                              next[stepIdx] = { ...next[stepIdx], extracts };
                              setFormData({ ...formData, synthetic_api_steps: next });
                            }}
                          />
                        </div>
                      </div>
                    ))}
                  </div>
                  <p className="text-xs text-slate-500">Extracted values become variables for subsequent steps.</p>
                </div>
                )}
              </div>
            ))}
          </div>
        </FormSection>
      )}

      {monitorType === 'synthetic_browser' && (
        <FormSection title="Check">
          <div>
            <div className="flex flex-wrap items-start justify-between gap-3">
              <div>
                <p className="text-sm font-medium text-white">Setup mode</p>
                <p className="text-ui text-slate-400">
                  Guided mode hides step-level complexity and applies safe defaults.
                </p>
              </div>
              <Tabs tabs={BROWSER_SETUP_TABS} active={syntheticBrowserSetupMode} onChange={switchSyntheticBrowserSetupMode} />
            </div>
            <p className="mt-2 text-xs text-slate-500">
              {syntheticBrowserSetupMode === 'guided'
                ? 'Guided enforces: fail-fast, Desktop Chrome, screenshot-on-failure only.'
                : 'Expert mode exposes full step/action configuration.'}
            </p>
          </div>

          {syntheticBrowserSetupMode === 'guided' && (
            <div className="space-y-4">
              <div className="flex flex-wrap items-center gap-2">
                {[1, 2, 3].map((step) => (
                  <FilterChip
                    key={`guided-step-${step}`}
                    selected={syntheticBrowserGuidedStep === step}
                    onClick={() => setSyntheticBrowserGuidedStep(step as SyntheticBrowserGuidedStep)}
                  >
                    {step === 1 ? '1. Journey' : step === 2 ? '2. Inputs' : '3. Review'}
                  </FilterChip>
                ))}
              </div>

              {syntheticBrowserGuidedStep === 1 && (
                <div className="space-y-3">
                  <p className="text-ui text-slate-400">Pick the journey type you want to monitor.</p>
                  <div className="grid grid-cols-1 gap-3 md:grid-cols-3">
                    <button
                      type="button"
                      className={`glass-well px-4 py-3 text-left transition-colors ${
                        formData.synthetic_browser_guided_template === 'homepage_smoke'
                          ? TILE_SELECTED
                          : 'hover:bg-well-hover'
                      }`}
                      onClick={() => applySyntheticBrowserGuidedTemplate('homepage_smoke')}
                    >
                      <p className="text-sm font-medium text-white">Homepage smoke</p>
                      <p className="mt-1 text-xs text-slate-500">Open page and assert key element is visible.</p>
                    </button>
                    <button
                      type="button"
                      className={`glass-well px-4 py-3 text-left transition-colors ${
                        formData.synthetic_browser_guided_template === 'login_flow'
                          ? TILE_SELECTED
                          : 'hover:bg-well-hover'
                      }`}
                      onClick={() => applySyntheticBrowserGuidedTemplate('login_flow')}
                    >
                      <p className="text-sm font-medium text-white">Login flow</p>
                      <p className="mt-1 text-xs text-slate-500">Open login page, submit form, and validate destination.</p>
                    </button>
                    <button
                      type="button"
                      className="glass-well px-4 py-3 text-left transition-colors hover:bg-well-hover"
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
                    <p className="text-xs text-rose-300">{errors.synthetic_browser_steps}</p>
                  )}
                </div>
              )}

              {syntheticBrowserGuidedStep === 3 && (
                <div className="space-y-3">
                  <div className="flex flex-wrap items-baseline justify-between gap-2">
                    <p className="text-ui text-slate-400">Review generated journey before creating the monitor.</p>
                    <p className="flex flex-wrap gap-x-3 text-xs tabular-nums text-slate-500">
                      <span>{syntheticBrowserGuidedPreview.steps.length} steps</span>
                      <span>{Object.keys(syntheticBrowserGuidedPreview.variables).length} variables</span>
                      <span>Screenshot on failure</span>
                    </p>
                  </div>
                  <div className="glass-well divide-y divide-divider">
                    {syntheticBrowserGuidedPreview.steps.map((step, idx) => (
                      <div key={`guided-preview-step-${idx}`} className="px-4 py-2.5">
                        <p className="text-ui font-medium text-slate-200">
                          Step {idx + 1}: {syntheticBrowserActionMeta[step.action].label}
                        </p>
                        <p className="break-all font-mono text-xs text-slate-500">
                          {step.action === 'goto'
                            ? step.url
                            : step.action === 'assert_url'
                            ? step.value
                            : step.selector}
                        </p>
                      </div>
                    ))}
                  </div>
                  <p className="text-xs text-slate-500">Click Create monitor at the bottom to save this guided setup.</p>
                </div>
              )}

              <div className="flex items-center justify-between">
                <Button
                  type="button"
                  variant="ghost"
                  onClick={() => setSyntheticBrowserGuidedStep((prev) => (prev > 1 ? ((prev - 1) as SyntheticBrowserGuidedStep) : prev))}
                  disabled={syntheticBrowserGuidedStep === 1}
                >
                  Back
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  onClick={() =>
                    setSyntheticBrowserGuidedStep((prev) => (prev < 3 ? ((prev + 1) as SyntheticBrowserGuidedStep) : prev))
                  }
                  disabled={syntheticBrowserGuidedStep === 3}
                >
                  Next
                </Button>
              </div>
            </div>
          )}

          {syntheticBrowserSetupMode === 'expert' && (
            <>
              <div>
                <div className="flex flex-wrap items-center justify-between gap-3">
                  <Tabs tabs={EDITOR_MODE_TABS} active={syntheticBrowserMode} onChange={setSyntheticBrowserMode} />
                  <p className="flex flex-wrap gap-x-3 text-xs tabular-nums text-slate-500">
                    <span>{formData.synthetic_browser_steps.length} steps</span>
                    <span>{(formData.synthetic_browser_variables || []).filter((v) => v.key.trim()).length} variables</span>
                    <span>{formData.synthetic_browser_screenshot_on_failure ? 'Screenshot on failure' : 'No screenshot'}</span>
                  </p>
                </div>
                <p className="mt-2 text-xs text-slate-500">
                  Basic is optimized for common user journeys. Advanced unlocks device/variables/step timeout tuning.
                </p>
              </div>

              <div className="glass-well flex flex-wrap items-center justify-between gap-3 p-4">
                <div>
                  <p className="text-sm font-medium text-white">Quick start templates</p>
                  <p className="text-ui text-slate-400">Start from a realistic browser flow and adjust selectors/URLs.</p>
                </div>
                <div className="flex flex-wrap gap-2">
                  <Button
                    type="button"
                    variant="ghost"
                    size="xs"
                    onClick={() => applySyntheticBrowserTemplate('homepage_smoke')}
                  >
                    Homepage smoke
                  </Button>
                  <Button
                    type="button"
                    variant="ghost"
                    size="xs"
                    onClick={() => applySyntheticBrowserTemplate('login_flow')}
                  >
                    Login flow
                  </Button>
                </div>
              </div>

              <div className="glass-well p-4">
                <div className="flex flex-wrap items-start justify-between gap-3">
                  <div>
                    <p className="text-sm font-medium text-white">Realtime test</p>
                    <p className="text-ui text-slate-400">Runs the saved monitor immediately and streams the latest result state here.</p>
                  </div>
                  <div className="flex flex-wrap items-center gap-2">
                    {formData.location_ids.length > 0 && (
                      <select
                        aria-label="Run test from location"
                        value={formData.location_ids.includes(testLocationId) ? testLocationId : ''}
                        onChange={(e) => setTestLocationId(e.target.value)}
                        className="input input-sm !w-auto"
                      >
                        <option value="">Run from: all locations</option>
                        {availableLocations
                          .filter((loc) => formData.location_ids.includes(loc.id))
                          .map((loc) => (
                            <option key={loc.id} value={loc.id}>Run from: {loc.name}</option>
                          ))}
                      </select>
                    )}
                    <Button
                      type="button"
                      variant="ghost"
                      onClick={startSyntheticLiveTest}
                      disabled={!isSavedMonitor || isTestRunning}
                    >
                      {isTestRunning ? 'Testing...' : 'Test now'}
                    </Button>
                  </div>
                </div>
                {!isSavedMonitor && (
                  <p className="mt-2 text-xs text-amber-300">Save this monitor first to enable realtime tests.</p>
                )}
                {isSavedMonitor && (
                  <p className="mt-2 text-xs text-slate-500">Test runs use the currently saved monitor config.</p>
                )}
                {syntheticTestState.phase !== 'idle' && (
                  <Callout tone={getSyntheticResultTone(syntheticTestState.phase)} className="mt-3">
                    <p>{syntheticTestState.message || 'Running...'}</p>
                    {syntheticTestState.result && (
                      <p className="mt-1 text-ui">
                        Status: <span className="font-medium text-white">{syntheticTestState.result.status}</span>
                        {typeof syntheticTestState.result.latency_ms === 'number' && (
                      <span className="ml-3 tabular-nums">{formatLatency(syntheticTestState.result.latency_ms)}</span>
                    )}
                      </p>
                    )}
                  </Callout>
                )}
              </div>

              <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
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
                  <div>
                    <p className={`${FIELD_LABEL} mb-1.5`}>Device profile</p>
                    <p className="text-xs text-slate-500">Using saved/default device. Switch to Advanced to customize it.</p>
                  </div>
                )}
              </div>

              <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                <FormSelect
                  label="Failure mode"
                  value={formData.synthetic_browser_failure_mode}
                  onChange={(v) => setFormData({ ...formData, synthetic_browser_failure_mode: v as 'fail_fast' | 'continue' })}
                  options={[
                    { value: 'fail_fast', label: 'Fail fast' },
                    { value: 'continue', label: 'Continue after failures' },
                  ]}
                  hint="Fail fast is recommended for user journey checks."
                />
                <div>
                  <label className={`${FIELD_LABEL} mb-1.5`}>Artifacts on failure</label>
                  <div className="glass-well space-y-3 p-4">
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
                <label className={`${FIELD_LABEL} mb-1.5`}>Variables (optional)</label>
                {errors.synthetic_browser_variables && <p className="mb-2 text-xs text-rose-300">{errors.synthetic_browser_variables}</p>}
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
                      <div className="col-span-1 flex items-center justify-center">
                        <IconButton
                          icon={<X strokeWidth={1.75} />}
                          label="Remove variable"
                          onClick={() => {
                            const next = [...formData.synthetic_browser_variables];
                            if (next.length === 1) {
                              next[0] = { key: '', value: '' };
                            } else {
                              next.splice(idx, 1);
                            }
                            setFormData({ ...formData, synthetic_browser_variables: next });
                          }}
                        />
                      </div>
                    </div>
                  ))}
                </div>
                <div className="mt-2">
                  <Button
                    type="button"
                    variant="ghost"
                    size="xs"
                    icon={<Plus className="h-3.5 w-3.5" strokeWidth={1.75} />}
                    onClick={() =>
                      setFormData({
                        ...formData,
                        synthetic_browser_variables: [...formData.synthetic_browser_variables, { key: '', value: '' }],
                      })
                    }
                  >
                    Add variable
                  </Button>
                </div>
                <p className="mt-2 text-xs text-slate-500">Use <code>{'{{variable_name}}'}</code> inside selectors and values.</p>
              </div>
              )}

              <div className="space-y-3">
                <div className="flex items-center justify-between">
                  <label className={FIELD_LABEL}>Steps</label>
                  <Button
                    type="button"
                    variant="ghost"
                    size="xs"
                    icon={<Plus className="h-3.5 w-3.5" strokeWidth={1.75} />}
                    onClick={() =>
                      setFormData({
                        ...formData,
                        synthetic_browser_steps: [...formData.synthetic_browser_steps, defaultSyntheticBrowserStep(formData.synthetic_browser_steps.length + 1)],
                      })
                    }
                  >
                    Add step
                  </Button>
                </div>
                {errors.synthetic_browser_steps && <p className="text-xs text-rose-300">{errors.synthetic_browser_steps}</p>}
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
                    <div key={`synthetic-browser-step-${stepIdx}`} className="glass-well space-y-4 p-4">
                      <div className="flex items-center justify-between">
                        <div>
                          <p className="text-sm font-medium text-white">Step {stepIdx + 1}</p>
                          <p className="flex flex-wrap gap-x-3 text-xs text-slate-500">
                            <span>{actionMeta.label}</span>
                            {stepSummary && <span className="min-w-0 truncate">{stepSummary}</span>}
                          </p>
                        </div>
                        <div className="flex gap-1">
                          <IconButton
                            icon={<ChevronUp strokeWidth={1.75} />}
                            label="Move step up"
                            disabled={stepIdx === 0}
                            onClick={() => {
                              if (stepIdx === 0) return;
                              const next = [...formData.synthetic_browser_steps];
                              [next[stepIdx - 1], next[stepIdx]] = [next[stepIdx], next[stepIdx - 1]];
                              setFormData({ ...formData, synthetic_browser_steps: next });
                            }}
                          />
                          <IconButton
                            icon={<ChevronDown strokeWidth={1.75} />}
                            label="Move step down"
                            disabled={stepIdx === formData.synthetic_browser_steps.length - 1}
                            onClick={() => {
                              if (stepIdx === formData.synthetic_browser_steps.length - 1) return;
                              const next = [...formData.synthetic_browser_steps];
                              [next[stepIdx + 1], next[stepIdx]] = [next[stepIdx], next[stepIdx + 1]];
                              setFormData({ ...formData, synthetic_browser_steps: next });
                            }}
                          />
                          <IconButton
                            icon={<Trash2 strokeWidth={1.75} />}
                            label="Remove step"
                            danger
                            onClick={() => {
                              const next = [...formData.synthetic_browser_steps];
                              if (next.length === 1) {
                                next[0] = defaultSyntheticBrowserStep(1);
                              } else {
                                next.splice(stepIdx, 1);
                              }
                              setFormData({ ...formData, synthetic_browser_steps: next });
                            }}
                          />
                        </div>
                      </div>

                      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
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
                          label="Step timeout (seconds, optional)"
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
        </FormSection>
      )}

      <ScheduleSection
        intervalSeconds={formData.interval_seconds}
        onIntervalChange={(n) => setFormData((prev) => ({ ...prev, interval_seconds: n }))}
        timeoutSeconds={formData.timeout_seconds}
        onTimeoutChange={(n) => setFormData((prev) => ({ ...prev, timeout_seconds: n }))}
        timeoutError={errors.timeout_seconds}
        locations={{
          selectedIds: formData.location_ids,
          onChange: (ids) => setFormData((prev) => ({ ...prev, location_ids: ids })),
          quorum: formData.location_quorum,
          onQuorumChange: (n) => setFormData((prev) => ({ ...prev, location_quorum: n })),
          onLocationsLoaded: setAvailableLocations,
        }}
        enabled={formData.enabled}
        onEnabledChange={(v) => setFormData((prev) => ({ ...prev, enabled: v }))}
      />

      <FormPanel
        title="Alerting"
        rows={
          <DependenciesField
            selectedIds={formData.depends_on_ids}
            onChange={(ids) => setFormData((prev) => ({ ...prev, depends_on_ids: ids }))}
            excludeId={monitor?.id}
            error={errors.depends_on_ids}
          />
        }
      >
        <AlertingSection
          isGroup={false}
          intervalSeconds={formData.interval_seconds}
          threshold={formData.consecutive_failures_threshold}
          onThresholdChange={(n) => setFormData({ ...formData, consecutive_failures_threshold: n })}
          mode={formData.notification_mode}
          onModeChange={(m) => setFormData({ ...formData, notification_mode: m })}
          dependencySuppression={formData.dependency_suppression}
          hasDependencies={formData.depends_on_ids.length > 0}
          onDependencySuppressionChange={(d) => setFormData({ ...formData, dependency_suppression: d })}
          customChannels={formData.notification_channels}
          onCustomChannelsChange={(next) => setFormData({ ...formData, notification_channels: next })}
        />
      </FormPanel>

      <LabelsSection tags={formData.tags} onTagsChange={(v) => setFormData({ ...formData, tags: v })}>
        {monitorType === 'http' && (
          <DisplayFieldsEditor
            value={formData.display_fields}
            onChange={(fields) => setFormData({ ...formData, display_fields: fields })}
            error={errors.display_fields}
          />
        )}
      </LabelsSection>

      <FormActions
        middle={<PlanSummary parts={planParts} errors={errors} />}
        cancel={onCancel ? { label: 'Cancel', onClick: onCancel, disabled: loading } : undefined}
        submit={{
          label: loading ? 'Saving…' : isEditMode ? 'Save changes' : 'Create monitor',
          loading,
          disabled: loading,
          type: 'submit',
        }}
      />
    </form>
  );
}
