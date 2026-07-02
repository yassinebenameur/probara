'use client';

import { useRouter } from 'next/navigation';
import { useState } from 'react';
import { CreateStatusPageRequest, UpdateStatusPageRequest } from '@/lib/types';
import { createStatusPage } from '@/lib/api';
import StatusPageForm from '@/components/status-pages/StatusPageForm';
import PageHeader from '@/components/ui/PageHeader';
import { useToast } from '@/components/ui/ToastProvider';

export default function NewStatusPagePage() {
  const router = useRouter();
  const [loading, setLoading] = useState(false);
  const { showToast } = useToast();

  const handleSubmit = async (data: CreateStatusPageRequest | UpdateStatusPageRequest) => {
    try {
      setLoading(true);
      await createStatusPage(data as CreateStatusPageRequest);
      showToast('Status page created successfully', 'success');
      setTimeout(() => {
        router.push('/status-pages');
      }, 1000);
    } catch (err: any) {
      showToast(err.message || 'Failed to create status page', 'error');
      setLoading(false);
    }
  };

  return (
    <div className="space-y-6">
      <PageHeader
        breadcrumb={[{ label: 'Status pages', href: '/status-pages' }, { label: 'New' }]}
        title="Create status page"
        subtitle="Set up a public page to display your service status."
      />

      <StatusPageForm
        onSubmit={handleSubmit}
        onCancel={() => router.push('/status-pages')}
        loading={loading}
      />
    </div>
  );
}
