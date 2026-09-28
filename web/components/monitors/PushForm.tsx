'use client';

import { useState, useEffect } from 'react';
import { Link2 } from 'lucide-react';
import { Monitor, CreateMonitorRequest, UpdateMonitorRequest, PushMonitorConfig, PushInfo, DependencySuppression, NotificationMode, ChannelAssignment } from '@/lib/types';
import { getPushInfo } from '@/lib/api';
import FormField from '@/components/ui/FormField';
import FormSection from '@/components/ui/FormSection';
import FormActions from '@/components/ui/FormActions';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import FilterChip from '@/components/ui/FilterChip';
import { AlertingSection } from './AlertingSection';
import { useCurrentUser } from '@/components/providers/CurrentUserProvider';
import Switch from '@/components/ui/Switch';

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

  const [formData, setFormData] = useState({
    name: monitor?.name || initialData?.name || '',
    expected_interval_seconds: monitor && monitor.type === 'push'
      ? (monitor.config as PushMonitorConfig)?.expected_interval_seconds || 60
      : initialPushConfig?.expected_interval_seconds || 60,
    grace_period_seconds: monitor && monitor.type === 'push'
      ? (monitor.config as PushMonitorConfig)?.grace_period_seconds || 120
      : initialPushConfig?.grace_period_seconds || 120,
    enabled: monitor?.enabled ?? initialData?.enabled ?? true,
    tags: monitor?.tags?.join(', ') || (initialData?.tags || []).join(', '),
    consecutive_failures_threshold: monitor?.consecutive_failures_threshold ?? 2,
    notification_mode: (monitor?.notification_mode ?? 'default') as NotificationMode,
    dependency_suppression: (monitor?.dependency_suppression ?? 'inherit') as DependencySuppression,
    notification_channels: monitor?.notification_channels ?? [] as ChannelAssignment[],
  });

  const [errors, setErrors] = useState<Record<string, string>>({});

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
    setErrors({});

    const newErrors: Record<string, string> = {};
    if (!formData.name.trim()) newErrors.name = 'Name is required';
    if (formData.expected_interval_seconds < 5) newErrors.expected_interval_seconds = 'Minimum 5 seconds';
    if (formData.grace_period_seconds < 0) newErrors.grace_period_seconds = 'Cannot be negative';

    if (Object.keys(newErrors).length > 0) {
      setErrors(newErrors);
      return;
    }

    const config: PushMonitorConfig = {
      push_token: isEditMode ? monitor?.push_token || '' : '',
      expected_interval_seconds: formData.expected_interval_seconds,
      grace_period_seconds: formData.grace_period_seconds,
    };

    const requestData: CreateMonitorRequest | UpdateMonitorRequest = {
      name: formData.name.trim(),
      type: 'push',
      config,
      interval_seconds: formData.expected_interval_seconds,
      timeout_seconds: formData.expected_interval_seconds,
      enabled: formData.enabled,
    };

    requestData.consecutive_failures_threshold = formData.consecutive_failures_threshold;
    requestData.notification_mode = formData.notification_mode;
    requestData.dependency_suppression = formData.dependency_suppression;
    requestData.notification_channels = formData.notification_mode === 'custom' ? formData.notification_channels : [];
    if (formData.tags.trim()) {
      requestData.tags = formData.tags.split(',').map(t => t.trim()).filter(t => t);
    }

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
    <form onSubmit={handleSubmit} className="space-y-5">
      <FormSection title="Monitor">
        <FormField label="Monitor name" required error={errors.name}>
          <input
            type="text"
            value={formData.name}
            onChange={(e) => setFormData({ ...formData, name: e.target.value })}
            placeholder="My service health"
            className="input"
          />
        </FormField>
        {canViewCredentials && isEditMode && monitor && (
          <div>
            <Button
              variant="ghost"
              type="button"
              icon={<Link2 strokeWidth={1.75} />}
              onClick={() => setShowWebhookInfo(true)}
            >
              View webhook URL &amp; instructions
            </Button>
          </div>
        )}
      </FormSection>

      <FormSection title="Timing">
        <FormField
          label="Expected interval (seconds)"
          required
          error={errors.expected_interval_seconds}
        >
          <div className="mb-2 flex flex-wrap gap-1.5">
            {INTERVAL_PRESETS.map((preset) => (
              <FilterChip
                key={preset}
                selected={formData.expected_interval_seconds === preset}
                onClick={() => setFormData({ ...formData, expected_interval_seconds: preset })}
              >
                {preset >= 60 ? `${preset / 60}m` : `${preset}s`}
              </FilterChip>
            ))}
          </div>
          <input
            type="number"
            value={formData.expected_interval_seconds}
            onChange={(e) => setFormData({ ...formData, expected_interval_seconds: parseInt(e.target.value) || 60 })}
            min={5}
            step={5}
            className="input"
          />
          <p className="mt-1 text-xs text-slate-500">How often your service sends a push</p>
        </FormField>

        <FormField
          label="Grace period (seconds)"
          error={errors.grace_period_seconds}
        >
          <input
            type="number"
            value={formData.grace_period_seconds}
            onChange={(e) => setFormData({ ...formData, grace_period_seconds: parseInt(e.target.value) || 0 })}
            min={0}
            step={5}
            className="input"
          />
          <p className="mt-1 text-xs text-slate-500">Buffer before marking down. Total window: {formData.expected_interval_seconds + formData.grace_period_seconds}s</p>
        </FormField>
      </FormSection>

      <FormSection title="Alerting">
        <AlertingSection
          isGroup={false}
          intervalSeconds={formData.expected_interval_seconds}
          threshold={formData.consecutive_failures_threshold}
          onThresholdChange={(n) => setFormData({ ...formData, consecutive_failures_threshold: n })}
          mode={formData.notification_mode}
          onModeChange={(m) => setFormData({ ...formData, notification_mode: m })}
          dependencySuppression={formData.dependency_suppression}
          hasDependencies={(monitor?.depends_on_ids?.length ?? 0) > 0}
          onDependencySuppressionChange={(d) => setFormData({ ...formData, dependency_suppression: d })}
          customChannels={formData.notification_channels}
          onCustomChannelsChange={(next) => setFormData({ ...formData, notification_channels: next })}
        />
      </FormSection>

      <FormSection title="Meta">
        <FormField label="Tags">
          <input
            type="text"
            value={formData.tags}
            onChange={(e) => setFormData({ ...formData, tags: e.target.value })}
            placeholder="production, backend, critical"
            className="input"
          />
          <p className="mt-1 text-xs text-slate-500">Comma-separated tags for filtering</p>
        </FormField>

        <div className="flex items-center justify-between gap-4">
          <div className="min-w-0">
            <p className="text-sm font-medium text-slate-200">Monitor enabled</p>
            <p className="text-xs text-slate-500">Accept pushes and track uptime</p>
          </div>
          <Switch checked={formData.enabled} onChange={() => setFormData({ ...formData, enabled: !formData.enabled })} label="Toggle monitor enabled" />
        </div>
      </FormSection>

      <FormActions
        middle={`Expect a push every ${formData.expected_interval_seconds}s · down if none arrives within ${formData.expected_interval_seconds + formData.grace_period_seconds}s`}
        cancel={onCancel ? { label: 'Cancel', onClick: onCancel, disabled: loading } : undefined}
        submit={{
          label: loading ? 'Saving…' : isEditMode ? 'Save changes' : 'Create push monitor',
          loading,
          disabled: loading,
          type: 'submit',
        }}
      />
    </form>
  );
}
