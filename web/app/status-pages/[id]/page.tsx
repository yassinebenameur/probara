'use client';

import { useRouter, useParams } from 'next/navigation';
import { useState, useEffect, useCallback } from 'react';
import Link from 'next/link';
import { StatusPage, UpdateStatusPageRequest } from '@/lib/types';
import { getStatusPage, updateStatusPage } from '@/lib/api';
import StatusPageForm from '@/components/status-pages/StatusPageForm';
import { resolveStatusPagePublicUrl } from '@/lib/statusPageUrl';

export default function EditStatusPagePage() {
  const router = useRouter();
  const params = useParams();
  const id = params.id as string;

  const [statusPage, setStatusPage] = useState<StatusPage | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string>('');
  const [toast, setToast] = useState<{ message: string; type: 'success' | 'error' } | null>(null);

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
      setToast({ message: 'Status page updated', type: 'success' });
    } catch (err: any) {
      setToast({ message: err.message || 'Failed to update', type: 'error' });
    } finally {
      setSaving(false);
    }
  };

  if (loading) {
    return (
      <div className="flex items-center justify-center h-64">
        <div className="text-slate-500">Loading status page...</div>
      </div>
    );
  }

  if (error || !statusPage) {
    return (
      <div className="flex flex-col items-center justify-center h-64">
        <p className="text-rose-400 mb-4">{error || 'Status page not found'}</p>
        <Link href="/status-pages">
          <button className="btn btn-secondary btn-sm">← Back to Status Pages</button>
        </Link>
      </div>
    );
  }

  const publicUrl = resolveStatusPagePublicUrl(statusPage);
  const monitorCount = statusPage.sections?.reduce((count, section) => {
    return count + (section.monitors?.length || 0);
  }, 0) || statusPage.monitor_ids?.length || 0;

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-start justify-between">
        <div>
          <div className="flex items-center gap-2 text-xs text-slate-500 mb-3">
            <Link href="/status-pages" className="hover:text-slate-400">Status Pages</Link>
            <svg className="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 5l7 7-7 7" />
            </svg>
            <span className="text-slate-400">{statusPage.title}</span>
          </div>
          <h1 className="text-xl font-semibold text-white">{statusPage.title}</h1>
          <p className="mt-1 text-sm text-slate-500 font-mono">/{statusPage.slug}</p>
        </div>
      </div>

      {/* Content Grid */}
      <div className="grid gap-6 lg:grid-cols-[1fr_340px]">
        {/* Form */}
        <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-6">
          <StatusPageForm
            statusPage={statusPage}
            onSubmit={handleSubmit}
            onCancel={() => router.push('/status-pages')}
            loading={saving}
          />
        </div>

        {/* Sidebar */}
        <div className="space-y-4">
          {/* Public URL */}
          <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-5">
            <h3 className="text-xs font-medium uppercase tracking-wider text-slate-500 mb-3">Public URL</h3>
            <div className="rounded-lg border border-white/[0.06] bg-slate-950/50 p-3 mb-3">
              <code className="text-xs text-cyan-400 break-all">{publicUrl}</code>
            </div>
            <a
              href={publicUrl}
              target="_blank"
              rel="noopener noreferrer"
              className="btn btn-primary btn-sm w-full justify-center"
            >
              <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M10 6H6a2 2 0 00-2 2v10a2 2 0 002 2h10a2 2 0 002-2v-4M14 4h6m0 0v6m0-6L10 14" />
              </svg>
              Open Public Page
            </a>
          </div>

          {/* Details */}
          <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-5">
            <h3 className="text-xs font-medium uppercase tracking-wider text-slate-500 mb-3">Details</h3>
            <div className="space-y-3 text-sm">
              <div className="flex justify-between">
                <span className="text-slate-500">ID</span>
                <span className="text-slate-400 font-mono text-xs truncate max-w-[180px]">{statusPage.id}</span>
              </div>
              <div className="flex justify-between">
                <span className="text-slate-500">Monitors</span>
                <span className="text-slate-300">{monitorCount}</span>
              </div>
              <div className="flex justify-between">
                <span className="text-slate-500">Created</span>
                <span className="text-slate-300 text-xs">
                  {new Date(statusPage.created_at).toLocaleDateString()}
                </span>
              </div>
              <div className="flex justify-between">
                <span className="text-slate-500">Updated</span>
                <span className="text-slate-300 text-xs">
                  {new Date(statusPage.updated_at).toLocaleDateString()}
                </span>
              </div>
            </div>
          </div>

          {/* Preview */}
          <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-5">
            <h3 className="text-xs font-medium uppercase tracking-wider text-slate-500 mb-3">Preview</h3>
            <div className="rounded-lg p-4 bg-[#0a0a0f]">
              <div className="flex items-center gap-3">
                {statusPage.logo_url ? (
                  <img 
                    src={statusPage.logo_url} 
                    alt="Logo" 
                    className="h-10 w-10 rounded-xl object-cover"
                  />
                ) : (
                  <div 
                    className="h-10 w-10 rounded-xl flex items-center justify-center"
                    style={{ 
                      background: `linear-gradient(135deg, ${statusPage.primary_color || '#6366f1'} 0%, #06b6d4 100%)`,
                      boxShadow: '0 0 20px rgba(99, 102, 241, 0.3)'
                    }}
                  >
                    <div className="w-5 h-5 rounded-full bg-[#0a0a0f] border-2 border-white/20" />
                  </div>
                )}
                <div>
                  <div className="text-sm font-semibold text-white">{statusPage.title}</div>
                  <div className="text-xs text-slate-500">
                    {statusPage.description || 'System Status'}
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
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
