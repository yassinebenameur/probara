'use client';

import { useRef, useState } from 'react';
import { testMonitorConfig, type TestMonitorConfigResponse } from '@/lib/api';
import {
  Monitor,
  Location,
  CreateMonitorRequest,
  UpdateMonitorRequest,
  VaultMonitorConfig,
  DependencySuppression,
  NotificationMode,
  ChannelAssignment,
} from '@/lib/types';
import Button from '@/components/ui/Button';
import FormField from '@/components/ui/FormField';
import FormSection from '@/components/ui/FormSection';
import FormActions from '@/components/ui/FormActions';
import { AlertingSection } from './AlertingSection';
import ScheduleSection from './ScheduleSection';
import DependenciesField from './DependenciesField';
import { FieldHint, FormPanel, LabelsSection, PlanSummary, PlanTarget, ToggleRow, downAfterLabel, everyLabel, revealFirstError, reportConstraintViolation, useClearErrorsOnEdit, useFollowEnabled } from './form-kit';
import { VaultLatestCheck } from './VaultDetails';

const PREVIEW_STATUS_COLORS: Record<string, string> = {
  success: 'text-emerald-300',
  failure: 'text-rose-300',
  error: 'text-amber-300',
};

interface VaultFormProps {
  monitor?: Monitor;
  initialData?: CreateMonitorRequest;
  onSubmit: (data: CreateMonitorRequest | UpdateMonitorRequest) => Promise<void>;
  onCancel?: () => void;
  loading?: boolean;
}

// Optional numeric fields are edited as strings so "empty" stays distinct
// from 0 and is omitted from the config.
const numberOrUndefined = (value: string): number | undefined => {
  const trimmed = value.trim();
  if (!trimmed) return undefined;
  const n = Number(trimmed);
  return Number.isFinite(n) ? n : NaN;
};

export default function VaultForm({ monitor, initialData, onSubmit, onCancel, loading = false }: VaultFormProps) {
  const isEditMode = Boolean(monitor);
  const initialConfig = !isEditMode && initialData?.type === 'vault' ? (initialData.config as VaultMonitorConfig) : undefined;
  const existingConfig = monitor && monitor.type === 'vault' ? (monitor.config as VaultMonitorConfig) : undefined;
  const source = existingConfig ?? initialConfig;
  const minTTL = source?.token_min_ttl_seconds;

  const [formData, setFormData] = useState({
    name: monitor?.name || initialData?.name || '',
    url: source?.url ?? '',
    // Secrets come back as "***"; submitting the placeholder keeps the stored
    // value, and clearing the field submits "" which removes it.
    token: source?.token ?? '',
    namespace: source?.namespace ?? '',
    require_active: source?.require_active ?? false,
    token_min_ttl_hours: minTTL ? String(Math.round((minTTL / 3600) * 100) / 100) : '',
    max_latency_ms: source?.max_latency_ms ? String(source.max_latency_ms) : '',
    tls_skip_verify: source?.tls_skip_verify ?? false,
    tls_server_name: source?.tls_server_name ?? '',
    tls_ca_pem: source?.tls_ca_pem ?? '',
    tls_client_cert_pem: source?.tls_client_cert_pem ?? '',
    tls_client_key_pem: source?.tls_client_key_pem ?? '',
    interval_seconds: monitor?.interval_seconds || initialData?.interval_seconds || 60,
    timeout_seconds: monitor?.timeout_seconds || initialData?.timeout_seconds || 10,
    enabled: monitor?.enabled ?? initialData?.enabled ?? true,
    tags: monitor?.tags?.join(', ') || (initialData?.tags || []).join(', '),
    consecutive_failures_threshold: monitor?.consecutive_failures_threshold ?? initialData?.consecutive_failures_threshold ?? 2,
    notification_mode: (monitor?.notification_mode ?? initialData?.notification_mode ?? 'default') as NotificationMode,
    dependency_suppression: (monitor?.dependency_suppression ?? initialData?.dependency_suppression ?? 'inherit') as DependencySuppression,
    notification_channels: monitor?.notification_channels ?? initialData?.notification_channels ?? [] as ChannelAssignment[],
    location_ids: monitor?.location_ids ?? initialData?.location_ids ?? ([] as string[]),
    location_quorum: monitor?.location_quorum ?? initialData?.location_quorum ?? 1,
    depends_on_ids: monitor?.depends_on_ids ?? initialData?.depends_on_ids ?? ([] as string[]),
  });

  const [errors, setErrors] = useState<Record<string, string>>({});
  useClearErrorsOnEdit(formData, setErrors);
  useFollowEnabled(monitor?.enabled, setFormData);
  const formRef = useRef<HTMLFormElement>(null);
  const [tlsOpen, setTlsOpen] = useState(
    Boolean(source?.tls_skip_verify || source?.tls_server_name || source?.tls_ca_pem || source?.tls_client_cert_pem),
  );

  const [preview, setPreview] = useState<TestMonitorConfigResponse | null>(null);
  const [previewError, setPreviewError] = useState('');
  const [previewSettings, setPreviewSettings] = useState('');
  const [testing, setTesting] = useState(false);
  const [previewLocation, setPreviewLocation] = useState('');
  const [locations, setLocations] = useState<Location[]>([]);

  const isHTTPS = formData.url.trim().toLowerCase().startsWith('https://');

  const buildConfig = (): VaultMonitorConfig => {
    const config: VaultMonitorConfig = { url: formData.url.trim().replace(/\/+$/, '') };
    // Always send the token key so an emptied field clears the stored token
    // (absent would keep it; see the merge contract in CLAUDE.md).
    config.token = formData.token.trim();
    if (formData.namespace.trim()) config.namespace = formData.namespace.trim();
    if (formData.require_active) config.require_active = true;
    const hours = numberOrUndefined(formData.token_min_ttl_hours);
    if (hours !== undefined) config.token_min_ttl_seconds = Math.round(hours * 3600);
    const latency = numberOrUndefined(formData.max_latency_ms);
    if (latency !== undefined) config.max_latency_ms = Math.round(latency);
    if (isHTTPS) {
      if (formData.tls_skip_verify) config.tls_skip_verify = true;
      if (formData.tls_server_name.trim()) config.tls_server_name = formData.tls_server_name.trim();
      if (formData.tls_ca_pem.trim()) config.tls_ca_pem = formData.tls_ca_pem.trim();
      if (formData.tls_client_cert_pem.trim()) {
        config.tls_client_cert_pem = formData.tls_client_cert_pem.trim();
        config.tls_client_key_pem = formData.tls_client_key_pem.trim();
      } else if (existingConfig?.tls_client_key_pem) {
        config.tls_client_key_pem = '';
      }
    } else if (existingConfig?.tls_client_key_pem) {
      config.tls_client_key_pem = '';
    }
    return config;
  };

  const validate = (): Record<string, string> => {
    const next: Record<string, string> = {};
    if (!formData.name.trim()) next.name = 'Name is required';
    const url = formData.url.trim();
    if (!url) next.url = 'Vault address is required';
    else if (!/^https?:\/\/[^/?#]+/i.test(url)) next.url = 'Use an http:// or https:// address';
    else if (/\/v1(\/|$)/.test(url)) next.url = 'Enter the Vault address without /v1';
    const hours = numberOrUndefined(formData.token_min_ttl_hours);
    if (hours !== undefined && (Number.isNaN(hours) || hours < 0)) next.token_min_ttl_hours = 'Enter a positive number of hours';
    else if (hours && !formData.token.trim()) next.token_min_ttl_hours = 'The TTL warning needs a monitor token';
    const latency = numberOrUndefined(formData.max_latency_ms);
    if (latency !== undefined && (Number.isNaN(latency) || latency < 0 || latency > 600000)) next.max_latency_ms = 'Enter 0–600000 ms';
    if (isHTTPS && formData.tls_client_cert_pem.trim() && !formData.tls_client_key_pem.trim()) {
      next.tls_client_key_pem = 'Client key is required alongside a client certificate';
    }
    if (formData.timeout_seconds >= formData.interval_seconds) next.timeout_seconds = 'Timeout must be less than interval';
    return next;
  };

  const settingsKey = JSON.stringify([buildConfig(), formData.timeout_seconds, formData.location_ids, previewLocation]);
  const runTest = async () => {
    const found = validate();
    delete found.name;
    delete found.timeout_seconds;
    if (Object.keys(found).length > 0) {
      setErrors(found);
      return;
    }
    setTesting(true); setPreview(null); setPreviewError(''); setPreviewSettings(settingsKey);
    try {
      setPreview(await testMonitorConfig({
        type: 'vault', config: buildConfig(), timeout_seconds: formData.timeout_seconds,
        monitor_id: monitor?.id,
        location_id: formData.location_ids.includes(previewLocation) ? previewLocation : formData.location_ids[0],
      }));
    } catch (error) {
      setPreviewError(error instanceof Error ? error.message : 'Test failed');
    } finally { setTesting(false); }
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (reportConstraintViolation(formRef.current)) return;
    setErrors({});
    const newErrors = validate();
    if (Object.keys(newErrors).length > 0) {
      setErrors(newErrors);
      if (newErrors.tls_client_key_pem) setTlsOpen(true);
      revealFirstError(formRef.current);
      return;
    }

    const requestData: CreateMonitorRequest | UpdateMonitorRequest = {
      name: formData.name.trim(),
      type: 'vault',
      config: buildConfig(),
      interval_seconds: formData.interval_seconds,
      timeout_seconds: formData.timeout_seconds,
      enabled: formData.enabled,
    };
    requestData.consecutive_failures_threshold = formData.consecutive_failures_threshold;
    requestData.notification_mode = formData.notification_mode;
    requestData.dependency_suppression = formData.dependency_suppression;
    requestData.notification_channels = formData.notification_mode === 'custom' ? formData.notification_channels : [];
    requestData.tags = formData.tags.split(',').map(t => t.trim()).filter(Boolean);
    requestData.depends_on_ids = formData.depends_on_ids;
    requestData.location_ids = formData.location_ids;
    requestData.location_quorum =
      formData.location_ids.length >= 2 ? Math.min(formData.location_quorum, formData.location_ids.length) : 1;

    await onSubmit(requestData);
  };

  const hasToken = Boolean(formData.token.trim());

  return (
    <form ref={formRef} onSubmit={handleSubmit} noValidate className="space-y-5">
      <FormSection title="Target">
        <FormField label="Monitor name" required error={errors.name}>
          <input className="input" value={formData.name} onChange={e => setFormData({ ...formData, name: e.target.value })} placeholder="Vault production" />
        </FormField>
        <FormField label="Vault address" required error={errors.url}>
          {(control) => (
            <>
              <input {...control} className="input font-mono" type="url" value={formData.url} onChange={e => setFormData({ ...formData, url: e.target.value })} placeholder="https://vault.example.com:8200" />
              <FieldHint>The same address clients use (VAULT_ADDR), without /v1. Works with OpenBao too.</FieldHint>
            </>
          )}
        </FormField>
        <FormField label="Monitor token" error={errors.token}>
          {(control) => (
            <>
              <input {...control} className="input font-mono" type="password" autoComplete="new-password" value={formData.token} onChange={e => setFormData({ ...formData, token: e.target.value })} placeholder="hvs.…" />
              <FieldHint>
                Strongly recommended. With a token each check also makes an audited request
                (auth/token/lookup-self, allowed by the default policy), which is the only way to see Vault
                refusing traffic because an audit device cannot write — sys/health stays green when that happens.
                Stored encrypted; leave the masked value to keep it, clear the field to remove it.
              </FieldHint>
            </>
          )}
        </FormField>
        {!hasToken && (
          <FieldHint tone="warning">Without a token only sys/health is checked: seal, initialization and standby state, but not audit-device failures.</FieldHint>
        )}
        <FormField label="Namespace (optional)">
          {(control) => (
            <>
              <input {...control} className="input font-mono" autoComplete="off" value={formData.namespace} onChange={e => setFormData({ ...formData, namespace: e.target.value })} placeholder="admin/team-a" />
              <FieldHint>Vault Enterprise / HCP namespace the token lives in. Sent as X-Vault-Namespace on the token probe only.</FieldHint>
            </>
          )}
        </FormField>
      </FormSection>

      <FormSection title="Check">
        <ToggleRow
          title="Require the active node"
          description="Fail when this address answers as a standby, performance standby or DR secondary. Turn on for a load-balancer address; leave off when monitoring an individual cluster member."
          checked={formData.require_active}
          onChange={(v) => setFormData({ ...formData, require_active: v })}
        />
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <FormField label="Warn before token expiry (hours)" error={errors.token_min_ttl_hours}>
            {(control) => (
              <>
                <input {...control} className="input tabular-nums" type="number" min={0} step="any" disabled={!hasToken} value={formData.token_min_ttl_hours} onChange={e => setFormData({ ...formData, token_min_ttl_hours: e.target.value })} placeholder="e.g. 72" />
                <FieldHint>Down when the monitor token&apos;s remaining TTL drops below this. Non-expiring tokens are never flagged.</FieldHint>
              </>
            )}
          </FormField>
          <FormField label="Max latency (ms)" error={errors.max_latency_ms}>
            {(control) => (
              <>
                <input {...control} className="input tabular-nums" type="number" min={0} max={600000} value={formData.max_latency_ms} onChange={e => setFormData({ ...formData, max_latency_ms: e.target.value })} placeholder="Optional" />
                <FieldHint>Total time for both requests.</FieldHint>
              </>
            )}
          </FormField>
        </div>

        {isHTTPS && (
          <div className="space-y-4">
            <button type="button" className="text-sm font-medium text-slate-300 hover:text-white" onClick={() => setTlsOpen(!tlsOpen)} aria-expanded={tlsOpen}>
              {tlsOpen ? '▾' : '▸'} TLS options
            </button>
            {tlsOpen && (
              <div className="space-y-4">
                <FormField label="CA certificate (PEM)">
                  {(control) => (
                    <>
                      <textarea {...control} className="input font-mono text-xs" rows={4} value={formData.tls_ca_pem} onChange={e => setFormData({ ...formData, tls_ca_pem: e.target.value })} placeholder="-----BEGIN CERTIFICATE-----" />
                      <FieldHint>For a Vault listener signed by a private CA (VAULT_CACERT).</FieldHint>
                    </>
                  )}
                </FormField>
                <FormField label="TLS server name (optional)">
                  <input className="input font-mono" autoComplete="off" value={formData.tls_server_name} onChange={e => setFormData({ ...formData, tls_server_name: e.target.value })} placeholder="vault.example.com" />
                </FormField>
                <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                  <FormField label="Client certificate (PEM)">
                    <textarea className="input font-mono text-xs" rows={3} value={formData.tls_client_cert_pem} onChange={e => setFormData({ ...formData, tls_client_cert_pem: e.target.value })} placeholder="Only for listeners that require client certs" />
                  </FormField>
                  <FormField label="Client key (PEM)" error={errors.tls_client_key_pem}>
                    {(control) => (
                      <>
                        <textarea {...control} className="input font-mono text-xs" rows={3} value={formData.tls_client_key_pem} onChange={e => setFormData({ ...formData, tls_client_key_pem: e.target.value })} placeholder="-----BEGIN PRIVATE KEY-----" />
                        <FieldHint>Stored encrypted; *** keeps the saved key.</FieldHint>
                      </>
                    )}
                  </FormField>
                </div>
                <ToggleRow
                  title="Skip TLS certificate verification"
                  description="Accept self-signed or mismatched certificates (VAULT_SKIP_VERIFY). Prefer a CA certificate."
                  checked={formData.tls_skip_verify}
                  onChange={(v) => setFormData({ ...formData, tls_skip_verify: v })}
                  tone="warning"
                />
              </div>
            )}
          </div>
        )}

        {formData.location_ids.length > 1 && <FormField label="Test from location">
          <select className="input" value={formData.location_ids.includes(previewLocation) ? previewLocation : formData.location_ids[0]} onChange={e => setPreviewLocation(e.target.value)}>
            {formData.location_ids.map(id => <option key={id} value={id}>{locations.find(location => location.id === id)?.name ?? id}</option>)}
          </select>
        </FormField>}
        <div>
          <div className="glass-well flex flex-wrap items-center gap-3 px-4 py-3">
            <Button type="button" variant="ghost" loading={testing} disabled={loading} onClick={runTest}>{testing ? 'Checking…' : 'Test connection'}</Button>
            <p className="text-xs text-slate-500">Run one check with the current settings without saving.</p>
          </div>
          <div aria-live="polite">
            {previewError && <p className="mt-3 text-sm text-rose-300">{previewError}</p>}
            {preview && (
              <div className="glass-well mt-3 space-y-2 p-4 text-sm">
                {previewSettings !== settingsKey && (
                  <p className="text-amber-300">Settings changed since this test. Run it again to check the current settings.</p>
                )}
                <div className="flex items-center justify-between gap-3">
                  <span className="text-slate-500">Result</span>
                  <span className={`font-medium capitalize ${PREVIEW_STATUS_COLORS[preview.status] ?? 'text-slate-300'}`}>
                    {preview.status === 'success' ? 'Up' : preview.status === 'failure' ? 'Down' : preview.status}
                    {preview.latency_ms !== undefined && <span className="ml-2 font-normal normal-case text-slate-400">{preview.latency_ms} ms</span>}
                  </span>
                </div>
                {preview.error_message && <p className="text-amber-300">{preview.error_message}</p>}
                {preview.metrics_data?.vault && <VaultLatestCheck metrics={preview.metrics_data.vault} size="default" />}
              </div>
            )}
          </div>
        </div>
      </FormSection>

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
          onLocationsLoaded: setLocations,
        }}
        enabled={formData.enabled}
        onEnabledChange={(v) => setFormData((prev) => ({ ...prev, enabled: v }))}
        enabledDescription="Run Vault checks on this schedule"
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

      <LabelsSection
        tags={formData.tags}
        onTagsChange={(v) => setFormData({ ...formData, tags: v })}
        placeholder="production, vault, critical"
      />

      <FormActions
        middle={
          <PlanSummary
            errors={errors}
            parts={[
              everyLabel(formData.interval_seconds),
              <PlanTarget key="t">{hasToken ? 'health + audited request' : 'sys/health only'}</PlanTarget>,
              downAfterLabel(formData.consecutive_failures_threshold),
            ]}
          />
        }
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
