'use client';

import { useRouter } from 'next/navigation';
import { AlertChannel, CreateAlertPolicyRequest, UpdateAlertPolicyRequest } from '@/lib/types';
import { createAlertPolicy, getAlertChannels } from '@/lib/api';
import AlertPolicyForm from '@/components/alert-policies/AlertPolicyForm';
import Toast from '@/components/ui/Toast';
import PageHeader from '@/components/ui/PageHeader';
import FormCard from '@/components/ui/FormCard';
import { useEffect, useState } from 'react';

export default function NewAlertPolicyPage() {
  const router = useRouter();
  const [loading, setLoading] = useState(false);
  const [toast, setToast] = useState<{ message: string; type: 'success' | 'error' } | null>(null);
  const [channels, setChannels] = useState<AlertChannel[]>([]);
  const [channelsError, setChannelsError] = useState<string>('');

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

  const handleSubmit = async (data: CreateAlertPolicyRequest | UpdateAlertPolicyRequest) => {
    if (!data.name || data.failure_threshold === undefined || data.failure_window_seconds === undefined) {
      setToast({ message: 'Name, threshold, and window are required', type: 'error' });
      return;
    }

    const payload: CreateAlertPolicyRequest = {
      name: data.name,
      description: data.description,
      failure_threshold: data.failure_threshold,
      failure_window_seconds: data.failure_window_seconds,
      channel_ids: data.channel_ids,
      email_subject_template: data.email_subject_template,
      email_body_template: data.email_body_template,
    };

    try {
      setLoading(true);
      await createAlertPolicy(payload);
      setToast({ message: 'Alert policy created successfully', type: 'success' });
      setTimeout(() => {
        router.push('/alert-policies');
      }, 1000);
    } catch (err: any) {
      setToast({ message: err.message || 'Failed to create alert policy', type: 'error' });
      setLoading(false);
    }
  };

  return (
    <div className="max-w-2xl space-y-6">
      <PageHeader
        breadcrumb={[{ label: 'Alert policies', href: '/alert-policies' }, { label: 'New' }]}
        title="Create alert policy"
        subtitle="Define alert conditions and choose notification channels."
      />

      <FormCard>
        <AlertPolicyForm
          onSubmit={handleSubmit}
          onCancel={() => router.push('/alert-policies')}
          loading={loading}
          channels={channels}
        />
        {channelsError && (
          <div className="mt-4 text-sm text-rose-500">{channelsError}</div>
        )}
      </FormCard>

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
