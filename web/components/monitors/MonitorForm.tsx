'use client';

import { ComponentType, ReactNode, useState, useRef } from 'react';
import {
  Monitor,
  CreateMonitorRequest,
  UpdateMonitorRequest,
  MonitorType,
  SyntheticAPIMonitorConfig,
  SyntheticBrowserMonitorConfig,
  Location,
} from '@/lib/types';
import { buildSyntheticBrowserGuidedDraft } from '@/lib/monitor-form/synthetic';
import { DB_TYPE_META, type DatabaseMonitorType } from '@/lib/monitor-form/database';
import {
  buildMonitorFormRequest,
  defaultTimeoutFor,
  hasAdvancedSyntheticAPIConfig,
  hasAdvancedSyntheticBrowserConfig,
  initialMonitorFormState,
  syntheticBrowserGuidedDraftFromForm,
  validateMonitorForm,
} from '@/lib/monitor-form/inline';
import { Globe, Radio, Search, Folder, Server, Webhook, Phone, Network, Code, MousePointer2, Database, Leaf, Zap, MessageSquare, PlugZap, Cylinder, Cable, Flame } from 'lucide-react';
import FormSection from '@/components/ui/FormSection';
import DisplayFieldsEditor from '@/components/monitors/DisplayFieldsEditor';
import FormActions from '@/components/ui/FormActions';
import { AlertingSection } from './AlertingSection';
import GroupForm from './GroupForm';
import AgentForm from './AgentForm';
import PushForm from './PushForm';
import SipForm from './SipForm';
import GrpcForm from './GrpcForm';
import TcpForm from './TcpForm';
import PrometheusForm from './PrometheusForm';
import WebsocketForm from './WebsocketForm';
import DatabaseForm from './DatabaseForm';
import HttpMonitorForm, { MethodUrlRow, sectionsWithErrors } from './HttpMonitorForm';
import DnsEditor from './DnsEditor';
import SyntheticApiEditor from './SyntheticApiEditor';
import SyntheticBrowserEditor, { useSyntheticBrowserSetup } from './SyntheticBrowserEditor';
import { SyntheticLiveTestPanel, useSyntheticEditorMode } from './synthetic-kit';
import { useSyntheticLiveTest } from './useSyntheticLiveTest';
import { MonitorTypeBar, MonitorTypePicker, type MonitorTypeMeta } from './MonitorTypePicker';
import ScheduleSection from './ScheduleSection';
import DependenciesField from './DependenciesField';
import { FormInput, FormPanel, LabelsSection, PlanSummary, PlanTarget, downAfterLabel, everyLabel, revealFirstError, reportConstraintViolation, useClearErrorsOnEdit, useFollowEnabled } from './form-kit';
import { monitorTypeLabel } from '@/lib/monitor-type-labels';

interface MonitorFormProps {
  monitor?: Monitor;
  initialData?: CreateMonitorRequest;
  onSubmit: (data: CreateMonitorRequest | UpdateMonitorRequest) => Promise<void>;
  onCancel?: () => void;
  loading?: boolean;
}

// Monitor type registry. Adding a new type (redis, postgres, websocket, …)
// only requires a new entry here — the picker, categories, search, and
// name placeholder all derive from it. Display names come from
// lib/monitor-type-labels so the picker matches the list and detail pages.
const MONITOR_TYPE_CATEGORIES = ['Web & API', 'Network', 'Databases & brokers', 'Infrastructure & grouping'] as const;

const typeMeta = (
  type: MonitorType,
  rest: Omit<MonitorTypeMeta, 'type' | 'label' | 'category'> & { category: (typeof MONITOR_TYPE_CATEGORIES)[number] }
): MonitorTypeMeta => ({
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

// Types whose whole form (state, fields, submit) is its own component. The
// rest (http, ping, dns, synthetic_api, synthetic_browser) share
// MonitorForm's state and submit below; each draws its fields through an
// editor (HttpMonitorForm, DnsEditor, SyntheticApiEditor,
// SyntheticBrowserEditor). Database types share DatabaseForm, keyed by type.
const TYPE_FORMS: Partial<Record<MonitorType, ComponentType<MonitorFormProps>>> = {
  group: GroupForm,
  agent: AgentForm,
  push: PushForm,
  sip: SipForm,
  grpc: GrpcForm,
  prometheus: PrometheusForm,
  tcp: TcpForm,
  websocket: WebsocketForm,
};

const isDatabaseType = (type: MonitorType): type is DatabaseMonitorType => type in DB_TYPE_META;

// Errors cleared by edits to another field (useClearErrorsOnEdit): the
// timeout is checked against the interval, the URL against the TLS options,
// the TLS expiry check against the URL scheme.
const MONITOR_FORM_RELATED_FIELDS: Record<string, string[]> = {
  timeout_seconds: ['interval_seconds'],
  url: ['tls_skip_verify', 'tls_ca_pem', 'tls_server_name'],
  tls_min_days_valid: ['url'],
};

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

  const [monitorType, setMonitorType] = useState<MonitorType>(sourceType);

  // The inline types' state. It lives here, not in the editors, so a
  // half-filled form survives switching to another type and back.
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

  const [syntheticAPIMode, setSyntheticAPIMode] = useSyntheticEditorMode(
    sourceType === 'synthetic_api' ? (sourceConfig as SyntheticAPIMonitorConfig) : undefined,
    hasAdvancedSyntheticAPIConfig
  );
  const [syntheticBrowserMode, setSyntheticBrowserMode] = useSyntheticEditorMode(
    sourceType === 'synthetic_browser' ? (sourceConfig as SyntheticBrowserMonitorConfig) : undefined,
    hasAdvancedSyntheticBrowserConfig
  );
  const browserSetup = useSyntheticBrowserSetup(monitorType, isEditMode);
  const liveTest = useSyntheticLiveTest({ monitorId: monitor?.id, monitorType, locationIds: formData.location_ids });
  const [availableLocations, setAvailableLocations] = useState<Location[]>([]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (reportConstraintViolation(formRef.current)) return;
    setErrors({});

    const newErrors = validateMonitorForm(monitorType, formData, browserSetup.guided);
    if (Object.keys(newErrors).length > 0) {
      setErrors(newErrors);
      setOpenSections((prev) => ({ ...prev, ...sectionsWithErrors(newErrors) }));
      revealFirstError(formRef.current);
      return;
    }

    await onSubmit(buildMonitorFormRequest(monitorType, formData, browserSetup.guided));
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
      types={MONITOR_TYPE_META}
      categories={MONITOR_TYPE_CATEGORIES}
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
      <MonitorTypeBar meta={getTypeMeta(monitorType)} locked={isEditMode} onChange={() => setTypePickerOpen(true)} />
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

  const typeFormProps: MonitorFormProps = { monitor, initialData, onSubmit, onCancel, loading };
  const TypeForm = TYPE_FORMS[monitorType];
  if (TypeForm) {
    return shell(<TypeForm {...typeFormProps} />);
  }
  if (isDatabaseType(monitorType)) {
    return shell(<DatabaseForm key={monitorType} type={monitorType} {...typeFormProps} />);
  }

  const patchForm = (patch: Partial<typeof formData>) => setFormData({ ...formData, ...patch });

  const liveTestPanel = (
    <SyntheticLiveTestPanel
      test={liveTest}
      saved={Boolean(monitor?.id)}
      locationIds={formData.location_ids}
      locations={availableLocations}
    />
  );

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
        const steps = browserSetup.guided
          ? buildSyntheticBrowserGuidedDraft(syntheticBrowserGuidedDraftFromForm(formData)).steps.length
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
          onChange={(v) => patchForm({ name: v })}
          placeholder={getTypeMeta(monitorType).namePlaceholder}
          error={errors.name}
        />

        {monitorType === 'http' && (
          <MethodUrlRow
            method={formData.method}
            url={formData.url}
            onMethodChange={(v) => patchForm({ method: v })}
            onUrlChange={(v) => patchForm({ url: v })}
            error={errors.url}
          />
        )}

        {(monitorType === 'ping' || monitorType === 'dns') && (
          <FormInput
            label="Host"
            required
            value={formData.host}
            onChange={(v) => patchForm({ host: v })}
            placeholder={monitorType === 'ping' ? 'example.com or 8.8.8.8' : 'example.com'}
            error={errors.host}
            className="input font-mono"
          />
        )}
      </FormSection>

      {monitorType === 'http' && (
        <HttpMonitorForm
          data={formData}
          onChange={patchForm}
          errors={errors}
          openSections={openSections}
          onToggleSection={(id, open) => setOpenSections((prev) => ({ ...prev, [id]: open }))}
        />
      )}

      {monitorType === 'dns' && <DnsEditor data={formData} onChange={patchForm} />}

      {monitorType === 'synthetic_api' && (
        <SyntheticApiEditor
          form={formData}
          setForm={setFormData}
          errors={errors}
          setErrors={setErrors}
          mode={syntheticAPIMode}
          onModeChange={setSyntheticAPIMode}
          liveTest={liveTestPanel}
        />
      )}

      {monitorType === 'synthetic_browser' && (
        <SyntheticBrowserEditor
          form={formData}
          setForm={setFormData}
          errors={errors}
          setErrors={setErrors}
          setup={browserSetup}
          mode={syntheticBrowserMode}
          onModeChange={setSyntheticBrowserMode}
          liveTest={liveTestPanel}
        />
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
          onThresholdChange={(n) => patchForm({ consecutive_failures_threshold: n })}
          mode={formData.notification_mode}
          onModeChange={(m) => patchForm({ notification_mode: m })}
          dependencySuppression={formData.dependency_suppression}
          hasDependencies={formData.depends_on_ids.length > 0}
          onDependencySuppressionChange={(d) => patchForm({ dependency_suppression: d })}
          customChannels={formData.notification_channels}
          onCustomChannelsChange={(next) => patchForm({ notification_channels: next })}
        />
      </FormPanel>

      <LabelsSection tags={formData.tags} onTagsChange={(v) => patchForm({ tags: v })}>
        {monitorType === 'http' && (
          <DisplayFieldsEditor
            value={formData.display_fields}
            onChange={(fields) => patchForm({ display_fields: fields })}
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
