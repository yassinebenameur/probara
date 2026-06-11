'use client';

import { useRouter, useSearchParams } from 'next/navigation';
import { Suspense, useState } from 'react';
import { CreateAlertChannelRequest, UpdateAlertChannelRequest } from '@/lib/types';
import { createAlertChannel } from '@/lib/api';
import AlertChannelForm from '@/components/alert-channels/AlertChannelForm';
import { useToast } from '@/components/ui/ToastProvider';
import PageHeader from '@/components/ui/PageHeader';
import FormCard from '@/components/ui/FormCard';

export default function NewAlertChannelPage() {
  return (
    <Suspense fallback={<p className="text-sm text-slate-400">Loading…</p>}>
      <NewAlertChannelPageInner />
    </Suspense>
  );
}

function NewAlertChannelPageInner() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const initialType = searchParams.get('type') || undefined;
  const [loading, setLoading] = useState(false);
  const { showToast } = useToast();

  const handleSubmit = async (data: CreateAlertChannelRequest | UpdateAlertChannelRequest) => {
    if (!('type' in data) || !data.type) {
      showToast('Channel type is required', 'error');
      return;
    }

    try {
      setLoading(true);
      await createAlertChannel(data as CreateAlertChannelRequest);
      showToast('Alert channel created successfully', 'success');
      setTimeout(() => {
        router.push('/alert-channels');
      }, 1000);
    } catch (err: any) {
      showToast(err.message || 'Failed to create alert channel', 'error');
      setLoading(false);
    }
  };

  return (
    <div className="max-w-2xl space-y-6">
      <PageHeader
        breadcrumb={[{ label: 'Alert channels', href: '/alert-channels' }, { label: 'New' }]}
        title="Create alert channel"
        subtitle="Configure where alerts should be delivered."
      />

      <FormCard>
        <AlertChannelForm
          initialType={initialType}
          onSubmit={handleSubmit}
          onCancel={() => router.push('/alert-channels')}
          loading={loading}
        />
      </FormCard>

    </div>
  );
}
