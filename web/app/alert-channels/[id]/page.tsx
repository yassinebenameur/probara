'use client';

import { useRouter, useParams } from 'next/navigation';
import { useState, useEffect } from 'react';
import { AlertChannel, CreateAlertChannelRequest, UpdateAlertChannelRequest } from '@/lib/types';
import { getAlertChannel, updateAlertChannel } from '@/lib/api';
import AlertChannelForm from '@/components/alert-channels/AlertChannelForm';
import { useToast } from '@/components/ui/ToastProvider';
import PageHeader from '@/components/ui/PageHeader';
import FormCard from '@/components/ui/FormCard';
import Panel from '@/components/ui/Panel';
import Pill from '@/components/ui/Pill';
import Callout from '@/components/ui/Callout';
import Page from '@/components/ui/Page';
import useDocumentTitle from '@/lib/useDocumentTitle';
import { formatDateTime, formatDateTimeFull } from '@/lib/format';

export default function EditAlertChannelPage() {
  const router = useRouter();
  const params = useParams();
  const id = params.id as string;

  const [channel, setChannel] = useState<AlertChannel | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string>('');
  const { showToast } = useToast();
  useDocumentTitle(channel?.name);

  useEffect(() => {
    loadChannel();
    // eslint-disable-next-line react-hooks/exhaustive-deps
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
      showToast('Alert channel updated successfully', 'success');
    } catch (err: any) {
      showToast(err.message || 'Failed to update alert channel', 'error');
    } finally {
      setSaving(false);
    }
  };

  if (loading) {
    return (
      <div className="flex h-64 items-center justify-center">
        <p className="text-sm text-slate-500">Loading alert channel…</p>
      </div>
    );
  }

  if (error || !channel) {
    return (
      <Callout tone="danger">{error || 'Alert channel not found'}</Callout>
    );
  }

  return (
    <Page width="reading">
      <PageHeader
        breadcrumb={[{ label: 'Settings', href: '/settings' }, { label: 'Notifications', href: '/settings?tab=channels' }, { label: channel.name }]}
        title="Edit alert channel"
        subtitle="Change where and how this channel delivers alerts."
      />

      <div className="grid grid-cols-1 items-start gap-6 lg:grid-cols-3">
        <div className="lg:col-span-2">
          <FormCard>
            <AlertChannelForm
              channel={channel}
              onSubmit={handleSubmit}
              onCancel={() => router.push('/settings?tab=channels')}
              loading={saving}
            />
          </FormCard>
        </div>

        <Panel title="Channel details">
          <dl className="space-y-3">
            <div>
              <dt className="text-xs text-slate-500">Channel ID</dt>
              <dd className="mt-0.5 break-all font-mono text-xs text-slate-300">{channel.id}</dd>
            </div>
            <div>
              <dt className="text-xs text-slate-500">Tenant ID</dt>
              <dd className="mt-0.5 break-all font-mono text-xs text-slate-300">{channel.tenant_id}</dd>
            </div>
            <div>
              <dt className="text-xs text-slate-500">Type</dt>
              <dd className="mt-0.5 font-mono text-xs text-slate-300">{channel.type}</dd>
            </div>
            <div>
              <dt className="text-xs text-slate-500">Status</dt>
              <dd className="mt-1">
                <Pill tone={channel.is_active ? 'success' : 'neutral'} size="xs" dot>
                  {channel.is_active ? 'Active' : 'Inactive'}
                </Pill>
              </dd>
            </div>
            <div>
              <dt className="text-xs text-slate-500">Created</dt>
              <dd className="mt-0.5 text-ui text-slate-300" title={formatDateTimeFull(channel.created_at)}>
                {formatDateTime(channel.created_at)}
              </dd>
            </div>
            <div>
              <dt className="text-xs text-slate-500">Last updated</dt>
              <dd className="mt-0.5 text-ui text-slate-300" title={formatDateTimeFull(channel.updated_at)}>
                {formatDateTime(channel.updated_at)}
              </dd>
            </div>
          </dl>
        </Panel>
      </div>
    </Page>
  );
}
