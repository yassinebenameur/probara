'use client';

import { useRouter } from 'next/navigation';
import { useState } from 'react';
import { X } from 'lucide-react';
import { CreateStatusPageRequest, UpdateStatusPageRequest } from '@/lib/types';
import { createStatusPage } from '@/lib/api';
import StatusPageForm from '@/components/status-pages/StatusPageForm';
import PageHeader from '@/components/ui/PageHeader';
import FormCard from '@/components/ui/FormCard';

export default function NewStatusPagePage() {
  const router = useRouter();
  const [loading, setLoading] = useState(false);
  const [toast, setToast] = useState<{ message: string; type: 'success' | 'error' } | null>(null);

  const handleSubmit = async (data: CreateStatusPageRequest | UpdateStatusPageRequest) => {
    try {
      setLoading(true);
      await createStatusPage(data as CreateStatusPageRequest);
      setToast({ message: 'Status page created successfully', type: 'success' });
      setTimeout(() => {
        router.push('/status-pages');
      }, 1000);
    } catch (err: any) {
      setToast({ message: err.message || 'Failed to create status page', type: 'error' });
      setLoading(false);
    }
  };

  return (
    <div className="max-w-6xl space-y-6">
      <PageHeader
        breadcrumb={[{ label: 'Status pages', href: '/status-pages' }, { label: 'New' }]}
        title="Create status page"
        subtitle="Set up a public page to display your service status."
      />

      <FormCard>
        <StatusPageForm
          onSubmit={handleSubmit}
          onCancel={() => router.push('/status-pages')}
          loading={loading}
        />
      </FormCard>

      {toast && (
        <div
          className={`fixed bottom-4 right-4 rounded-lg px-4 py-3 shadow-lg ${
            toast.type === 'success' ? 'bg-emerald-500' : 'bg-rose-500'
          }`}
        >
          <div className="flex items-center gap-3">
            <p className="text-sm text-white">{toast.message}</p>
            <button onClick={() => setToast(null)} className="text-white/80 hover:text-white" aria-label="Dismiss">
              <X className="h-4 w-4" strokeWidth={1.75} />
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
