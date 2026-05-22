'use client';

import { useRouter, useParams } from 'next/navigation';
import { useState, useEffect } from 'react';
import { AlertChannel, CreateAlertChannelRequest, UpdateAlertChannelRequest } from '@/lib/types';
import { getAlertChannel, updateAlertChannel } from '@/lib/api';
import AlertChannelForm from '@/components/alert-channels/AlertChannelForm';
import Toast from '@/components/ui/Toast';
import PageHeader from '@/components/ui/PageHeader';
import FormCard from '@/components/ui/FormCard';
import Pill from '@/components/ui/Pill';

export default function EditAlertChannelPage() {
  const router = useRouter();
  const params = useParams();
  const id = params.id as string;

  const [channel, setChannel] = useState<AlertChannel | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string>('');
  const [toast, setToast] = useState<{ message: string; type: 'success' | 'error' } | null>(null);

  useEffect(() => {
    loadChannel();
  }, [id]);

  const loadChannel = async () => {
    try {
      setLoading(true);
      setError('');
      const data = await getAlertChannel(id);
      setChannel(data);
    } catch (err: any) {
      setError(err.message || 'Failed to load alert channel');
    } finally {
      setLoading(false);
    }
  };

  const handleSubmit = async (data: CreateAlertChannelRequest | UpdateAlertChannelRequest) => {
    const payload: UpdateAlertChannelRequest = {
      name: data.name,
      config: data.config,
      is_active: data.is_active,
    };

    try {
      setSaving(true);
      const updated = await updateAlertChannel(id, payload);
      setChannel(updated);
      setToast({ message: 'Alert channel updated successfully', type: 'success' });
    } catch (err: any) {
      setToast({ message: err.message || 'Failed to update alert channel', type: 'error' });
    } finally {
      setSaving(false);
    }
  };

  if (loading) {
    return (
      <div className="flex h-64 items-center justify-center">
        <div className="text-slate-500">Loading alert channel…</div>
      </div>
    );
  }

  if (error || !channel) {
    return (
      <FormCard>
        <div className="rounded-lg border border-rose-500/20 bg-rose-500/10 px-4 py-3 text-sm text-rose-400">
          {error || 'Alert channel not found'}
        </div>
      </FormCard>
    );
  }

  return (
    <div className="max-w-5xl space-y-6">
      <PageHeader
        breadcrumb={[{ label: 'Alert channels', href: '/alert-channels' }, { label: channel.name }]}
        title="Edit alert channel"
        subtitle="Update delivery settings and recipients."
      />

      <div className="grid grid-cols-1 gap-6 lg:grid-cols-3">
        <div className="lg:col-span-2">
          <FormCard>
            <AlertChannelForm
              channel={channel}
              onSubmit={handleSubmit}
              onCancel={() => router.push('/alert-channels')}
              loading={saving}
            />
          </FormCard>
        </div>

        <FormCard>
          <h3 className="text-sm font-medium text-white">Channel details</h3>
          <dl className="mt-4 space-y-4">
            <div>
              <dt className="text-xs font-medium text-slate-500">Channel ID</dt>
              <dd className="mt-1 break-all font-mono text-xs text-slate-300">{channel.id}</dd>
            </div>
            <div>
              <dt className="text-xs font-medium text-slate-500">Tenant ID</dt>
              <dd className="mt-1 break-all font-mono text-xs text-slate-300">{channel.tenant_id}</dd>
            </div>
            <div>
              <dt className="text-xs font-medium text-slate-500">Type</dt>
              <dd className="mt-1 text-sm text-slate-300">{channel.type.toUpperCase()}</dd>
            </div>
            <div>
              <dt className="text-xs font-medium text-slate-500">Status</dt>
              <dd className="mt-1">
                <Pill tone={channel.is_active ? 'success' : 'neutral'} size="xs" dot>
                  {channel.is_active ? 'Active' : 'Inactive'}
                </Pill>
              </dd>
            </div>
            <div>
              <dt className="text-xs font-medium text-slate-500">Created</dt>
              <dd className="mt-1 text-sm text-slate-300">
                {new Date(channel.created_at).toLocaleString()}
              </dd>
            </div>
            <div>
              <dt className="text-xs font-medium text-slate-500">Last updated</dt>
              <dd className="mt-1 text-sm text-slate-300">
                {new Date(channel.updated_at).toLocaleString()}
              </dd>
            </div>
          </dl>
        </FormCard>
      </div>

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
