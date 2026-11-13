'use client';

import { Monitor } from '@/lib/types';
import { AlertingSection } from '@/components/monitors/AlertingSection';
import DependenciesField from '@/components/monitors/DependenciesField';
import { FormPanel } from '@/components/monitors/form-kit';
import { type SipFormData, type SetSipFormData } from '@/components/monitors/sip/types';

interface SipAlertingPanelProps {
  formData: SipFormData;
  setFormData: SetSipFormData;
  errors: Record<string, string>;
  monitor?: Monitor;
}

/** SipForm's Alerting panel: dependencies, the down threshold and notification routing. */
export default function SipAlertingPanel({ formData, setFormData, errors, monitor }: SipAlertingPanelProps) {
  return (
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
  );
}
