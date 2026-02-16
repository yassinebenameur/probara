'use client';

import { useRouter } from 'next/navigation';
import Link from 'next/link';
import { useState } from 'react';
import { CreateAlertChannelRequest, UpdateAlertChannelRequest } from '@/lib/types';
import { createAlertChannel } from '@/lib/api';
import AlertChannelForm from '@/components/alert-channels/AlertChannelForm';
import Toast from '@/components/ui/Toast';

export default function NewAlertChannelPage() {
  const router = useRouter();
  const [loading, setLoading] = useState(false);
  const [toast, setToast] = useState<{ message: string; type: 'success' | 'error' } | null>(null);

  const handleSubmit = async (data: CreateAlertChannelRequest | UpdateAlertChannelRequest) => {
    if (!('type' in data) || !data.type) {
      setToast({ message: 'Channel type is required', type: 'error' });
      return;
    }

    try {
      setLoading(true);
      await createAlertChannel(data as CreateAlertChannelRequest);
      setToast({ message: 'Alert channel created successfully', type: 'success' });
      setTimeout(() => {
        router.push('/alert-channels');
      }, 1000);
    } catch (err: any) {
      setToast({ message: err.message || 'Failed to create alert channel', type: 'error' });
      setLoading(false);
    }
  };

  return (
    <div className="max-w-2xl">
      <div className="mb-6">
        <div className="flex items-center gap-2 text-xs text-slate-500">
          <Link href="/alert-channels" className="hover:text-slate-400">Alert Channels</Link>
          <svg className="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 5l7 7-7 7" />
          </svg>
          <span className="text-slate-400">New Alert Channel</span>
        </div>
        <h1 className="mt-3 text-xl font-semibold text-white">Create Alert Channel</h1>
        <p className="mt-1 text-sm text-slate-500">
          Configure where alerts should be delivered for your team.
        </p>
      </div>

      <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-6">
        <AlertChannelForm
          onSubmit={handleSubmit}
          onCancel={() => router.push('/alert-channels')}
          loading={loading}
        />
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
