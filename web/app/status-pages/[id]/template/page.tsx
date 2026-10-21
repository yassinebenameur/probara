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
  FileInput,
  BookmarkPlus,
  Library,
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
import Page from '@/components/ui/Page';
import CollapsibleSection from '@/components/ui/CollapsibleSection';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import IconButton from '@/components/ui/IconButton';
import FormField from '@/components/ui/FormField';
import Pill from '@/components/ui/Pill';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import Dialog from '@/components/ui/Dialog';
import Input from '@/components/ui/Input';
import { useToast } from '@/components/ui/ToastProvider';
import { formatDateTime, formatDateTimeFull } from '@/lib/format';
import useDocumentTitle from '@/lib/useDocumentTitle';
import { followLiveSource, templateEditorState } from '@/lib/template-editor';
import { isApplePlatform, modalOpen, shortcutLabel } from '@/lib/shortcuts';

const CODE_AREA =
  'glass-well block w-full font-mono text-xs leading-5 text-slate-200 placeholder:text-slate-500 ' +
  'focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-focus';

interface PendingConfirm {
  title: string;
  description: string;
  confirmLabel: string;
  tone: 'danger' | 'default';
  run: () => Promise<unknown>;
}

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
  useDocumentTitle(statusPage ? `Template · ${statusPage.title}` : null);
  const [templateState, setTemplateState] = useState<StatusPageTemplateState | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  const [source, setSource] = useState('');
  // What the editor's text is compared against: the stored draft, or the live
  // source while no draft exists. "Unsaved" means the text differs from it,
  // not merely that no draft exists yet, so every action that changes the
  // draft or what is live moves it (lib/template-editor.ts).
  const [baseline, setBaseline] = useState('');
  const [busy, setBusy] = useState(false);
  const [previewNonce, setPreviewNonce] = useState(0);

  const [brandingCSS, setBrandingCSS] = useState('');
  const [brandingHead, setBrandingHead] = useState('');
  const [brandingFooter, setBrandingFooter] = useState('');
  const [savingBranding, setSavingBranding] = useState(false);

  const [libraryItems, setLibraryItems] = useState<StatusPageLibraryTemplate[]>([]);
  const [confirm, setConfirm] = useState<PendingConfirm | null>(null);
  const [libraryName, setLibraryName] = useState<string | null>(null);

  const fileInputRef = useRef<HTMLInputElement>(null);
  // Read once: the app shell renders client-only.
  const [apple] = useState(isApplePlatform);

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
      let seed: string;
      if (template.draft_source != null) {
        seed = template.draft_source;
      } else if (template.published_version != null) {
        seed = await getStatusPageTemplateVersionSource(id, template.published_version);
      } else {
        seed = await getStatusPageDefaultTemplateSource(id);
      }
      setSource(seed);
      setBaseline(seed);
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

  const hasDraft = templateState?.draft_source != null;
  const { dirty, needsSave, status } = templateEditorState({ source, baseline, hasDraft });

  const previewUrl = useMemo(() => {
    if (!statusPage) return '';
    const base = resolveStatusPageDraftPreviewUrl(
      statusPage,
      templateState?.preview_token
    );
    const separator = base.includes('?') ? '&' : '?';
    return `${base}${separator}_=${previewNonce}`;
  }, [statusPage, templateState?.preview_token, previewNonce]);

  // ⌘S / Ctrl+S saves the draft. A ref keeps the listener stable while the
  // handler closes over the latest source; it stands down while a dialog is
  // open so it never saves behind a confirmation.
  const saveDraftRef = useRef<() => void>(() => {});
  useEffect(() => {
    const handleKey = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 's') {
        event.preventDefault();
        saveDraftRef.current();
      }
    };
    window.addEventListener('keydown', handleKey);
    return () => window.removeEventListener('keydown', handleKey);
  }, []);

  // Unsaved editor text survives neither a reload nor closing the tab.
  useEffect(() => {
    if (!dirty) return;
    const handler = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = '';
    };
    window.addEventListener('beforeunload', handler);
    return () => window.removeEventListener('beforeunload', handler);
  }, [dirty]);

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
      setBaseline(source);
    }
    return state;
  };

  const handlePublish = async () => {
    if (needsSave) {
      try {
        setBusy(true);
        await saveStatusPageTemplateDraft(id, source);
        setBaseline(source);
      } catch (err: any) {
        showToast(err.message || 'Draft is invalid; fix it before publishing', 'error');
        setBusy(false);
        return;
      }
    }
    await runAction(() => publishStatusPageTemplate(id), 'Template published');
  };

  const handleDiscardDraft = async () => {
    const state = await runAction(
      () => discardStatusPageTemplateDraft(id),
      'Draft discarded'
    );
    if (state) {
      await load();
    }
  };

  // Reset and republish change what is live but leave any draft alone. With
  // no draft the baseline is the live source, so it must follow: a clean
  // editor reloads the new live template, edits are kept and read unsaved.
  const followLive = async (state: StatusPageTemplateState) => {
    if (state.draft_source != null) return;
    try {
      const live =
        state.published_version != null
          ? await getStatusPageTemplateVersionSource(id, state.published_version)
          : await getStatusPageDefaultTemplateSource(id);
      const next = followLiveSource({ source, baseline }, live);
      setSource(next.source);
      setBaseline(next.baseline);
    } catch {
      // Could not read the new live source: reload everything rather than
      // leave a status line comparing against the old one.
      await load();
    }
  };

  const handleReset = async () => {
    const state = await runAction(() => resetStatusPageTemplate(id), 'Reverted to the built-in template');
    if (state) await followLive(state);
  };

  const handleRevert = async (version: number) => {
    const state = await runAction(
      () => revertStatusPageTemplate(id, version),
      `Version ${version} republished`
    );
    if (state) await followLive(state);
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

  const handleSaveToLibrary = async (templateName: string) => {
    if (!templateName.trim()) return;
    try {
      setBusy(true);
      const created = await createLibraryTemplate({ name: templateName.trim(), source });
      setLibraryName(null);
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

  saveDraftRef.current = () => {
    if (busy || !needsSave || confirm !== null || libraryName !== null || modalOpen()) return;
    void handleSaveDraft();
  };

  if (loading) {
    return (
      <div className="animate-pulse space-y-6" role="status" aria-label="Loading template">
        <div className="space-y-2" aria-hidden="true">
          <div className="h-4 w-56 rounded-md bg-well-hover" />
          <div className="h-8 w-48 rounded-lg bg-well-hover" />
        </div>
        <div className="glass h-14" aria-hidden="true" />
        <div className="glass h-[700px]" aria-hidden="true" />
      </div>
    );
  }

  if (error || !statusPage) {
    return (
      <Callout
        tone="danger"
        action={
          <Button variant="ghost" size="xs" onClick={() => router.push(`/status-pages/${id}`)}>
            ← Back to status page
          </Button>
        }
      >
        {error || 'Status page not found'}
      </Callout>
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
    <Page width="data">
      <PageHeader
        breadcrumb={[
          { label: 'Status pages', href: '/status-pages' },
          { label: statusPage.title, href: `/status-pages/${id}` },
          { label: 'Template' },
        ]}
        title="Page template"
        subtitle={[`Live: ${activeLabel}`, hasDraft ? 'Unpublished draft' : null]}
        action={
          <Button variant="ghost" size="sm" icon={<ArrowUpRight strokeWidth={1.75} />} asChild>
            <a href={publicUrl} target="_blank" rel="noopener noreferrer">
              Open public page
            </a>
          </Button>
        }
      />

      <CollapsibleSection
        title="Quick branding"
        summary="Custom CSS, extra <head> markup, and footer HTML applied to the built-in template"
      >
        <p className="text-ui text-slate-400">
          The lightweight way to make the page match your brand: these snippets are injected
          into the built-in template (and available to custom templates as{' '}
          <code className="font-mono text-xs text-slate-200">.CustomCSS</code>,{' '}
          <code className="font-mono text-xs text-slate-200">.CustomHeadHTML</code>,{' '}
          <code className="font-mono text-xs text-slate-200">.CustomFooterHTML</code>).
        </p>
        <div className="grid gap-4 lg:grid-cols-3">
          <FormField label="Custom CSS">
            <textarea
              value={brandingCSS}
              onChange={(e) => setBrandingCSS(e.target.value)}
              rows={8}
              spellCheck={false}
              wrap="off"
              placeholder={':root { --brand-primary: #6366f1; }\n.header { … }'}
              className={`${CODE_AREA} p-3`}
            />
          </FormField>
          <FormField label="Extra <head> HTML">
            <textarea
              value={brandingHead}
              onChange={(e) => setBrandingHead(e.target.value)}
              rows={8}
              spellCheck={false}
              wrap="off"
              placeholder={'<link rel="icon" href="…">\n<meta property="og:image" content="…">'}
              className={`${CODE_AREA} p-3`}
            />
          </FormField>
          <FormField label="Footer HTML">
            <textarea
              value={brandingFooter}
              onChange={(e) => setBrandingFooter(e.target.value)}
              rows={8}
              spellCheck={false}
              wrap="off"
              placeholder={'<div class="links">…</div>'}
              className={`${CODE_AREA} p-3`}
            />
          </FormField>
        </div>
        <div className="flex justify-end">
          <Button variant="ghost" size="sm" onClick={handleSaveBranding} loading={savingBranding}>
            Save branding
          </Button>
        </div>
      </CollapsibleSection>

      <div className="glass">
        <div className="flex flex-wrap items-center justify-between gap-3 px-5 py-4">
          <div className="min-w-0">
            <h2 className="text-title font-semibold text-white">Full template editor</h2>
            <p className="mt-0.5 flex flex-wrap gap-x-3 text-xs text-slate-500">
              <span>Go html/template</span>
              <span className="tabular-nums">
                {formatBytes(source.length)} / {formatBytes(maxSize)}
              </span>
              {status === 'unsaved' ? (
                <span className="text-amber-300">Unsaved changes</span>
              ) : status === 'draft' ? (
                <span className="text-sky-300">Draft saved, not published</span>
              ) : (
                <span>Same as the live template</span>
              )}
            </p>
          </div>
          <div className="flex flex-wrap items-center gap-1">
            {libraryItems.length > 0 && (
              <select
                value=""
                onChange={(e) => handleApplyFromLibrary(e.target.value)}
                disabled={busy}
                className="input input-sm mr-1 h-9 !w-auto text-ui"
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
            <Button
              variant="subtle"
              size="sm"
              icon={<BookmarkPlus strokeWidth={1.75} />}
              onClick={() => setLibraryName(`${statusPage.title} template`)}
              disabled={busy}
            >
              Save to library
            </Button>
            <Button
              variant="subtle"
              size="sm"
              icon={<Library strokeWidth={1.75} />}
              onClick={() => router.push('/status-pages/templates')}
            >
              Manage library
            </Button>
            <Button
              variant="subtle"
              size="sm"
              icon={<FileInput strokeWidth={1.75} />}
              onClick={handleLoadDefault}
              disabled={busy}
            >
              Load built-in
            </Button>
            <Button
              variant="subtle"
              size="sm"
              icon={<Upload strokeWidth={1.75} />}
              onClick={() => fileInputRef.current?.click()}
              disabled={busy}
            >
              Upload file
            </Button>
            <Button
              variant="subtle"
              size="sm"
              icon={<Download strokeWidth={1.75} />}
              onClick={handleDownload}
              disabled={busy}
            >
              Download
            </Button>
          </div>
        </div>

        {/* Editor and preview share the viewport height on wide screens, so
            the code and its result stay side by side without page scroll. */}
        <div className="grid gap-4 px-5 xl:h-[max(560px,calc(100dvh-19rem))] xl:grid-cols-2">
          <textarea
            value={source}
            onChange={(e) => setSource(e.target.value)}
            spellCheck={false}
            wrap="off"
            aria-label="Template source"
            className={`${CODE_AREA} min-h-[420px] resize-y p-4 xl:h-full xl:min-h-0 xl:resize-none`}
          />
          <div className="glass-well flex min-h-[420px] flex-col overflow-hidden xl:min-h-0">
            <div className="flex items-center justify-between gap-2 py-1.5 pl-4 pr-1.5">
              <span className="min-w-0 truncate text-ui font-medium text-slate-300">
                {hasDraft ? 'Draft preview' : 'Live page'}
                <span className={`ml-2 hidden font-normal sm:inline ${dirty ? 'text-amber-300' : 'text-slate-500'}`}>
                  {dirty
                    ? 'save the draft to preview your edits'
                    : hasDraft
                      ? 'with live data'
                      : 'save a draft to preview changes'}
                </span>
              </span>
              <div className="flex flex-shrink-0 items-center gap-1">
                <Button
                  variant="subtle"
                  size="xs"
                  icon={<RefreshCw strokeWidth={1.75} />}
                  onClick={() => setPreviewNonce((n) => n + 1)}
                >
                  Refresh
                </Button>
                <Button variant="subtle" size="xs" icon={<ArrowUpRight strokeWidth={1.75} />} asChild>
                  <a href={previewUrl} target="_blank" rel="noopener noreferrer">
                    Open
                  </a>
                </Button>
              </div>
            </div>
            {/* The draft route renders the published (or built-in) page while
                no draft exists, so there is always something real to show. */}
            <iframe
              key={previewNonce}
              src={previewUrl}
              title={hasDraft ? 'Draft template preview' : 'Live status page preview'}
              className="min-h-0 w-full flex-1 bg-[#fff]"
              sandbox="allow-scripts allow-same-origin"
            />
          </div>
        </div>

        <div className="flex flex-wrap items-center justify-between gap-3 px-5 py-4">
          <div className="flex flex-wrap items-center gap-2">
            {hasDraft && (
              <Button
                variant="ghost"
                size="sm"
                icon={<Trash2 strokeWidth={1.75} />}
                onClick={() =>
                  setConfirm({
                    title: 'Discard the draft?',
                    description: 'The saved draft is deleted and the editor reloads the live template. Visitors are not affected.',
                    confirmLabel: 'Discard draft',
                    tone: 'danger',
                    run: handleDiscardDraft,
                  })
                }
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
                onClick={() =>
                  setConfirm({
                    title: 'Reset to the built-in template?',
                    description: 'The public page stops using your custom template right away. Every version stays in the history for republishing.',
                    confirmLabel: 'Reset to built-in',
                    tone: 'danger',
                    run: handleReset,
                  })
                }
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
              loading={busy && !confirm}
              disabled={busy || !needsSave}
              title={shortcutLabel('S', apple)}
            >
              Save draft
            </Button>
            <Button
              variant="accent"
              size="sm"
              icon={<Rocket strokeWidth={1.75} />}
              onClick={() =>
                setConfirm({
                  title: 'Publish this template?',
                  description: needsSave
                    ? 'The editor content is saved as the draft, then published: the public status page renders with it immediately.'
                    : 'The public status page renders with this draft immediately.',
                  confirmLabel: 'Publish',
                  tone: 'default',
                  run: handlePublish,
                })
              }
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
          <div className="divide-y divide-divider">
            {historyVersions.map((v) => (
              <div
                key={`${v.version}-${v.created_at}`}
                className="flex flex-wrap items-center justify-between gap-3 py-2.5"
              >
                <div className="flex items-center gap-3">
                  <span className="text-sm font-medium tabular-nums text-white">v{v.version}</span>
                  <Pill
                    size="xs"
                    tone={v.status === 'published' ? 'success' : 'neutral'}
                    className="capitalize"
                  >
                    {v.status}
                  </Pill>
                  <span className="text-xs tabular-nums text-slate-500">{formatBytes(v.size_bytes)}</span>
                  {v.published_at && (
                    <span className="text-xs text-slate-500" title={formatDateTimeFull(v.published_at)}>
                      published {formatDateTime(v.published_at)}
                    </span>
                  )}
                </div>
                <div className="flex items-center gap-0.5">
                  {v.version != null && (
                    <>
                      <IconButton
                        icon={<FileInput strokeWidth={1.75} />}
                        label="Load into editor"
                        onClick={() => handleLoadVersion(v.version!)}
                        disabled={busy}
                      />
                      {v.status !== 'published' && (
                        <IconButton
                          icon={<Rocket strokeWidth={1.75} />}
                          label="Republish"
                          onClick={() =>
                            setConfirm({
                              title: `Republish version ${v.version}?`,
                              description: 'It becomes the live template again as a new version; the history stays append-only.',
                              confirmLabel: 'Republish',
                              tone: 'default',
                              run: () => handleRevert(v.version!),
                            })
                          }
                          disabled={busy}
                        />
                      )}
                    </>
                  )}
                </div>
              </div>
            ))}
          </div>
        </CollapsibleSection>
      )}

      <ConfirmDialog
        open={confirm !== null}
        title={confirm?.title ?? ''}
        description={confirm?.description}
        confirmLabel={confirm?.confirmLabel}
        tone={confirm?.tone ?? 'default'}
        loading={busy}
        onConfirm={async () => {
          const pending = confirm;
          if (!pending) return;
          await pending.run();
          setConfirm(null);
        }}
        onCancel={() => setConfirm(null)}
      />

      <Dialog
        open={libraryName !== null}
        onClose={() => setLibraryName(null)}
        busy={busy}
        title="Save to library"
        description="Stores a copy of the editor content as a reusable template. Pages that apply it later get their own copy."
        footer={
          <>
            <Button variant="ghost" size="sm" onClick={() => setLibraryName(null)} disabled={busy}>
              Cancel
            </Button>
            <Button
              variant="accent"
              size="sm"
              type="submit"
              form="save-library-form"
              loading={busy}
              disabled={busy || !libraryName?.trim()}
            >
              Save to library
            </Button>
          </>
        }
      >
        <form
          id="save-library-form"
          onSubmit={(event) => {
            event.preventDefault();
            void handleSaveToLibrary(libraryName ?? '');
          }}
        >
          <Input
            label="Template name"
            value={libraryName ?? ''}
            onChange={(event) => setLibraryName(event.target.value)}
            placeholder={`${statusPage.title} template`}
            maxLength={120}
            autoFocus
          />
        </form>
      </Dialog>

      <input
        ref={fileInputRef}
        type="file"
        accept=".html,.gohtml,.tmpl,.txt"
        className="hidden"
        onChange={handleUpload}
      />
    </Page>
  );
}
