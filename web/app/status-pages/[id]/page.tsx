'use client';

import { useRouter, useParams } from 'next/navigation';
import { useState, useCallback } from 'react';
import { ArrowUpRight, Paintbrush } from 'lucide-react';
import { StatusPage, UpdateStatusPageRequest } from '@/lib/types';
import { getStatusPage, updateStatusPage } from '@/lib/api';
import StatusPageForm from '@/components/status-pages/StatusPageForm';
import { resolveStatusPagePublicUrl } from '@/lib/statusPageUrl';
import PageHeader from '@/components/ui/PageHeader';
import Page from '@/components/ui/Page';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import { useToast } from '@/components/ui/ToastProvider';
import useDocumentTitle from '@/lib/useDocumentTitle';
import { RelativeTime } from '@/components/ui/RelativeTime';
import { errorMessage } from '@/lib/errors';
import { useAsyncData } from '@/lib/useAsyncData';

export default function EditStatusPagePage() {
  const router = useRouter();
  const params = useParams();
  const id = params.id as string;

  const loadStatusPage = useCallback(() => getStatusPage(id), [id]);
  const { data: statusPage, setData: setStatusPage, loading, error } = useAsyncData<StatusPage | null>(
    loadStatusPage,
    null,
    'Failed to load status page'
  );
  useDocumentTitle(statusPage?.title);
  const [saving, setSaving] = useState(false);
  const { showToast } = useToast();

  const handleSubmit = async (data: UpdateStatusPageRequest) => {
    try {
      setSaving(true);
      const updated = await updateStatusPage(id, data);
      setStatusPage(updated);
      showToast('Status page updated', 'success');
    } catch (err) {
      showToast(errorMessage(err, 'Failed to update'), 'error');
    } finally {
      setSaving(false);
    }
  };

  if (loading) {
    return (
      <div className="mx-auto w-full max-w-[100rem] animate-pulse space-y-6" role="status" aria-label="Loading status page">
        <div className="space-y-2" aria-hidden="true">
          <div className="h-4 w-40 rounded-md bg-well-hover" />
          <div className="h-8 w-64 rounded-lg bg-well-hover" />
        </div>
        <div className="grid gap-6 xl:grid-cols-[minmax(0,1fr)_380px] 2xl:grid-cols-[minmax(0,1fr)_440px]" aria-hidden="true">
          <div className="space-y-6">
            <div className="glass h-[460px]" />
            <div className="glass h-64" />
          </div>
          <div className="glass hidden h-[520px] xl:block" />
        </div>
      </div>
    );
  }

  if (error || !statusPage) {
    return (
      <Callout
        tone="danger"
        action={
          <Button variant="ghost" size="xs" onClick={() => router.push('/status-pages')}>
            ← Back to status pages
          </Button>
        }
      >
        {error || 'Status page not found'}
      </Callout>
    );
  }

  const publicUrl = resolveStatusPagePublicUrl(statusPage);

  return (
    <Page width="detail">
      <PageHeader
        breadcrumb={[{ label: 'Status pages', href: '/status-pages' }, { label: statusPage.title }]}
        title={statusPage.title}
        subtitle={[
          <span key="slug" className="font-mono text-ui">/{statusPage.slug}</span>,
          <RelativeTime key="updated" prefix="Updated " value={statusPage.updated_at} />,
        ]}
        action={
          <>
            <Button
              variant="ghost"
              size="sm"
              icon={<Paintbrush strokeWidth={1.75} />}
              onClick={() => router.push(`/status-pages/${id}/template`)}
            >
              Customize template
            </Button>
            <Button variant="ghost" size="sm" icon={<ArrowUpRight strokeWidth={1.75} />} asChild>
              <a href={publicUrl} target="_blank" rel="noopener noreferrer">
                Open public page
              </a>
            </Button>
          </>
        }
      />

      <StatusPageForm
        statusPage={statusPage}
        onSubmit={handleSubmit}
        loading={saving}
      />
    </Page>
  );
}
