'use client';

import { useState } from 'react';
import { testMonitorConfig, type TestMonitorConfigResponse } from '@/lib/api';
import {
  Monitor,
  Location,
  CreateMonitorRequest,
  UpdateMonitorRequest,
  PrometheusMonitorConfig,
  DependencySuppression,
  NotificationMode,
  ChannelAssignment,
} from '@/lib/types';
import Button from '@/components/ui/Button';
import FormField from '@/components/ui/FormField';
import FormSection from '@/components/ui/FormSection';
import FormActions from '@/components/ui/FormActions';
import { AlertingSection } from './AlertingSection';
import { LocationsSection } from './LocationsSection';

const PREVIEW_STATUS_COLORS: Record<string, string> = {
  success: 'text-emerald-400',
  failure: 'text-red-400',
  error: 'text-amber-400',
};

interface PrometheusFormProps {
  monitor?: Monitor;
  initialData?: CreateMonitorRequest;
  onSubmit: (data: CreateMonitorRequest | UpdateMonitorRequest) => Promise<void>;
  onCancel?: () => void;
  loading?: boolean;
}

export default function PrometheusForm({
  monitor,
  initialData,
  onSubmit,
  onCancel,
  loading = false,
}: PrometheusFormProps) {
  const isEditMode = Boolean(monitor);
  const initialConfig =
    !isEditMode && initialData?.type === 'prometheus' ? (initialData.config as PrometheusMonitorConfig) : undefined;
  const existingConfig = monitor && monitor.type === 'prometheus' ? (monitor.config as PrometheusMonitorConfig) : undefined;

  const [formData, setFormData] = useState({
    name: monitor?.name || initialData?.name || '',
    url: existingConfig?.url ?? initialConfig?.url ?? '',
    query: existingConfig?.query ?? initialConfig?.query ?? '',
    operator: existingConfig?.operator ?? initialConfig?.operator ?? 'lt',
    threshold: existingConfig?.threshold ?? initialConfig?.threshold ?? 5,
    no_data_status: existingConfig?.no_data_status ?? initialConfig?.no_data_status ?? 'failure',
    auth_type: existingConfig?.auth_type ?? initialConfig?.auth_type ?? 'none',
    username: existingConfig?.username ?? initialConfig?.username ?? '',
    password: existingConfig?.password ?? initialConfig?.password ?? '',
    bearer_token: existingConfig?.bearer_token ?? initialConfig?.bearer_token ?? '',
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
  });

  const [errors, setErrors] = useState<Record<string, string>>({});

  const [preview, setPreview] = useState<TestMonitorConfigResponse | null>(null);
  const [previewError, setPreviewError] = useState('');
  const [previewSettings, setPreviewSettings] = useState('');
  const [testing, setTesting] = useState(false);
  const [previewLocation, setPreviewLocation] = useState('');
  const [locations, setLocations] = useState<Location[]>([]);
  const buildConfig = (): PrometheusMonitorConfig => ({
    url: formData.url.trim(), query: formData.query.trim(),
    operator: formData.operator, threshold: formData.threshold,
    no_data_status: formData.no_data_status, auth_type: formData.auth_type,
    username: formData.auth_type === 'basic' ? formData.username : '',
    password: formData.auth_type === 'basic' ? formData.password : '',
    bearer_token: formData.auth_type === 'bearer' ? formData.bearer_token : '',
  });
  const settingsKey = JSON.stringify([buildConfig(), formData.timeout_seconds, formData.location_ids, previewLocation]);
  const testQuery = async () => {
    setTesting(true); setPreview(null); setPreviewError(''); setPreviewSettings(settingsKey);
    try {
      setPreview(await testMonitorConfig({
        type: 'prometheus', config: buildConfig(), timeout_seconds: formData.timeout_seconds,
        monitor_id: monitor?.id,
        location_id: formData.location_ids.includes(previewLocation) ? previewLocation : formData.location_ids[0],
      }));
    } catch (error) {
      setPreviewError(error instanceof Error ? error.message : 'Query preview failed');
    } finally { setTesting(false); }
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setErrors({});

    const newErrors: Record<string, string> = {};
    if (!formData.name.trim()) newErrors.name = 'Name is required';
    if (!formData.url.trim()) newErrors.url = 'Endpoint is required';
    if (!formData.query.trim()) newErrors.query = 'Query is required';
    if (!Number.isFinite(formData.threshold)) newErrors.threshold = 'Enter a finite threshold';
    if (formData.timeout_seconds >= formData.interval_seconds) {
      newErrors.timeout_seconds = 'Timeout must be less than interval';
    }

    if (Object.keys(newErrors).length > 0) {
      setErrors(newErrors);
      return;
    }

    const config = buildConfig();

    const requestData: CreateMonitorRequest | UpdateMonitorRequest = {
      name: formData.name.trim(),
      type: 'prometheus',
      config,
      interval_seconds: formData.interval_seconds,
      timeout_seconds: formData.timeout_seconds,
      enabled: formData.enabled,
    };

    requestData.consecutive_failures_threshold = formData.consecutive_failures_threshold;
    requestData.notification_mode = formData.notification_mode;
    requestData.dependency_suppression = formData.dependency_suppression;
    requestData.notification_channels = formData.notification_mode === 'custom' ? formData.notification_channels : [];
    requestData.tags = formData.tags.split(',').map(t => t.trim()).filter(Boolean);
    requestData.location_ids = formData.location_ids;
    requestData.location_quorum =
      formData.location_ids.length >= 2
        ? Math.min(formData.location_quorum, formData.location_ids.length)
        : 1;

    await onSubmit(requestData);
  };

  return (
    <form onSubmit={handleSubmit} className="space-y-5">
      <FormSection title="Prometheus query">
        <FormField label="Monitor name" required error={errors.name}>
          <input aria-label="Monitor name" className="input" value={formData.name} onChange={e => setFormData({...formData, name: e.target.value})} placeholder="API error rate" required />
        </FormField>
        <FormField label="Prometheus base URL" required error={errors.url} description="Include any reverse-proxy path prefix. The query endpoint is added automatically.">
          <input aria-label="Prometheus base URL" className="input" type="url" value={formData.url} onChange={e => setFormData({...formData, url: e.target.value})} placeholder="https://prometheus.example.com" required />
        </FormField>
        <FormField label="PromQL query" required error={errors.query} description="Return a scalar or instant vector. Use sum(), max() or another PromQL aggregation when you need a single value.">
          <textarea aria-label="PromQL query" className="input font-mono" rows={4} value={formData.query} onChange={e => setFormData({...formData, query: e.target.value})} placeholder={'100 * sum(rate(http_requests_total{status=~"5.."}[5m])) / sum(rate(http_requests_total[5m]))'} required />
        </FormField>
        <div className="grid grid-cols-2 gap-4">
          <FormField label="Healthy when every value is">
            <select aria-label="Healthy comparison" className="input" value={formData.operator} onChange={e => setFormData({...formData, operator: e.target.value as PrometheusMonitorConfig['operator']})}>
              <option value="lt">Less than (&lt;)</option><option value="lte">At most (≤)</option>
              <option value="gt">Greater than (&gt;)</option><option value="gte">At least (≥)</option>
              <option value="eq">Equal to (=)</option><option value="ne">Not equal to (≠)</option>
            </select>
          </FormField>
          <FormField label="Threshold" required error={errors.threshold}>
            <input aria-label="Threshold" className="input" type="number" step="any" required value={Number.isNaN(formData.threshold) ? '' : formData.threshold} onChange={e => setFormData({...formData, threshold: e.target.valueAsNumber})} />
          </FormField>
        </div>
        <FormField label="When the query returns no samples" description="Query errors, warnings and non-finite values always report an error.">
          <select aria-label="No samples status" className="input" value={formData.no_data_status} onChange={e => setFormData({...formData, no_data_status: e.target.value as NonNullable<PrometheusMonitorConfig['no_data_status']>})}>
            <option value="failure">Down</option><option value="error">Error</option><option value="success">Up</option>
          </select>
        </FormField>
      </FormSection>
      <FormSection title="Authentication">
        <FormField label="Authentication method">
          <select aria-label="Authentication method" className="input" value={formData.auth_type} onChange={e => setFormData({...formData, auth_type: e.target.value as NonNullable<PrometheusMonitorConfig['auth_type']>})}>
            <option value="none">None</option><option value="basic">Basic authentication</option><option value="bearer">Bearer token</option>
          </select>
        </FormField>
        {formData.auth_type === 'basic' && <>
          <FormField label="Username" required><input aria-label="Username" className="input" autoComplete="off" value={formData.username} onChange={e => setFormData({...formData, username: e.target.value})} required /></FormField>
          <FormField label="Password" description="Stored encrypted. Leave the masked value to keep the saved password."><input aria-label="Password" className="input" type="password" autoComplete="new-password" value={formData.password} onChange={e => setFormData({...formData, password: e.target.value})} /></FormField>
        </>}
        {formData.auth_type === 'bearer' && <FormField label="Bearer token" required description="Stored encrypted. Leave the masked value to keep the saved token."><input aria-label="Bearer token" className="input" type="password" autoComplete="new-password" value={formData.bearer_token} onChange={e => setFormData({...formData, bearer_token: e.target.value})} required /></FormField>}
      </FormSection>
      <FormSection title="Query preview">
        <p className="text-sm text-slate-400">Run the current query without saving. Results show up to 20 values; the threshold checks every returned sample.</p>
        {formData.location_ids.length > 1 && <FormField label="Preview location">
          <select className="input" value={formData.location_ids.includes(previewLocation) ? previewLocation : formData.location_ids[0]} onChange={e => setPreviewLocation(e.target.value)}>
            {formData.location_ids.map(id => <option key={id} value={id}>{locations.find(location => location.id === id)?.name ?? id}</option>)}
          </select>
        </FormField>}
        <Button type="button" variant="ghost" loading={testing} disabled={loading} onClick={testQuery}>{testing ? 'Running query…' : 'Test query'}</Button>
        <div aria-live="polite">
          {previewError && <p className="text-sm text-red-400">{previewError}</p>}
          {preview && (
            <div className="space-y-2 rounded-lg border border-white/[0.06] bg-slate-900/40 px-4 py-3 text-sm">
              {previewSettings !== settingsKey && (
                <p className="text-amber-400">Settings changed since this preview. Run the query again to check the current settings.</p>
              )}
              <div className="flex items-center justify-between gap-3">
                <span className="text-slate-500">Result</span>
                <span className={`font-medium uppercase ${PREVIEW_STATUS_COLORS[preview.status] ?? 'text-slate-300'}`}>
                  {preview.status === 'success' ? 'Up' : preview.status === 'failure' ? 'Down' : preview.status}
                  {preview.latency_ms !== undefined && <span className="ml-2 font-normal normal-case text-slate-400">{preview.latency_ms} ms</span>}
                </span>
              </div>
              {preview.error_message && <p className="text-amber-400">{preview.error_message}</p>}
              {preview.metrics_data?.prometheus && (
                <>
                  <div className="flex items-center justify-between gap-3">
                    <span className="text-slate-500">Samples</span>
                    <span className="text-slate-300">
                      {preview.metrics_data.prometheus.sample_count}
                      {' · '}
                      <span className={preview.metrics_data.prometheus.failed_count > 0 ? 'text-red-400' : 'text-emerald-400'}>
                        {preview.metrics_data.prometheus.failed_count} outside threshold
                      </span>
                    </span>
                  </div>
                  {preview.metrics_data.prometheus.values.length > 0 && (
                    <p className="break-all font-mono text-xs text-slate-300">
                      {preview.metrics_data.prometheus.values.join(', ')}
                      {preview.metrics_data.prometheus.sample_count > preview.metrics_data.prometheus.values.length && (
                        <span className="text-slate-500"> … first {preview.metrics_data.prometheus.values.length} of {preview.metrics_data.prometheus.sample_count}</span>
                      )}
                    </p>
                  )}
                </>
              )}
            </div>
          )}
        </div>
      </FormSection>

      <FormSection title="Schedule">
        <div className="grid grid-cols-2 gap-4">
          <FormField label="Interval (seconds)" description="How often to check">
            <input
              type="number"
              value={formData.interval_seconds}
              onChange={(e) => setFormData({ ...formData, interval_seconds: parseInt(e.target.value, 10) || 60 })}
              min={10}
              step={5}
              className="input"
            />
          </FormField>

          <FormField
            label="Timeout (seconds)"
            error={errors.timeout_seconds}
            description="Must be less than interval"
          >
            <input
              type="number"
              value={formData.timeout_seconds}
              onChange={(e) => setFormData({ ...formData, timeout_seconds: parseInt(e.target.value, 10) || 10 })}
              min={1}
              className="input"
            />
          </FormField>
        </div>
      </FormSection>

      <FormSection
        title="Locations"
        summary={formData.location_ids.length > 0 ? `${formData.location_ids.length} selected` : 'Default fleet'}
        collapsible
        defaultOpen={formData.location_ids.length > 0}
      >
        <LocationsSection
          onLocationsLoaded={setLocations}
          selectedIds={formData.location_ids}
          onChange={(ids) => setFormData({ ...formData, location_ids: ids })}
          quorum={formData.location_quorum}
          onQuorumChange={(n) => setFormData({ ...formData, location_quorum: n })}
        />
      </FormSection>

      <FormSection title="Alerting">
        <AlertingSection
          isGroup={false}
          intervalSeconds={formData.interval_seconds}
          threshold={formData.consecutive_failures_threshold}
          onThresholdChange={(n) => setFormData({ ...formData, consecutive_failures_threshold: n })}
          mode={formData.notification_mode}
          onModeChange={(m) => setFormData({ ...formData, notification_mode: m })}
          dependencySuppression={formData.dependency_suppression}
          onDependencySuppressionChange={(d) => setFormData({ ...formData, dependency_suppression: d })}
          customChannels={formData.notification_channels}
          onCustomChannelsChange={(next) => setFormData({ ...formData, notification_channels: next })}
        />
      </FormSection>

      <FormSection title="Meta">
        <FormField label="Tags" description="Comma-separated, e.g. production, network, critical">
          <input
            type="text"
            value={formData.tags}
            onChange={(e) => setFormData({ ...formData, tags: e.target.value })}
            placeholder="production, network, critical"
            className="input"
          />
        </FormField>

        <div className="flex items-center justify-between rounded-lg border border-white/[0.06] bg-slate-900/40 px-4 py-3">
          <div>
            <p className="text-sm font-medium text-white">Monitor enabled</p>
            <p className="text-xs text-slate-500">Run PromQL checks on schedule</p>
          </div>
          <button
            type="button"
            onClick={() => setFormData({ ...formData, enabled: !formData.enabled })}
            className={`relative h-5 w-9 rounded-full transition-colors ${
              formData.enabled ? 'bg-cyan-500' : 'bg-slate-700'
            }`}
            aria-pressed={formData.enabled}
            aria-label="Toggle monitor enabled"
          >
            <span
              className={`absolute left-0.5 top-0.5 h-4 w-4 rounded-full bg-white transition-transform ${
                formData.enabled ? 'translate-x-4' : ''
              }`}
            />
          </button>
        </div>
      </FormSection>

      <FormActions
        middle={`Every ${formData.interval_seconds}s · down after ${formData.consecutive_failures_threshold} failed checks`}
        cancel={onCancel ? { label: 'Cancel', onClick: onCancel, disabled: loading } : undefined}
        submit={{
          label: loading ? 'Saving…' : isEditMode ? 'Update monitor' : 'Create monitor',
          loading,
          disabled: loading,
          type: 'submit',
        }}
      />
    </form>
  );
}
