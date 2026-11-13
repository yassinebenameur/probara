'use client';

import { useRouter } from 'next/navigation';
import { useState } from 'react';
import { CreateStatusPageRequest, UpdateStatusPageRequest } from '@/lib/types';
import { createStatusPage } from '@/lib/api';
import StatusPageForm from '@/components/status-pages/StatusPageForm';
import PageHeader from '@/components/ui/PageHeader';
import Page from '@/components/ui/Page';
import { useToast } from '@/components/ui/ToastProvider';
import { errorMessage } from '@/lib/errors';

export default function NewStatusPagePage() {
  const router = useRouter();
  const [loading, setLoading] = useState(false);
  const { showToast } = useToast();

  const handleSubmit = async (data: CreateStatusPageRequest | UpdateStatusPageRequest) => {
    try {
      setLoading(true);
      const created = await createStatusPage(data as CreateStatusPageRequest);
      showToast('Status page created', 'success');
      // Straight into the editor: the template, and the page's public link,
      // live there.
      router.push(`/status-pages/${created.id}`);
    } catch (err) {
      showToast(errorMessage(err, 'Failed to create status page'), 'error');
      setLoading(false);
    }
  };

  return (
    <Page width="detail">
      <PageHeader
        breadcrumb={[{ label: 'Status pages', href: '/status-pages' }, { label: 'New' }]}
        title="New status page"
        subtitle="Name the page and arrange its monitors into sections; a custom template can follow once it exists."
      />

      <StatusPageForm
        onSubmit={handleSubmit}
        onCancel={() => router.push('/status-pages')}
        loading={loading}
      />
    </Page>
  );
}
