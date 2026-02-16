'use client';

import { useRouter } from 'next/navigation';
import Link from 'next/link';
import { CreateMonitorRequest } from '@/lib/types';
import { createMonitor } from '@/lib/api';
import MonitorForm from '@/components/monitors/MonitorForm';
import { useState } from 'react';

export default function NewMonitorPage() {
  const router = useRouter();
  const [loading, setLoading] = useState(false);
  const [toast, setToast] = useState<{ message: string; type: 'success' | 'error' } | null>(null);

  const handleSubmit = async (data: CreateMonitorRequest) => {
    try {
      setLoading(true);
      const created = await createMonitor(data);
      setToast({ message: 'Monitor created successfully', type: 'success' });
      setTimeout(() => {
        router.push(`/monitors/${created.id}`);
      }, 1000);
    } catch (err: any) {
      setToast({ message: err.message || 'Failed to create monitor', type: 'error' });
      setLoading(false);
    }
  };

  return (
    <div className="max-w-2xl">
      {/* Breadcrumb */}
      <div className="mb-6">
        <div className="flex items-center gap-2 text-xs text-slate-500">
          <Link href="/monitors" className="hover:text-slate-400">Monitors</Link>
          <svg className="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 5l7 7-7 7" />
          </svg>
          <span className="text-slate-400">New Monitor</span>
        </div>
        <h1 className="mt-3 text-xl font-semibold text-white">Create Monitor</h1>
        <p className="mt-1 text-sm text-slate-500">
          Set up a new monitor to track your service availability
        </p>
      </div>

      {/* Form Card */}
      <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-6">
        <MonitorForm
          onSubmit={handleSubmit}
          onCancel={() => router.push('/monitors')}
          loading={loading}
        />
      </div>

      {/* Toast */}
      {toast && (
        <div className={`fixed bottom-4 right-4 rounded-lg px-4 py-3 shadow-lg ${
          toast.type === 'success' ? 'bg-emerald-500' : 'bg-rose-500'
        }`}>
          <div className="flex items-center gap-3">
            <p className="text-sm text-white">{toast.message}</p>
            <button onClick={() => setToast(null)} className="text-white/80 hover:text-white">
              <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
              </svg>
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
