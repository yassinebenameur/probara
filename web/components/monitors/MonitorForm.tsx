'use client';

import { useState, type ComponentType } from 'react';
import type {
  CreateMonitorRequest,
  Location,
  Monitor,
  MonitorType,
  SyntheticAPIMonitorConfig,
  SyntheticBrowserMonitorConfig,
  UpdateMonitorRequest,
} from '@/lib/types';
import {
  type InlineMonitorDrafts,
  type InlineMonitorFormState,
  type InlineMonitorType,
  describeCheckPlan,
  initialInlineMonitorDrafts,
  isInlineMonitorType,
  prepareInlineMonitorSubmission,
  sourceConfigFor,
} from '@/lib/monitor-form';
import type { FormErrors } from '@/lib/monitor-form/common';
import { type SharedMonitorFields, initialSharedFields } from '@/lib/monitor-form/shared';
import { hasAdvancedSyntheticAPIConfig } from '@/lib/monitor-form/synthetic-api';
import { hasAdvancedSyntheticBrowserConfig } from '@/lib/monitor-form/synthetic-browser';
import FormSection from '@/components/ui/FormSection';
import FormActions from '@/components/ui/FormActions';
import GroupForm from './GroupForm';
import AgentForm from './AgentForm';
import PushForm from './PushForm';
import SipForm from './SipForm';
import GrpcForm from './GrpcForm';
import TcpForm from './TcpForm';
import PrometheusForm from './PrometheusForm';
import WebsocketForm from './WebsocketForm';
import DatabaseForm, { type DatabaseMonitorType } from './DatabaseForm';
import HttpMonitorForm, { MethodUrlRow } from './HttpMonitorForm';
import CurlPreview from './CurlPreview';
import MonitorTypePicker, { getTypeMeta } from './MonitorTypePicker';
import { FormInput } from './MonitorFormControls';
import { DnsFields, PingFields } from './PingDnsFields';
import SharedMonitorSections from './SharedMonitorSections';
import SyntheticApiEditor from './SyntheticApiEditor';
import SyntheticBrowserEditor from './SyntheticBrowserEditor';

interface MonitorFormProps {
  monitor?: Monitor;
  initialData?: CreateMonitorRequest;
  onSubmit: (data: CreateMonitorRequest | UpdateMonitorRequest) => Promise<void>;
  onCancel?: () => void;
  loading?: boolean;
}

const DATABASE_TYPES: readonly MonitorType[] = ['redis', 'postgres', 'mongodb', 'rabbitmq', 'mysql'];
const isDatabaseMonitorType = (type: MonitorType): type is DatabaseMonitorType => DATABASE_TYPES.includes(type);

/** Types with their own complete form. Keyed exhaustively so a new MonitorType must pick an editor. */
const DELEGATED_FORMS: Record<Exclude<MonitorType, InlineMonitorType | DatabaseMonitorType>, ComponentType<MonitorFormProps>> = {
  group: GroupForm,
  agent: AgentForm,
  push: PushForm,
  sip: SipForm,
  grpc: GrpcForm,
  prometheus: PrometheusForm,
  tcp: TcpForm,
  websocket: WebsocketForm,
};

function DelegatedMonitorForm({ type, ...props }: MonitorFormProps & { type: Exclude<MonitorType, InlineMonitorType> }) {
  // Keyed so switching between database types remounts the form with fresh state.
  if (isDatabaseMonitorType(type)) return <DatabaseForm key={type} type={type} {...props} />;
  const Form = DELEGATED_FORMS[type];
  return <Form {...props} />;
}

/**
 * Create/edit form for every monitor type. HTTP, ping, DNS and synthetic
 * monitors are edited inline here (shared fields + a type editor); every other
 * type delegates to its own form. Validation and payload building live in
 * lib/monitor-form.
 */
export default function MonitorForm({ monitor, initialData, onSubmit, onCancel, loading = false }: MonitorFormProps) {
  const isEditMode = Boolean(monitor);
  const [monitorType, setMonitorType] = useState<MonitorType>(monitor?.type || initialData?.type || 'http');
  // Expanded by default when creating so all types are discoverable up front.
  const [typePickerOpen, setTypePickerOpen] = useState(!isEditMode);
  const [shared, setShared] = useState<SharedMonitorFields>(() => initialSharedFields(monitor, initialData));
  const [drafts, setDrafts] = useState<InlineMonitorDrafts>(() => initialInlineMonitorDrafts(monitor, initialData));
  const [errors, setErrors] = useState<FormErrors>({});
  const [openSections, setOpenSections] = useState<Record<string, boolean>>({});
  const [availableLocations, setAvailableLocations] = useState<Location[]>([]);

  const updateShared = (patch: Partial<SharedMonitorFields>) => setShared((prev) => ({ ...prev, ...patch }));
  const updateDraft = <K extends keyof InlineMonitorDrafts>(key: K, patch: Partial<InlineMonitorDrafts[K]>) =>
    setDrafts((prev) => ({ ...prev, [key]: { ...prev[key], ...patch } }));
  const clearErrors = (keys: string[]) =>
    setErrors((prev) => ({ ...prev, ...Object.fromEntries(keys.map((key) => [key, ''])) }));

  const selectType = (type: MonitorType) => {
    // A new browser monitor always (re)starts in the guided setup.
    if (type !== monitorType && type === 'synthetic_browser' && !isEditMode) {
      updateDraft('syntheticBrowser', { setup_mode: 'guided' });
    }
    setMonitorType(type);
    setTypePickerOpen(false);
  };

  const typePicker = (
    <MonitorTypePicker
      value={monitorType}
      onSelect={selectType}
      open={typePickerOpen}
      onOpenChange={setTypePickerOpen}
      locked={isEditMode}
    />
  );

  if (!isInlineMonitorType(monitorType)) {
    return (
      <div className="space-y-4">
        {typePicker}
        <DelegatedMonitorForm
          type={monitorType}
          monitor={monitor}
          initialData={initialData}
          onSubmit={onSubmit}
          onCancel={onCancel}
          loading={loading}
        />
      </div>
    );
  }

  const state: InlineMonitorFormState = { type: monitorType, shared, ...drafts };
  const liveTest = { monitorId: monitor?.id, locationIds: shared.location_ids, locations: availableLocations };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const submission = prepareInlineMonitorSubmission(state);
    setErrors(submission.ok ? {} : submission.errors);
    if (submission.ok) await onSubmit(submission.request);
  };

  return (
    <form onSubmit={handleSubmit} className="space-y-5">
      {typePicker}

      <div id="monitor-name-field">
        <FormSection title="Basics">
          <FormInput
            label="Monitor Name"
            value={shared.name}
            onChange={(name) => updateShared({ name })}
            placeholder={getTypeMeta(monitorType).namePlaceholder}
            error={errors.name}
            hint="A descriptive name for this monitor"
          />

          {monitorType === 'http' && (
            <>
              <MethodUrlRow
                method={drafts.http.method}
                url={drafts.http.url}
                onMethodChange={(method) => updateDraft('http', { method })}
                onUrlChange={(url) => updateDraft('http', { url })}
                error={errors.url}
              />
              <CurlPreview
                method={drafts.http.method}
                url={drafts.http.url}
                headers={drafts.http.request_headers}
                body={drafts.http.body}
              />
            </>
          )}
        </FormSection>
      </div>

      {monitorType === 'http' && (
        <HttpMonitorForm
          data={drafts.http}
          onChange={(patch) => updateDraft('http', patch)}
          errors={errors}
          openSections={openSections}
          onToggleSection={(id, open) => setOpenSections((prev) => ({ ...prev, [id]: open }))}
        />
      )}

      {monitorType === 'ping' && <PingFields value={drafts.host} onChange={(patch) => updateDraft('host', patch)} errors={errors} />}

      {monitorType === 'dns' && <DnsFields value={drafts.host} onChange={(patch) => updateDraft('host', patch)} errors={errors} />}

      {monitorType === 'synthetic_api' && (
        <SyntheticApiEditor
          value={drafts.syntheticApi}
          onChange={(next) => updateDraft('syntheticApi', next)}
          errors={errors}
          onClearErrors={clearErrors}
          advancedByDefault={hasAdvancedSyntheticAPIConfig(
            sourceConfigFor<SyntheticAPIMonitorConfig>('synthetic_api', monitor, initialData)
          )}
          liveTest={liveTest}
        />
      )}

      {monitorType === 'synthetic_browser' && (
        <SyntheticBrowserEditor
          value={drafts.syntheticBrowser}
          onChange={(next) => updateDraft('syntheticBrowser', next)}
          errors={errors}
          onClearErrors={clearErrors}
          advancedByDefault={hasAdvancedSyntheticBrowserConfig(
            sourceConfigFor<SyntheticBrowserMonitorConfig>('synthetic_browser', monitor, initialData)
          )}
          liveTest={liveTest}
        />
      )}

      <SharedMonitorSections
        value={shared}
        onChange={updateShared}
        errors={errors}
        monitorId={monitor?.id}
        onLocationsLoaded={setAvailableLocations}
      />

      <FormActions
        middle={describeCheckPlan(state)}
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
