'use client';

import { useState, useEffect, useRef } from 'react';
import { Link2 } from 'lucide-react';
import { Monitor, CreateMonitorRequest, UpdateMonitorRequest, PushMonitorConfig, PushInfo } from '@/lib/types';
import { getPushInfo } from '@/lib/api';
import { buildPushRequest, initialPushFormState } from '@/lib/monitor-form/heartbeat';
import FormField from '@/components/ui/FormField';
import FormSection from '@/components/ui/FormSection';
import FormActions from '@/components/ui/FormActions';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import { AlertingSection } from './AlertingSection';
import { useCurrentUser } from '@/components/providers/CurrentUserProvider';
import { IntervalField } from './ScheduleSection';
import DependenciesField from './DependenciesField';
import { FieldHint, FormPanel, LabelsSection, NumberInput, PlanSummary, ToggleRow, everyLabel, revealFirstError, reportConstraintViolation, useClearErrorsOnEdit, useFollowEnabled } from './form-kit';

interface PushFormProps {
  monitor?: Monitor;
  initialData?: CreateMonitorRequest;
  onSubmit: (data: CreateMonitorRequest | UpdateMonitorRequest) => Promise<void>;
  onCancel?: () => void;
  loading?: boolean;
}

const INTERVAL_PRESETS = [15, 30, 60, 300];

export default function PushForm({
  monitor,
  initialData,
  onSubmit,
  onCancel,
  loading = false,
}: PushFormProps) {
  const { canWrite, loading: loadingUser } = useCurrentUser();
  const canViewCredentials = canWrite && !loadingUser;
  const [showWebhookInfo, setShowWebhookInfo] = useState(false);
  const [pushInfo, setPushInfo] = useState<PushInfo | null>(null);
  const [loadingPushInfo, setLoadingPushInfo] = useState(false);
  const [copiedField, setCopiedField] = useState<string | null>(null);
  const isEditMode = Boolean(monitor);
  const initialPushConfig =
    !isEditMode && initialData?.type === 'push' ? (initialData.config as PushMonitorConfig) : undefined;

  const [formData, setFormData] = useState(() => initialPushFormState(monitor, initialData));

  const [errors, setErrors] = useState<Record<string, string>>({});
  useClearErrorsOnEdit(formData, setErrors);
  useFollowEnabled(monitor?.enabled, setFormData);
  const formRef = useRef<HTMLFormElement>(null);

  useEffect(() => {
    if (!isEditMode && !initialPushConfig) {
      setFormData(prev => ({
        ...prev,
        grace_period_seconds: prev.expected_interval_seconds * 2,
      }));
    }
  }, [formData.expected_interval_seconds, isEditMode, initialPushConfig]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (reportConstraintViolation(formRef.current)) return;
    setErrors({});

    const newErrors: Record<string, string> = {};
    if (!formData.name.trim()) newErrors.name = 'Name is required';
    if (formData.expected_interval_seconds < 5) newErrors.expected_interval_seconds = 'Minimum 5 seconds';
    if (formData.grace_period_seconds < 0) newErrors.grace_period_seconds = 'Cannot be negative';

    if (Object.keys(newErrors).length > 0) {
      setErrors(newErrors);
      revealFirstError(formRef.current);
      return;
    }

    const requestData: CreateMonitorRequest | UpdateMonitorRequest = buildPushRequest(formData, monitor);

    try {
      await onSubmit(requestData);
      if (!isEditMode) setShowWebhookInfo(true);
    } catch (error) {
      console.error('Failed to save monitor:', error);
    }
  };

  useEffect(() => {
    if (!monitor?.id || !showWebhookInfo || !canViewCredentials) return;
    let cancelled = false;
    setPushInfo(null);
    setLoadingPushInfo(true);
    getPushInfo(monitor.id)
      .then(info => { if (!cancelled) setPushInfo(info); })
      .catch(error => { if (!cancelled) console.error('Failed to load push info:', error); })
      .finally(() => { if (!cancelled) setLoadingPushInfo(false); });
    return () => { cancelled = true; };
  }, [monitor?.id, showWebhookInfo, canViewCredentials]);

  const copyToClipboard = (text: string, field: string) => {
    navigator.clipboard.writeText(text);
    setCopiedField(field);
    setTimeout(() => setCopiedField(null), 2000);
  };

  if (canViewCredentials && isEditMode && monitor && showWebhookInfo) {
    return (
      <div className="space-y-5">
        <FormSection title="Webhook information">
          {loadingPushInfo ? (
            <div className="text-sm text-slate-500">Loading webhook details…</div>
          ) : pushInfo ? (
            <>
              <FormField label="Webhook URL">
                <div className="flex items-center gap-2">
                  <code className="glass-well flex-1 overflow-x-auto px-3 py-2 font-mono text-xs text-slate-200">
                    {pushInfo.webhook_url}
                  </code>
                  <Button
                    variant="ghost"
                    size="xs"
                    type="button"
                    onClick={() => copyToClipboard(pushInfo.webhook_url, 'webhook_url')}
                  >
                    {copiedField === 'webhook_url' ? 'Copied' : 'Copy'}
                  </Button>
                </div>
              </FormField>

              <FormField label="Push token">
                <div className="flex items-center gap-2">
                  <code className="glass-well flex-1 overflow-x-auto px-3 py-2 font-mono text-xs text-slate-200">
                    {pushInfo.push_token}
                  </code>
                  <Button
                    variant="ghost"
                    size="xs"
                    type="button"
                    onClick={() => copyToClipboard(pushInfo.push_token, 'push_token')}
                  >
                    {copiedField === 'push_token' ? 'Copied' : 'Copy'}
                  </Button>
                </div>
              </FormField>

              <FormField label="Example usage">
                <pre className="glass-well overflow-x-auto whitespace-pre-wrap px-3 py-2 font-mono text-xs text-slate-300">
                  {pushInfo.example_curl}
                </pre>
                <div className="mt-2">
                  <Button
                    variant="ghost"
                    size="xs"
                    type="button"
                    onClick={() => copyToClipboard(pushInfo.example_curl, 'example')}
                  >
                    {copiedField === 'example' ? 'Copied' : 'Copy examples'}
                  </Button>
                </div>
              </FormField>

              <div>
                <p className="text-sm font-medium text-slate-200">Auto-detected metrics</p>
                <p className="mt-0.5 text-xs text-slate-500">
                  Any additional parameters you send (besides &quot;status&quot; and &quot;error&quot;) will be
                  captured and displayed as metrics. Send latency, CPU, memory, or any custom metrics.
                </p>
              </div>

              <div>
                <p className="text-sm font-medium text-slate-200">Expected timing</p>
                <p className="mt-0.5 text-xs text-slate-500">
                  Push every <strong className="font-medium text-slate-300">{pushInfo.interval_seconds}s</strong>. Grace period:{' '}
                  <strong className="font-medium text-slate-300">{pushInfo.grace_period_seconds}s</strong>. Monitor goes down if no push within{' '}
                  {pushInfo.interval_seconds + pushInfo.grace_period_seconds}s.
                </p>
              </div>
            </>
          ) : (
            <Callout tone="danger">Failed to load webhook details</Callout>
          )}
        </FormSection>

        <div className="flex items-center justify-end gap-2">
          <Button variant="ghost" size="sm" type="button" onClick={() => setShowWebhookInfo(false)}>
            Back to settings
          </Button>
          {onCancel && (
            <Button variant="accent" size="sm" type="button" onClick={onCancel}>
              Done
            </Button>
          )}
        </div>
      </div>
    );
  }

  return (
    <form ref={formRef} onSubmit={handleSubmit} noValidate className="space-y-5">
      <FormSection title="Target">
        <FormField label="Monitor name" required error={errors.name}>
          <input
            type="text"
            value={formData.name}
            onChange={(e) => setFormData({ ...formData, name: e.target.value })}
            placeholder="My service health"
            className="input"
          />
        </FormField>
        {canViewCredentials && isEditMode && monitor ? (
          <div>
            <Button
              variant="ghost"
              type="button"
              icon={<Link2 strokeWidth={1.75} />}
              onClick={() => setShowWebhookInfo(true)}
            >
              View webhook URL and instructions
            </Button>
          </div>
        ) : (
          !isEditMode && <p className="text-xs text-slate-500">After creating, open the monitor&apos;s Settings tab to copy its webhook URL and token.</p>
        )}
      </FormSection>

      <FormPanel title="Schedule">
        <IntervalField
          label="Expect a push every"
          value={formData.expected_interval_seconds}
          onChange={(n) => setFormData((prev) => ({ ...prev, expected_interval_seconds: n }))}
          presets={INTERVAL_PRESETS}
          min={5}
          step={5}
          error={errors.expected_interval_seconds}
          hint="How often your service sends a push"
        />

        <FormField label="Grace period" error={errors.grace_period_seconds}>
          {(control) => (
            <>
              <span className="flex items-center gap-2">
                <NumberInput
                  {...control}
                  value={formData.grace_period_seconds}
                  onChange={(n) => setFormData((prev) => ({ ...prev, grace_period_seconds: n }))}
                  min={0}
                  step={5}
                  className="input input-sm !w-24 tabular-nums"
                />
                <span className="text-xs text-slate-500">seconds</span>
              </span>
              {!errors.grace_period_seconds && (
                <FieldHint>
                  Buffer before marking down. Total window: {formData.expected_interval_seconds + formData.grace_period_seconds}s
                </FieldHint>
              )}
            </>
          )}
        </FormField>

        <ToggleRow
          title="Monitor enabled"
          description="Accept pushes and track uptime"
          checked={formData.enabled}
          onChange={(v) => setFormData((prev) => ({ ...prev, enabled: v }))}
        />
      </FormPanel>

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
          intervalSeconds={formData.expected_interval_seconds}
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
        placeholder="production, backend, critical"
      />

      <FormActions
        middle={
          <PlanSummary
            errors={errors}
            parts={[
              `Expect a push every ${formData.expected_interval_seconds}s`,
              `down if none arrives within ${formData.expected_interval_seconds + formData.grace_period_seconds}s`,
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
