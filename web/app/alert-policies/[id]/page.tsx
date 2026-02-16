'use client';

import { useRouter, useParams } from 'next/navigation';
import { useState, useEffect } from 'react';
import { AlertChannel, AlertPolicy, CreateAlertPolicyRequest, UpdateAlertPolicyRequest } from '@/lib/types';
import { getAlertChannels, getAlertPolicy, updateAlertPolicy } from '@/lib/api';
import AlertPolicyForm from '@/components/alert-policies/AlertPolicyForm';
import Toast from '@/components/ui/Toast';
import Link from 'next/link';

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
        <div className="text-slate-500">Loading alert policy...</div>
      </div>
    );
  }

  if (error || !policy) {
    return (
      <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-6">
        <div className="rounded-lg border border-rose-500/20 bg-rose-500/10 px-4 py-3 text-sm text-rose-400">
          {error || 'Alert policy not found'}
        </div>
      </div>
    );
  }

  return (
    <div className="max-w-5xl">
      <div className="mb-6">
        <div className="flex items-center gap-2 text-xs text-slate-500">
          <Link href="/alert-policies" className="hover:text-slate-400">Alert Policies</Link>
          <svg className="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 5l7 7-7 7" />
          </svg>
          <span className="text-slate-400">Edit</span>
        </div>
        <h1 className="mt-3 text-xl font-semibold text-white">Edit Alert Policy</h1>
        <p className="mt-1 text-sm text-slate-500">
          Update alert thresholds and notification channels.
        </p>
      </div>

      <div className="grid grid-cols-1 gap-6 lg:grid-cols-3">
        <div className="lg:col-span-2">
          <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-6">
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
          </div>
        </div>

        <div>
          <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-6">
            <h3 className="text-sm font-medium text-white">Policy Details</h3>
            <dl className="space-y-4">
              <div>
                <dt className="text-xs font-medium text-slate-500">Policy ID</dt>
                <dd className="mt-1 text-sm font-mono text-white">{policy.id}</dd>
              </div>
              <div>
                <dt className="text-xs font-medium text-slate-500">Tenant ID</dt>
                <dd className="mt-1 text-sm font-mono text-white">{policy.tenant_id}</dd>
              </div>
              <div>
                <dt className="text-xs font-medium text-slate-500">Created</dt>
                <dd className="mt-1 text-sm text-slate-200">
                  {new Date(policy.created_at).toLocaleString()}
                </dd>
              </div>
              <div>
                <dt className="text-xs font-medium text-slate-500">Last Updated</dt>
                <dd className="mt-1 text-sm text-slate-200">
                  {new Date(policy.updated_at).toLocaleString()}
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
