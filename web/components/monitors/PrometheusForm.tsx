'use client';

import { useRef, useState } from 'react';
import { testMonitorConfig, type TestMonitorConfigResponse } from '@/lib/api';
import {
  Monitor,
  Location,
  CreateMonitorRequest,
  UpdateMonitorRequest,
  PrometheusMonitorConfig,
} from '@/lib/types';
import { buildPrometheusConfig, buildPrometheusRequest, initialPrometheusFormState } from '@/lib/monitor-form/prometheus';
import Button from '@/components/ui/Button';
import FormField from '@/components/ui/FormField';
import { AlertingSection } from './AlertingSection';
import ScheduleSection from './ScheduleSection';
import DependenciesField from './DependenciesField';
import { FieldHint, FormPanel, LabelsSection, PlanSummary, PlanTarget, downAfterLabel, everyLabel, revealFirstError, reportConstraintViolation, useClearErrorsOnEdit, useFollowEnabled, FormActionBar } from './form-kit';

const PREVIEW_STATUS_COLORS: Record<string, string> = {
  success: 'text-emerald-300',
  failure: 'text-rose-300',
  error: 'text-amber-300',
};

const OPERATOR_SYMBOL: Record<string, string> = { lt: '<', lte: '≤', gt: '>', gte: '≥', eq: '=', ne: '≠' };

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

  const [formData, setFormData] = useState(() => initialPrometheusFormState(monitor, initialData));

  const [errors, setErrors] = useState<Record<string, string>>({});
  useClearErrorsOnEdit(formData, setErrors);
  useFollowEnabled(monitor?.enabled, setFormData);
  const formRef = useRef<HTMLFormElement>(null);

  const [preview, setPreview] = useState<TestMonitorConfigResponse | null>(null);
  const [previewError, setPreviewError] = useState('');
  const [previewSettings, setPreviewSettings] = useState('');
  const [testing, setTesting] = useState(false);
  const [previewLocation, setPreviewLocation] = useState('');
  const [locations, setLocations] = useState<Location[]>([]);
  const buildConfig = (): PrometheusMonitorConfig => buildPrometheusConfig(formData);
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
    if (reportConstraintViolation(formRef.current)) return;
    setErrors({});

    const newErrors: Record<string, string> = {};
    if (!formData.name.trim()) newErrors.name = 'Name is required';
    if (!formData.url.trim()) newErrors.url = 'Endpoint is required';
    if (!formData.query.trim()) newErrors.query = 'Query is required';
    if (!Number.isFinite(formData.threshold)) newErrors.threshold = 'Enter a finite threshold';
    if (formData.auth_type === 'basic' && !formData.username.trim()) newErrors.username = 'Username is required';
    if (formData.auth_type === 'bearer' && !formData.bearer_token) newErrors.bearer_token = 'Bearer token is required';
    if (formData.timeout_seconds >= formData.interval_seconds) {
      newErrors.timeout_seconds = 'Timeout must be less than interval';
    }

    if (Object.keys(newErrors).length > 0) {
      setErrors(newErrors);
      revealFirstError(formRef.current);
      return;
    }

    const requestData: CreateMonitorRequest | UpdateMonitorRequest = buildPrometheusRequest(formData);

    await onSubmit(requestData);
  };

  return (
    <form ref={formRef} onSubmit={handleSubmit} noValidate data-form-stack className="space-y-5">
      <FormPanel title="Target">
        <FormField label="Monitor name" required error={errors.name}>
          <input className="input" value={formData.name} onChange={e => setFormData({...formData, name: e.target.value})} placeholder="API error rate" />
        </FormField>
        <FormField label="Prometheus base URL" required error={errors.url}>
          {(control) => (
            <>
              <input {...control} className="input font-mono" type="url" value={formData.url} onChange={e => setFormData({...formData, url: e.target.value})} placeholder="https://prometheus.example.com" />
              <FieldHint>Include any reverse-proxy path prefix. The query endpoint is added automatically.</FieldHint>
            </>
          )}
        </FormField>
        <FormField label="Authentication">
          <select className="input" value={formData.auth_type} onChange={e => setFormData({...formData, auth_type: e.target.value as NonNullable<PrometheusMonitorConfig['auth_type']>})}>
            <option value="none">None</option><option value="basic">Basic authentication</option><option value="bearer">Bearer token</option>
          </select>
        </FormField>
        {formData.auth_type === 'basic' && <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <FormField label="Username" required error={errors.username}><input className="input" autoComplete="off" value={formData.username} onChange={e => setFormData({...formData, username: e.target.value})} /></FormField>
          <FormField label="Password">{(control) => <><input {...control} className="input" type="password" autoComplete="new-password" value={formData.password} onChange={e => setFormData({...formData, password: e.target.value})} /><FieldHint>Stored encrypted. Leave the masked value to keep the saved password.</FieldHint></>}</FormField>
        </div>}
        {formData.auth_type === 'bearer' && <FormField label="Bearer token" required error={errors.bearer_token}>{(control) => <><input {...control} className="input" type="password" autoComplete="new-password" value={formData.bearer_token} onChange={e => setFormData({...formData, bearer_token: e.target.value})} /><FieldHint>Stored encrypted. Leave the masked value to keep the saved token.</FieldHint></>}</FormField>}
      </FormPanel>
      <FormPanel title="Check">
        <FormField label="PromQL query" required error={errors.query}>
          {(control) => (
            <>
              <textarea {...control} className="input font-mono" rows={4} value={formData.query} onChange={e => setFormData({...formData, query: e.target.value})} placeholder={'100 * sum(rate(http_requests_total{status=~"5.."}[5m])) / sum(rate(http_requests_total[5m]))'} />
              <FieldHint>Return a scalar or instant vector. Use sum(), max() or another PromQL aggregation when you need a single value.</FieldHint>
            </>
          )}
        </FormField>
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <FormField label="Healthy when every value is">
            <select className="input" value={formData.operator} onChange={e => setFormData({...formData, operator: e.target.value as PrometheusMonitorConfig['operator']})}>
              <option value="lt">Less than (&lt;)</option><option value="lte">At most (≤)</option>
              <option value="gt">Greater than (&gt;)</option><option value="gte">At least (≥)</option>
              <option value="eq">Equal to (=)</option><option value="ne">Not equal to (≠)</option>
            </select>
          </FormField>
          <FormField label="Threshold" required error={errors.threshold}>
            <input className="input tabular-nums" type="number" step="any" value={Number.isNaN(formData.threshold) ? '' : formData.threshold} onChange={e => setFormData({...formData, threshold: e.target.valueAsNumber})} />
          </FormField>
        </div>
        <FormField label="When the query returns no samples">
          {(control) => (
            <>
              <select {...control} className="input" value={formData.no_data_status} onChange={e => setFormData({...formData, no_data_status: e.target.value as NonNullable<PrometheusMonitorConfig['no_data_status']>})}>
                <option value="failure">Down</option><option value="error">Error</option><option value="success">Up</option>
              </select>
              <FieldHint>Query errors, warnings and non-finite values always report an error.</FieldHint>
            </>
          )}
        </FormField>
        {formData.location_ids.length > 1 && <FormField label="Preview location">
          <select className="input" value={formData.location_ids.includes(previewLocation) ? previewLocation : formData.location_ids[0]} onChange={e => setPreviewLocation(e.target.value)}>
            {formData.location_ids.map(id => <option key={id} value={id}>{locations.find(location => location.id === id)?.name ?? id}</option>)}
          </select>
        </FormField>}
        <div>
          <div className="glass-well flex flex-wrap items-center gap-3 px-4 py-3">
            <Button type="button" variant="ghost" loading={testing} disabled={loading} onClick={testQuery}>{testing ? 'Running query…' : 'Test query'}</Button>
            <p className="text-xs text-slate-500">Run the current query without saving. Results show up to 20 values; the threshold checks every returned sample.</p>
          </div>
          <div aria-live="polite">
            {previewError && <p className="mt-3 text-sm text-rose-300">{previewError}</p>}
            {preview && (
              <div className="glass-well mt-3 space-y-2 p-4 text-sm">
                {previewSettings !== settingsKey && (
                  <p className="text-amber-300">Settings changed since this preview. Run the query again to check the current settings.</p>
                )}
                <div className="flex items-center justify-between gap-3">
                  <span className="text-slate-500">Result</span>
                  <span className={`font-medium capitalize ${PREVIEW_STATUS_COLORS[preview.status] ?? 'text-slate-300'}`}>
                    {preview.status === 'success' ? 'Up' : preview.status === 'failure' ? 'Down' : preview.status}
                    {preview.latency_ms !== undefined && <span className="ml-2 font-normal normal-case text-slate-400">{preview.latency_ms} ms</span>}
                  </span>
                </div>
                {preview.error_message && <p className="text-amber-300">{preview.error_message}</p>}
                {preview.metrics_data?.prometheus && (
                  <>
                    <div className="flex items-center justify-between gap-3">
                      <span className="text-slate-500">Samples</span>
                      <span className="text-slate-300">
                        {preview.metrics_data.prometheus.sample_count}
                        <span className={`ml-3 ${preview.metrics_data.prometheus.failed_count > 0 ? 'text-rose-300' : 'text-emerald-300'}`}>
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
        </div>
      </FormPanel>

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
        enabledDescription="Run PromQL checks on this schedule"
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
          onThresholdChange={(n) => setFormData((prev) => ({ ...prev, consecutive_failures_threshold: n }))}
          mode={formData.notification_mode}
          onModeChange={(m) => setFormData((prev) => ({ ...prev, notification_mode: m }))}
          dependencySuppression={formData.dependency_suppression}
          hasDependencies={formData.depends_on_ids.length > 0}
          onDependencySuppressionChange={(d) => setFormData((prev) => ({ ...prev, dependency_suppression: d }))}
          customChannels={formData.notification_channels}
          onCustomChannelsChange={(next) => setFormData((prev) => ({ ...prev, notification_channels: next }))}
        />
      </FormPanel>

      <LabelsSection
        tags={formData.tags}
        onTagsChange={(v) => setFormData({ ...formData, tags: v })}
        placeholder="production, network, critical"
      />

      <FormActionBar
        summary={
          <PlanSummary
            errors={errors}
            parts={
              formData.query.trim()
                ? [
                    everyLabel(formData.interval_seconds),
                    <PlanTarget key="t">PromQL</PlanTarget>,
                    `healthy while every value is ${OPERATOR_SYMBOL[formData.operator] ?? formData.operator} ${formData.threshold}`,
                    downAfterLabel(formData.consecutive_failures_threshold),
                  ]
                : [everyLabel(formData.interval_seconds), downAfterLabel(formData.consecutive_failures_threshold)]
            }
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
