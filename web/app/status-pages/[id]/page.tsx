'use client';

import { useRouter, useParams } from 'next/navigation';
import { useState, useEffect, useCallback } from 'react';
import { ArrowUpRight } from 'lucide-react';
import { StatusPage, UpdateStatusPageRequest } from '@/lib/types';
import { getStatusPage, updateStatusPage } from '@/lib/api';
import StatusPageForm from '@/components/status-pages/StatusPageForm';
import { resolveStatusPagePublicUrl } from '@/lib/statusPageUrl';
import PageHeader from '@/components/ui/PageHeader';
import FormCard from '@/components/ui/FormCard';
import CollapsibleSection from '@/components/ui/CollapsibleSection';
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
  const monitorCount = statusPage.sections?.reduce((count, section) => {
    return count + (section.monitors?.length || 0);
  }, 0) || statusPage.monitor_ids?.length || 0;

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

      <CollapsibleSection title="Page info" summary={publicUrl}>
        <div className="grid gap-4 md:grid-cols-3">
          <div>
            <h3 className="mb-2 text-xs font-medium uppercase tracking-wider text-slate-500">Public URL</h3>
            <div className="rounded-lg border border-white/[0.06] bg-slate-950/50 p-3">
              <code className="break-all text-xs text-cyan-300">{publicUrl}</code>
            </div>
          </div>

          <div>
            <h3 className="mb-2 text-xs font-medium uppercase tracking-wider text-slate-500">Details</h3>
            <div className="space-y-2 text-sm">
              <div className="flex justify-between gap-3">
                <span className="text-slate-500">ID</span>
                <span className="min-w-0 truncate font-mono text-xs text-slate-400">{statusPage.id}</span>
              </div>
              <div className="flex justify-between gap-3">
                <span className="text-slate-500">Monitors</span>
                <span className="text-slate-300">{monitorCount}</span>
              </div>
              <div className="flex justify-between gap-3">
                <span className="text-slate-500">Created</span>
                <span className="text-xs text-slate-300">
                  {new Date(statusPage.created_at).toLocaleDateString()}
                </span>
              </div>
              <div className="flex justify-between gap-3">
                <span className="text-slate-500">Updated</span>
                <span className="text-xs text-slate-300">
                  {new Date(statusPage.updated_at).toLocaleDateString()}
                </span>
              </div>
            </div>
          </div>

          <div>
            <h3 className="mb-2 text-xs font-medium uppercase tracking-wider text-slate-500">Preview</h3>
            <div className="rounded-lg bg-[#0a0a0f] p-4">
              <div className="flex items-center gap-3">
                {statusPage.logo_url ? (
                  <img
                    src={statusPage.logo_url}
                    alt="Logo"
                    className="h-10 w-10 rounded-xl object-cover"
                  />
                ) : (
                  <div
                    className="flex h-10 w-10 items-center justify-center rounded-xl"
                    style={{
                      background: `linear-gradient(135deg, ${statusPage.primary_color || '#6366f1'} 0%, #06b6d4 100%)`,
                      boxShadow: '0 0 20px rgba(99, 102, 241, 0.3)',
                    }}
                  >
                    <div className="h-5 w-5 rounded-full border-2 border-white/20 bg-[#0a0a0f]" />
                  </div>
                )}
                <div className="min-w-0">
                  <div className="truncate text-sm font-semibold text-white">{statusPage.title}</div>
                  <div className="truncate text-xs text-slate-500">
                    {statusPage.description || 'System status'}
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </CollapsibleSection>

      <FormCard>
        <StatusPageForm
          statusPage={statusPage}
          onSubmit={handleSubmit}
          onCancel={() => router.push('/status-pages')}
          loading={saving}
        />
      </FormCard>

    </div>
  );
}
