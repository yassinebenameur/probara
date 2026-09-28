'use client';

import { useRouter, useParams } from 'next/navigation';
import { useState, useEffect, useCallback } from 'react';
import { ArrowUpRight, Paintbrush } from 'lucide-react';
import { StatusPage, UpdateStatusPageRequest } from '@/lib/types';
import { getStatusPage, updateStatusPage } from '@/lib/api';
import StatusPageForm from '@/components/status-pages/StatusPageForm';
import { resolveStatusPagePublicUrl } from '@/lib/statusPageUrl';
import PageHeader from '@/components/ui/PageHeader';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import { useToast } from '@/components/ui/ToastProvider';

export default function EditStatusPagePage() {
  const router = useRouter();
  const params = useParams();
  const id = params.id as string;

  const [statusPage, setStatusPage] = useState<StatusPage | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string>('');
  const { showToast } = useToast();

  const loadStatusPage = useCallback(async () => {
    try {
      setLoading(true);
      setError('');
      const data = await getStatusPage(id);
      setStatusPage(data);
    } catch (err: any) {
      setError(err.message || 'Failed to load status page');
    } finally {
      setLoading(false);
    }
  }, [id]);

  useEffect(() => {
    loadStatusPage();
  }, [loadStatusPage]);

  const handleSubmit = async (data: UpdateStatusPageRequest) => {
    try {
      setSaving(true);
      const updated = await updateStatusPage(id, data);
      setStatusPage(updated);
      showToast('Status page updated', 'success');
    } catch (err: any) {
      showToast(err.message || 'Failed to update', 'error');
    } finally {
      setSaving(false);
    }
  };

  if (loading) {
    return (
      <div className="animate-pulse space-y-6">
        <div className="space-y-2">
          <div className="h-4 w-40 rounded-md bg-well-hover" />
          <div className="h-8 w-64 rounded-lg bg-well-hover" />
        </div>
        <div className="glass h-[590px]" />
        <div className="glass h-40" />
        <span className="sr-only">Loading status page…</span>
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
    <div className="space-y-6">
      <PageHeader
        breadcrumb={[{ label: 'Status pages', href: '/status-pages' }, { label: statusPage.title }]}
        title={statusPage.title}
        subtitle={<span className="font-mono text-[13px]">/{statusPage.slug}</span>}
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
        onCancel={() => router.push('/status-pages')}
        loading={saving}
      />
    </div>
  );
}
