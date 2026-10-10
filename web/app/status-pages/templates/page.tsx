'use client';

import { useRouter } from 'next/navigation';
import { useState, useEffect, useCallback, useRef } from 'react';
import { Download, Plus, Trash2, Pencil, FileCode, Upload } from 'lucide-react';
import { StatusPageLibraryTemplate } from '@/lib/types';
import {
  listLibraryTemplates,
  createLibraryTemplate,
  updateLibraryTemplate,
  deleteLibraryTemplate,
  getLibraryTemplateSource,
} from '@/lib/api';
import { formatDateTime, formatDateTimeFull } from '@/lib/format';
import PageHeader from '@/components/ui/PageHeader';
import Button from '@/components/ui/Button';
import IconButton from '@/components/ui/IconButton';
import FormField from '@/components/ui/FormField';
import Callout from '@/components/ui/Callout';
import EmptyState from '@/components/ui/EmptyState';
import { useToast } from '@/components/ui/ToastProvider';

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  return `${(bytes / 1024).toFixed(1)} KB`;
}

export default function StatusPageTemplateLibraryPage() {
  const router = useRouter();
  const { showToast } = useToast();

  const [items, setItems] = useState<StatusPageLibraryTemplate[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  const [showCreate, setShowCreate] = useState(false);
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [source, setSource] = useState('');
  const fileInputRef = useRef<HTMLInputElement>(null);

  const load = useCallback(async () => {
    try {
      setLoading(true);
      setError('');
      const result = await listLibraryTemplates();
      setItems(result.items);
    } catch (err: any) {
      setError(err.message || 'Failed to load templates');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  const handleCreate = async () => {
    try {
      setBusy(true);
      await createLibraryTemplate({
        name,
        description: description.trim() || undefined,
        source,
      });
      showToast(`Template “${name.trim()}” added to the library`, 'success');
      setShowCreate(false);
      setName('');
      setDescription('');
      setSource('');
      await load();
    } catch (err: any) {
      showToast(err.message || 'Failed to create template', 'error');
    } finally {
      setBusy(false);
    }
  };

  const handleUpload = async (event: React.ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    event.target.value = '';
    if (!file) return;
    setSource(await file.text());
    if (!name.trim()) {
      setName(file.name.replace(/\.(go)?html?$|\.tmpl$|\.txt$/i, ''));
    }
    showToast(`Loaded ${file.name}`, 'success');
  };

  const handleRename = async (item: StatusPageLibraryTemplate) => {
    const next = window.prompt('Rename template', item.name);
    if (!next || next.trim() === item.name) return;
    try {
      setBusy(true);
      await updateLibraryTemplate(item.id, { name: next });
      showToast('Template renamed', 'success');
      await load();
    } catch (err: any) {
      showToast(err.message || 'Failed to rename template', 'error');
    } finally {
      setBusy(false);
    }
  };

  const handleDelete = async (item: StatusPageLibraryTemplate) => {
    if (
      !window.confirm(
        `Delete “${item.name}” from the library? Pages that applied it keep their own copies.`
      )
    ) {
      return;
    }
    try {
      setBusy(true);
      await deleteLibraryTemplate(item.id);
      showToast('Template deleted', 'success');
      await load();
    } catch (err: any) {
      showToast(err.message || 'Failed to delete template', 'error');
    } finally {
      setBusy(false);
    }
  };

  const handleDownload = async (item: StatusPageLibraryTemplate) => {
    try {
      const text = await getLibraryTemplateSource(item.id);
      const blob = new Blob([text], { type: 'text/plain;charset=utf-8' });
      const url = URL.createObjectURL(blob);
      const link = document.createElement('a');
      link.href = url;
      link.download = `${item.name}.gohtml`;
      link.click();
      URL.revokeObjectURL(url);
    } catch (err: any) {
      showToast(err.message || 'Failed to download template', 'error');
    }
  };

  if (loading) {
    return (
      <div className="animate-pulse space-y-6">
        <div className="space-y-2">
          <div className="h-4 w-44 rounded-md bg-well-hover" />
          <div className="h-8 w-56 rounded-lg bg-well-hover" />
        </div>
        <div className="glass h-[72px]" />
        <span className="sr-only">Loading template library…</span>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <PageHeader
        breadcrumb={[{ label: 'Status pages', href: '/status-pages' }, { label: 'Template library' }]}
        title="Template library"
        subtitle="Reusable page templates for this workspace — apply them from any status page's template editor"
        action={
          <Button
            variant="accent"
            size="sm"
            icon={<Plus strokeWidth={1.75} />}
            onClick={() => setShowCreate((v) => !v)}
          >
            New template
          </Button>
        }
      />

      {error && <Callout tone="danger">{error}</Callout>}

      {showCreate && (
        <div className="glass space-y-4 p-6">
          <div className="grid gap-4 md:grid-cols-2">
            <FormField label="Name">
              <input
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="Aurora light"
                className="input"
              />
            </FormField>
            <FormField label="Description (optional)">
              <input
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                placeholder="Light-first brand template, product-triad hairline"
                className="input"
              />
            </FormField>
          </div>
          <div>
            <div className="mb-1.5 flex items-center justify-between">
              <label className="block text-ui font-medium text-slate-300">
                Template source
              </label>
              <Button
                variant="subtle"
                size="xs"
                icon={<Upload strokeWidth={1.75} />}
                onClick={() => fileInputRef.current?.click()}
              >
                Upload file
              </Button>
            </div>
            <textarea
              value={source}
              onChange={(e) => setSource(e.target.value)}
              rows={10}
              spellCheck={false}
              wrap="off"
              placeholder="Paste a Go html/template here, or upload a file"
              className="glass-well block w-full p-3 font-mono text-xs leading-5 text-slate-200 placeholder:text-slate-500 focus:outline-none focus:ring-1 focus:ring-inset focus:ring-cyan-500/50"
            />
          </div>
          <div className="flex justify-end gap-2">
            <Button variant="ghost" size="sm" onClick={() => setShowCreate(false)}>
              Cancel
            </Button>
            <Button
              variant="accent"
              size="sm"
              onClick={handleCreate}
              loading={busy}
              disabled={!name.trim() || !source.trim()}
            >
              Add to library
            </Button>
          </div>
        </div>
      )}

      {items.length === 0 ? (
        !showCreate && (
          <EmptyState
            icon={<FileCode strokeWidth={1.5} />}
            title="No templates in the library yet"
            description="Add one here, or open a status page's template editor and use “Save to library” to store its current template for reuse."
          />
        )
      ) : (
        <div className="glass divide-y divide-divider overflow-hidden">
          {items.map((item) => (
            <div
              key={item.id}
              className="flex items-center justify-between gap-3 px-5 py-4 transition-colors hover:bg-well"
            >
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  <FileCode className="h-4 w-4 shrink-0 text-slate-400" strokeWidth={1.75} />
                  <span className="min-w-0 truncate text-sm font-medium text-white">{item.name}</span>
                  <span className="shrink-0 text-xs text-slate-500">{formatBytes(item.size_bytes)}</span>
                </div>
                <div className="mt-0.5 flex min-w-0 gap-x-3 pl-6 text-xs text-slate-500">
                  {item.description && <span className="min-w-0 truncate">{item.description}</span>}
                  <span className="shrink-0" title={formatDateTimeFull(item.updated_at)}>
                    Updated {formatDateTime(item.updated_at)}
                  </span>
                </div>
              </div>
              <div className="flex shrink-0 items-center gap-0.5">
                <IconButton
                  icon={<Pencil strokeWidth={1.75} />}
                  label="Rename"
                  onClick={() => handleRename(item)}
                  disabled={busy}
                />
                <IconButton
                  icon={<Download strokeWidth={1.75} />}
                  label="Download"
                  onClick={() => handleDownload(item)}
                />
                <IconButton
                  icon={<Trash2 strokeWidth={1.75} />}
                  label="Delete"
                  onClick={() => handleDelete(item)}
                  disabled={busy}
                  danger
                />
              </div>
            </div>
          ))}
        </div>
      )}

      <p className="text-xs text-slate-500">
        Applying a library template copies its source into a page&apos;s draft — later edits to
        the library entry never change a live page.
      </p>

      <input
        ref={fileInputRef}
        type="file"
        accept=".html,.gohtml,.tmpl,.txt"
        className="hidden"
        onChange={handleUpload}
      />
    </div>
  );
}
