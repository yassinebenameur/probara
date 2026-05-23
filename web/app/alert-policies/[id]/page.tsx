'use client';

import { useRouter, useParams } from 'next/navigation';
import { useState, useEffect } from 'react';
import { AlertChannel, AlertPolicy, CreateAlertPolicyRequest, UpdateAlertPolicyRequest } from '@/lib/types';
import { getAlertChannels, getAlertPolicy, updateAlertPolicy } from '@/lib/api';
import AlertPolicyForm from '@/components/alert-policies/AlertPolicyForm';
import BulkAttachPolicyDialog from '@/components/alert-policies/BulkAttachPolicyDialog';
import Toast from '@/components/ui/Toast';
import PageHeader from '@/components/ui/PageHeader';
import FormCard from '@/components/ui/FormCard';
import Button from '@/components/ui/Button';

export default function EditAlertPolicyPage() {
  const router = useRouter();
  const params = useParams();
  const id = params.id as string;

  const [policy, setPolicy] = useState<AlertPolicy | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string>('');
  const [toast, setToast] = useState<{ message: string; type: 'success' | 'error' } | null>(null);
  const [channels, setChannels] = useState<AlertChannel[]>([]);
  const [channelsError, setChannelsError] = useState<string>('');
  const [showAttachMonitors, setShowAttachMonitors] = useState(false);

  useEffect(() => {
    loadPolicy();
  }, [id]);

  useEffect(() => {
    const loadChannels = async () => {
      try {
        const response = await getAlertChannels({ page_size: 100 });
        setChannels(response?.items || []);
        setChannelsError('');
      } catch (err: any) {
        setChannels([]);
        setChannelsError(err.message || 'Failed to load alert channels');
      }
    };
    loadChannels();
  }, []);

  const loadPolicy = async () => {
    try {
      setLoading(true);
      setError('');
      const data = await getAlertPolicy(id);
      setPolicy(data);
    } catch (err: any) {
      setError(err.message || 'Failed to load alert policy');
    } finally {
      setLoading(false);
    }
  };

  const handleSubmit = async (data: CreateAlertPolicyRequest | UpdateAlertPolicyRequest) => {
    const payload: UpdateAlertPolicyRequest = {
      name: data.name,
      description: data.description,
      failure_threshold: data.failure_threshold,
      failure_window_seconds: data.failure_window_seconds,
      channel_ids: data.channel_ids,
      email_subject_template: data.email_subject_template,
      email_body_template: data.email_body_template,
    };

    try {
      setSaving(true);
      const updated = await updateAlertPolicy(id, payload);
      setPolicy(updated);
      setToast({ message: 'Alert policy updated successfully', type: 'success' });
    } catch (err: any) {
      setToast({ message: err.message || 'Failed to update alert policy', type: 'error' });
    } finally {
      setSaving(false);
    }
  };

  if (loading) {
    return (
      <div className="flex h-64 items-center justify-center">
        <div className="text-slate-500">Loading alert policy…</div>
      </div>
    );
  }

  if (error || !policy) {
    return (
      <FormCard>
        <div className="rounded-lg border border-rose-500/20 bg-rose-500/10 px-4 py-3 text-sm text-rose-400">
          {error || 'Alert policy not found'}
        </div>
      </FormCard>
    );
  }

  return (
    <div className="max-w-5xl space-y-6">
      <PageHeader
        breadcrumb={[{ label: 'Alert policies', href: '/alert-policies' }, { label: policy.name }]}
        title="Edit alert policy"
        subtitle="Update alert thresholds and notification channels."
        action={
          <Button variant="ghost" size="sm" onClick={() => setShowAttachMonitors(true)}>
            Attach to monitors
          </Button>
        }
      />

      <div className="grid grid-cols-1 gap-6 lg:grid-cols-3">
        <div className="lg:col-span-2">
          <FormCard>
            <AlertPolicyForm
              policy={policy}
              onSubmit={handleSubmit}
              onCancel={() => router.push('/alert-policies')}
              loading={saving}
              channels={channels}
            />
            {channelsError && (
              <div className="mt-4 text-sm text-rose-500">{channelsError}</div>
            )}
          </FormCard>
        </div>

        <FormCard>
          <h3 className="text-sm font-medium text-white">Policy details</h3>
          <dl className="mt-4 space-y-4">
            <div>
              <dt className="text-xs font-medium text-slate-500">Policy ID</dt>
              <dd className="mt-1 break-all font-mono text-xs text-slate-300">{policy.id}</dd>
            </div>
            <div>
              <dt className="text-xs font-medium text-slate-500">Tenant ID</dt>
              <dd className="mt-1 break-all font-mono text-xs text-slate-300">{policy.tenant_id}</dd>
            </div>
            <div>
              <dt className="text-xs font-medium text-slate-500">Created</dt>
              <dd className="mt-1 text-sm text-slate-300">
                {new Date(policy.created_at).toLocaleString()}
              </dd>
            </div>
            <div>
              <dt className="text-xs font-medium text-slate-500">Last updated</dt>
              <dd className="mt-1 text-sm text-slate-300">
                {new Date(policy.updated_at).toLocaleString()}
              </dd>
            </div>
          </dl>
        </FormCard>
      </div>

      {showAttachMonitors && (
        <BulkAttachPolicyDialog
          open={showAttachMonitors}
          kind="pick-monitors"
          policy={policy}
          onClose={() => setShowAttachMonitors(false)}
          onDone={(result) => {
            setShowAttachMonitors(false);
            const verb = result.op === 'attach' ? 'Attached' : 'Detached';
            setToast({
              message: `${verb} ${result.policyName} on ${result.updated} monitors (${result.unchanged} unchanged)`,
              type: 'success',
            });
          }}
        />
      )}

      {toast && (
        <Toast
          message={toast.message}
          type={toast.type}
          onClose={() => setToast(null)}
        />
      )}
    </div>
  );
}
