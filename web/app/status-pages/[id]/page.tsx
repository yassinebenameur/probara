'use client';

import { useRouter, useParams } from 'next/navigation';
import { useState, useEffect, useCallback } from 'react';
import { ArrowUpRight } from 'lucide-react';
import { StatusPage, UpdateStatusPageRequest } from '@/lib/types';
import { getStatusPage, updateStatusPage } from '@/lib/api';
import StatusPageForm from '@/components/status-pages/StatusPageForm';
import { resolveStatusPagePublicUrl } from '@/lib/statusPageUrl';
import PageHeader from '@/components/ui/PageHeader';
import Button from '@/components/ui/Button';
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
      <div className="flex h-64 items-center justify-center">
        <div className="text-slate-500">Loading status page…</div>
      </div>
    );
  }

  if (error || !statusPage) {
    return (
      <div className="flex h-64 flex-col items-center justify-center gap-4">
        <p className="text-rose-400">{error || 'Status page not found'}</p>
        <Button variant="ghost" size="sm" onClick={() => router.push('/status-pages')}>
          ← Back to status pages
        </Button>
      </div>
    );
  }

  const publicUrl = resolveStatusPagePublicUrl(statusPage);

  return (
    <div className="space-y-6">
      <PageHeader
        breadcrumb={[{ label: 'Status pages', href: '/status-pages' }, { label: statusPage.title }]}
        title={statusPage.title}
        subtitle={`/${statusPage.slug}`}
        action={
          <Button
            variant="accent"
            size="sm"
            icon={<ArrowUpRight strokeWidth={1.75} />}
            asChild
          >
            <a href={publicUrl} target="_blank" rel="noopener noreferrer">
              Open public page
            </a>
          </Button>
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
