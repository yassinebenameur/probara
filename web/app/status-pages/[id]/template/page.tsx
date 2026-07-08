'use client';

import { useRouter, useParams } from 'next/navigation';
import { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import {
  ArrowUpRight,
  Download,
  Upload,
  RefreshCw,
  Save,
  Rocket,
  RotateCcw,
  Trash2,
  FileCode,
} from 'lucide-react';
import {
  StatusPage,
  StatusPageTemplateState,
  StatusPageSettings,
  StatusPageLibraryTemplate,
} from '@/lib/types';
import {
  getStatusPage,
  updateStatusPage,
  getStatusPageTemplate,
  saveStatusPageTemplateDraft,
  discardStatusPageTemplateDraft,
  publishStatusPageTemplate,
  revertStatusPageTemplate,
  resetStatusPageTemplate,
  getStatusPageDefaultTemplateSource,
  getStatusPageTemplateVersionSource,
  listLibraryTemplates,
  createLibraryTemplate,
  getLibraryTemplateSource,
} from '@/lib/api';
import {
  resolveStatusPagePublicUrl,
  resolveStatusPageDraftPreviewUrl,
} from '@/lib/statusPageUrl';
import PageHeader from '@/components/ui/PageHeader';
import CollapsibleSection from '@/components/ui/CollapsibleSection';
import Button from '@/components/ui/Button';
import { useToast } from '@/components/ui/ToastProvider';

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  return `${(bytes / 1024).toFixed(1)} KB`;
}

export default function StatusPageTemplatePage() {
  const router = useRouter();
  const params = useParams();
  const id = params.id as string;
  const { showToast } = useToast();

  const [statusPage, setStatusPage] = useState<StatusPage | null>(null);
  const [templateState, setTemplateState] = useState<StatusPageTemplateState | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  const [source, setSource] = useState('');
  const [savedDraftSource, setSavedDraftSource] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [previewNonce, setPreviewNonce] = useState(0);

  const [brandingCSS, setBrandingCSS] = useState('');
  const [brandingHead, setBrandingHead] = useState('');
  const [brandingFooter, setBrandingFooter] = useState('');
  const [savingBranding, setSavingBranding] = useState(false);

  const [libraryItems, setLibraryItems] = useState<StatusPageLibraryTemplate[]>([]);

  const fileInputRef = useRef<HTMLInputElement>(null);

  const applySettings = (settings?: StatusPageSettings) => {
    setBrandingCSS(settings?.custom_css || '');
    setBrandingHead(settings?.custom_head_html || '');
    setBrandingFooter(settings?.custom_footer_html || '');
  };

  const load = useCallback(async () => {
    try {
      setLoading(true);
      setError('');
      const [page, template] = await Promise.all([
        getStatusPage(id),
        getStatusPageTemplate(id),
      ]);
      setStatusPage(page);
      setTemplateState(template);
      applySettings(page.settings);

      // Seed the editor: draft first, then the published version, then the
      // built-in default as the starting point.
      if (template.draft_source != null) {
        setSource(template.draft_source);
        setSavedDraftSource(template.draft_source);
      } else if (template.published_version != null) {
        const published = await getStatusPageTemplateVersionSource(
          id,
          template.published_version
        );
        setSource(published);
        setSavedDraftSource(null);
      } else {
        const fallback = await getStatusPageDefaultTemplateSource(id);
        setSource(fallback);
        setSavedDraftSource(null);
      }
    } catch (err: any) {
      setError(err.message || 'Failed to load template');
    } finally {
      setLoading(false);
    }
  }, [id]);

  useEffect(() => {
    load();
  }, [load]);

  useEffect(() => {
    listLibraryTemplates()
      .then((result) => setLibraryItems(result.items))
      .catch(() => {
        // Library is optional context; the editor works without it.
      });
  }, []);

  const dirty = savedDraftSource === null || source !== savedDraftSource;
  const hasDraft = templateState?.draft_source != null;

  const previewUrl = useMemo(() => {
    if (!statusPage) return '';
    const base = resolveStatusPageDraftPreviewUrl(
      statusPage,
      templateState?.preview_token
    );
    const separator = base.includes('?') ? '&' : '?';
    return `${base}${separator}_=${previewNonce}`;
  }, [statusPage, templateState?.preview_token, previewNonce]);

  const runAction = async (
    action: () => Promise<StatusPageTemplateState>,
    successMessage: string
  ): Promise<StatusPageTemplateState | null> => {
    try {
      setBusy(true);
      const state = await action();
      setTemplateState(state);
      showToast(successMessage, 'success');
      setPreviewNonce((n) => n + 1);
      return state;
    } catch (err: any) {
      showToast(err.message || 'Operation failed', 'error');
      return null;
    } finally {
      setBusy(false);
    }
  };

  const handleSaveDraft = async () => {
    const state = await runAction(
      () => saveStatusPageTemplateDraft(id, source),
      'Draft saved'
    );
    if (state) {
      setSavedDraftSource(source);
    }
    return state;
  };

  const handlePublish = async () => {
    if (
      !window.confirm(
        'Publish this template? The public status page will render with it immediately.'
      )
    ) {
      return;
    }
    if (dirty) {
      try {
        setBusy(true);
        await saveStatusPageTemplateDraft(id, source);
        setSavedDraftSource(source);
      } catch (err: any) {
        showToast(err.message || 'Draft is invalid; fix it before publishing', 'error');
        setBusy(false);
        return;
      }
    }
    await runAction(() => publishStatusPageTemplate(id), 'Template published');
  };

  const handleDiscardDraft = async () => {
    if (!window.confirm('Discard the draft? Unpublished changes will be lost.')) {
      return;
    }
    const state = await runAction(
      () => discardStatusPageTemplateDraft(id),
      'Draft discarded'
    );
    if (state) {
      await load();
    }
  };

  const handleReset = async () => {
    if (
      !window.confirm(
        'Reset to the built-in template? The public page stops using your custom template (versions stay available for revert).'
      )
    ) {
      return;
    }
    await runAction(() => resetStatusPageTemplate(id), 'Reverted to the built-in template');
  };

  const handleRevert = async (version: number) => {
    if (!window.confirm(`Republish version ${version}?`)) {
      return;
    }
    await runAction(
      () => revertStatusPageTemplate(id, version),
      `Version ${version} republished`
    );
  };

  const handleLoadDefault = async () => {
    try {
      setBusy(true);
      setSource(await getStatusPageDefaultTemplateSource(id));
      showToast('Built-in template loaded into the editor', 'success');
    } catch (err: any) {
      showToast(err.message || 'Failed to load the built-in template', 'error');
    } finally {
      setBusy(false);
    }
  };

  const handleLoadVersion = async (version: number) => {
    try {
      setBusy(true);
      setSource(await getStatusPageTemplateVersionSource(id, version));
      showToast(`Version ${version} loaded into the editor`, 'success');
    } catch (err: any) {
      showToast(err.message || 'Failed to load version', 'error');
    } finally {
      setBusy(false);
    }
  };

  const handleApplyFromLibrary = async (templateId: string) => {
    if (!templateId) return;
    const item = libraryItems.find((t) => t.id === templateId);
    try {
      setBusy(true);
      setSource(await getLibraryTemplateSource(templateId));
      showToast(
        `“${item?.name || 'Template'}” loaded into the editor — save as draft to keep it`,
        'success'
      );
    } catch (err: any) {
      showToast(err.message || 'Failed to load library template', 'error');
    } finally {
      setBusy(false);
    }
  };

  const handleSaveToLibrary = async () => {
    const templateName = window.prompt('Save current editor content to the library as:');
    if (!templateName?.trim()) return;
    try {
      setBusy(true);
      const created = await createLibraryTemplate({ name: templateName, source });
      setLibraryItems((prev) =>
        [...prev, created].sort((a, b) => a.name.localeCompare(b.name))
      );
      showToast(`Saved to library as “${created.name}”`, 'success');
    } catch (err: any) {
      showToast(err.message || 'Failed to save to library', 'error');
    } finally {
      setBusy(false);
    }
  };

  const handleDownload = () => {
    const blob = new Blob([source], { type: 'text/plain;charset=utf-8' });
    const url = URL.createObjectURL(blob);
    const link = document.createElement('a');
    link.href = url;
    link.download = `${statusPage?.slug || 'status-page'}-template.gohtml`;
    link.click();
    URL.revokeObjectURL(url);
  };

  const handleUpload = async (event: React.ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    event.target.value = '';
    if (!file) return;
    setSource(await file.text());
    showToast(`Loaded ${file.name} into the editor — save as draft to keep it`, 'success');
  };

  const handleSaveBranding = async () => {
    try {
      setSavingBranding(true);
      const updated = await updateStatusPage(id, {
        settings: {
          custom_css: brandingCSS,
          custom_head_html: brandingHead,
          custom_footer_html: brandingFooter,
        },
      });
      setStatusPage(updated);
      applySettings(updated.settings);
      showToast('Branding saved — the public page picks it up immediately', 'success');
    } catch (err: any) {
      showToast(err.message || 'Failed to save branding', 'error');
    } finally {
      setSavingBranding(false);
    }
  };

  if (loading) {
    return (
      <div className="flex h-64 items-center justify-center">
        <div className="text-slate-500">Loading template…</div>
      </div>
    );
  }

  if (error || !statusPage) {
    return (
      <div className="flex h-64 flex-col items-center justify-center gap-4">
        <p className="text-rose-400">{error || 'Status page not found'}</p>
        <Button variant="ghost" size="sm" onClick={() => router.push(`/status-pages/${id}`)}>
          ← Back to status page
        </Button>
      </div>
    );
  }

  const publicUrl = resolveStatusPagePublicUrl(statusPage);
  const activeLabel = templateState?.has_custom
    ? `Custom template v${templateState.published_version}`
    : 'Built-in template';
  const maxSize = templateState?.max_size_bytes || 512 * 1024;
  const historyVersions = (templateState?.versions || []).filter(
    (v) => v.status !== 'draft'
  );

  return (
    <div className="space-y-6">
      <input
        ref={fileInputRef}
        type="file"
        accept=".html,.gohtml,.tmpl,.txt"
        className="hidden"
        onChange={handleUpload}
      />

      <PageHeader
        breadcrumb={[
          { label: 'Status pages', href: '/status-pages' },
          { label: statusPage.title, href: `/status-pages/${id}` },
          { label: 'Template' },
        ]}
        title="Page template"
        subtitle={`Live: ${activeLabel}${hasDraft ? ' · unpublished draft' : ''}`}
        action={
          <Button variant="accent" size="sm" icon={<ArrowUpRight strokeWidth={1.75} />} asChild>
            <a href={publicUrl} target="_blank" rel="noopener noreferrer">
              Open public page
            </a>
          </Button>
        }
      />

      <CollapsibleSection
        title="Quick branding (CSS & HTML injection)"
        summary="Custom CSS, extra <head> markup, and footer HTML applied to the built-in template"
      >
        <div className="space-y-4">
          <p className="text-sm text-slate-500">
            The lightweight way to make the page match your brand: these snippets are injected
            into the built-in template (and available to custom templates as{' '}
            <code className="text-xs text-cyan-300">.CustomCSS</code>,{' '}
            <code className="text-xs text-cyan-300">.CustomHeadHTML</code>,{' '}
            <code className="text-xs text-cyan-300">.CustomFooterHTML</code>).
          </p>
          <div className="grid gap-4 lg:grid-cols-3">
            <div>
              <label className="mb-2 block text-xs font-medium uppercase tracking-wider text-slate-500">
                Custom CSS
              </label>
              <textarea
                value={brandingCSS}
                onChange={(e) => setBrandingCSS(e.target.value)}
                rows={8}
                spellCheck={false}
                placeholder={':root { --brand-primary: #6366f1; }\n.header { … }'}
                className="w-full rounded-lg border border-white/[0.06] bg-slate-950/50 p-3 font-mono text-xs text-slate-200 placeholder:text-slate-600 focus:border-cyan-500/40 focus:outline-none"
              />
            </div>
            <div>
              <label className="mb-2 block text-xs font-medium uppercase tracking-wider text-slate-500">
                Extra &lt;head&gt; HTML
              </label>
              <textarea
                value={brandingHead}
                onChange={(e) => setBrandingHead(e.target.value)}
                rows={8}
                spellCheck={false}
                placeholder={'<link rel="icon" href="…">\n<meta property="og:image" content="…">'}
                className="w-full rounded-lg border border-white/[0.06] bg-slate-950/50 p-3 font-mono text-xs text-slate-200 placeholder:text-slate-600 focus:border-cyan-500/40 focus:outline-none"
              />
            </div>
            <div>
              <label className="mb-2 block text-xs font-medium uppercase tracking-wider text-slate-500">
                Footer HTML
              </label>
              <textarea
                value={brandingFooter}
                onChange={(e) => setBrandingFooter(e.target.value)}
                rows={8}
                spellCheck={false}
                placeholder={'<div class="links">…</div>'}
                className="w-full rounded-lg border border-white/[0.06] bg-slate-950/50 p-3 font-mono text-xs text-slate-200 placeholder:text-slate-600 focus:border-cyan-500/40 focus:outline-none"
              />
            </div>
          </div>
          <div className="flex justify-end">
            <Button variant="accent" size="sm" onClick={handleSaveBranding} loading={savingBranding}>
              Save branding
            </Button>
          </div>
        </div>
      </CollapsibleSection>

      <div className="rounded-xl border border-white/[0.06] bg-slate-900/40">
        <div className="flex flex-wrap items-center justify-between gap-3 border-b border-white/[0.06] px-4 py-3">
          <div className="flex items-center gap-2 text-sm text-slate-300">
            <FileCode className="h-4 w-4 text-cyan-400" strokeWidth={1.75} />
            <span className="font-medium">Full template editor</span>
            <span className="text-xs text-slate-500">
              Go html/template · {formatBytes(source.length)} / {formatBytes(maxSize)}
              {dirty ? ' · unsaved changes' : ''}
            </span>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            {libraryItems.length > 0 && (
              <select
                value=""
                onChange={(e) => handleApplyFromLibrary(e.target.value)}
                disabled={busy}
                className="rounded-[12px] border border-white/10 bg-white/[0.04] px-3 py-2 text-xs text-slate-200 focus:border-cyan-500/40 focus:outline-none"
              >
                <option value="" disabled>
                  Apply from library…
                </option>
                {libraryItems.map((item) => (
                  <option key={item.id} value={item.id}>
                    {item.name}
                  </option>
                ))}
              </select>
            )}
            <Button variant="ghost" size="sm" onClick={handleSaveToLibrary} disabled={busy}>
              Save to library
            </Button>
            <Button
              variant="ghost"
              size="sm"
              onClick={() => router.push('/status-pages/templates')}
            >
              Manage library
            </Button>
            <Button variant="ghost" size="sm" onClick={handleLoadDefault} disabled={busy}>
              Load built-in
            </Button>
            <Button
              variant="ghost"
              size="sm"
              icon={<Upload strokeWidth={1.75} />}
              onClick={() => fileInputRef.current?.click()}
              disabled={busy}
            >
              Upload file
            </Button>
            <Button
              variant="ghost"
              size="sm"
              icon={<Download strokeWidth={1.75} />}
              onClick={handleDownload}
              disabled={busy}
            >
              Download
            </Button>
          </div>
        </div>

        <div className="grid gap-0 xl:grid-cols-2">
          <textarea
            value={source}
            onChange={(e) => setSource(e.target.value)}
            spellCheck={false}
            className="min-h-[560px] w-full resize-y border-0 bg-slate-950/60 p-4 font-mono text-xs leading-5 text-slate-200 focus:outline-none xl:border-r xl:border-white/[0.06]"
          />
          <div className="flex min-h-[560px] flex-col">
            <div className="flex items-center justify-between border-b border-white/[0.06] px-4 py-2">
              <span className="text-xs font-medium uppercase tracking-wider text-slate-500">
                Draft preview (live data)
              </span>
              <div className="flex items-center gap-2">
                <Button
                  variant="ghost"
                  size="sm"
                  icon={<RefreshCw strokeWidth={1.75} />}
                  onClick={() => setPreviewNonce((n) => n + 1)}
                >
                  Refresh
                </Button>
                <Button variant="ghost" size="sm" asChild>
                  <a href={previewUrl} target="_blank" rel="noopener noreferrer">
                    Open
                  </a>
                </Button>
              </div>
            </div>
            {hasDraft || previewNonce > 0 ? (
              <iframe
                key={previewNonce}
                src={previewUrl}
                title="Draft template preview"
                className="min-h-0 w-full flex-1 bg-white"
                sandbox="allow-scripts allow-same-origin"
              />
            ) : (
              <div className="flex flex-1 items-center justify-center p-6 text-center text-sm text-slate-500">
                Save a draft to preview it here against the page&apos;s live data.
              </div>
            )}
          </div>
        </div>

        <div className="flex flex-wrap items-center justify-between gap-3 border-t border-white/[0.06] px-4 py-3">
          <div className="flex flex-wrap items-center gap-2">
            {hasDraft && (
              <Button
                variant="ghost"
                size="sm"
                icon={<Trash2 strokeWidth={1.75} />}
                onClick={handleDiscardDraft}
                disabled={busy}
              >
                Discard draft
              </Button>
            )}
            {templateState?.has_custom && (
              <Button
                variant="ghost"
                size="sm"
                icon={<RotateCcw strokeWidth={1.75} />}
                onClick={handleReset}
                disabled={busy}
              >
                Reset to built-in
              </Button>
            )}
          </div>
          <div className="flex items-center gap-2">
            <Button
              variant="ghost"
              size="sm"
              icon={<Save strokeWidth={1.75} />}
              onClick={handleSaveDraft}
              loading={busy}
              disabled={!dirty && hasDraft}
            >
              Save draft
            </Button>
            <Button
              variant="accent"
              size="sm"
              icon={<Rocket strokeWidth={1.75} />}
              onClick={handlePublish}
              disabled={busy}
            >
              Publish
            </Button>
          </div>
        </div>
      </div>

      {historyVersions.length > 0 && (
        <CollapsibleSection
          title="Version history"
          summary={`${historyVersions.length} version${historyVersions.length === 1 ? '' : 's'}`}
        >
          <div className="divide-y divide-white/[0.06]">
            {historyVersions.map((v) => (
              <div
                key={`${v.version}-${v.created_at}`}
                className="flex flex-wrap items-center justify-between gap-3 py-3"
              >
                <div className="flex items-center gap-3">
                  <span className="font-mono text-sm text-slate-200">v{v.version}</span>
                  <span
                    className={`rounded-full px-2 py-0.5 text-[11px] font-medium uppercase tracking-wider ${
                      v.status === 'published'
                        ? 'bg-emerald-500/10 text-emerald-300'
                        : 'bg-slate-500/10 text-slate-400'
                    }`}
                  >
                    {v.status}
                  </span>
                  <span className="text-xs text-slate-500">
                    {formatBytes(v.size_bytes)}
                    {v.published_at
                      ? ` · published ${new Date(v.published_at).toLocaleString()}`
                      : ''}
                  </span>
                </div>
                <div className="flex items-center gap-2">
                  {v.version != null && (
                    <>
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => handleLoadVersion(v.version!)}
                        disabled={busy}
                      >
                        Load into editor
                      </Button>
                      {v.status !== 'published' && (
                        <Button
                          variant="ghost"
                          size="sm"
                          onClick={() => handleRevert(v.version!)}
                          disabled={busy}
                        >
                          Republish
                        </Button>
                      )}
                    </>
                  )}
                </div>
              </div>
            ))}
          </div>
        </CollapsibleSection>
      )}
    </div>
  );
}
