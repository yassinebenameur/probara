'use client';

import { useRouter, useParams } from 'next/navigation';
import Link from 'next/link';
import { useState, useEffect } from 'react';
import { AlertChannel, CreateAlertChannelRequest, UpdateAlertChannelRequest } from '@/lib/types';
import { getAlertChannel, updateAlertChannel } from '@/lib/api';
import AlertChannelForm from '@/components/alert-channels/AlertChannelForm';
import Toast from '@/components/ui/Toast';

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
        <div className="text-slate-500">Loading alert channel...</div>
      </div>
    );
  }

  if (error || !channel) {
    return (
      <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-6">
        <div className="rounded-lg border border-rose-500/20 bg-rose-500/10 px-4 py-3 text-sm text-rose-400">
          {error || 'Alert channel not found'}
        </div>
      </div>
    );
  }

  return (
    <div className="max-w-5xl">
      <div className="mb-6">
        <div className="flex items-center gap-2 text-xs text-slate-500">
          <Link href="/alert-channels" className="hover:text-slate-400">Alert Channels</Link>
          <svg className="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 5l7 7-7 7" />
          </svg>
          <span className="text-slate-400">Edit</span>
        </div>
        <h1 className="mt-3 text-xl font-semibold text-white">Edit Alert Channel</h1>
        <p className="mt-1 text-sm text-slate-500">
          Update delivery settings and recipients for this channel.
        </p>
      </div>

      <div className="grid grid-cols-1 gap-6 lg:grid-cols-3">
        <div className="lg:col-span-2">
          <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-6">
            <AlertChannelForm
              channel={channel}
              onSubmit={handleSubmit}
              onCancel={() => router.push('/alert-channels')}
              loading={saving}
            />
          </div>
        </div>

        <div>
          <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-6">
            <h3 className="text-sm font-medium text-white">Channel Details</h3>
            <dl className="space-y-4">
              <div>
                <dt className="text-xs font-medium text-slate-500">Channel ID</dt>
                <dd className="mt-1 text-sm font-mono text-white">{channel.id}</dd>
              </div>
              <div>
                <dt className="text-xs font-medium text-slate-500">Tenant ID</dt>
                <dd className="mt-1 text-sm font-mono text-white">{channel.tenant_id}</dd>
              </div>
              <div>
                <dt className="text-xs font-medium text-slate-500">Type</dt>
                <dd className="mt-1 text-sm text-slate-200">{channel.type.toUpperCase()}</dd>
              </div>
              <div>
                <dt className="text-xs font-medium text-slate-500">Status</dt>
                <dd className="mt-1">
                  <span className={channel.is_active ? 'badge badge-success' : 'badge badge-default'}>
                    {channel.is_active ? 'Active' : 'Inactive'}
                  </span>
                </dd>
              </div>
              <div>
                <dt className="text-xs font-medium text-slate-500">Created</dt>
                <dd className="mt-1 text-sm text-slate-200">
                  {new Date(channel.created_at).toLocaleString()}
                </dd>
              </div>
              <div>
                <dt className="text-xs font-medium text-slate-500">Last Updated</dt>
                <dd className="mt-1 text-sm text-slate-200">
                  {new Date(channel.updated_at).toLocaleString()}
                </dd>
              </div>
            </dl>
          </div>
        </div>
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
