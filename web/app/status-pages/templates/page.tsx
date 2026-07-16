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
import PageHeader from '@/components/ui/PageHeader';
import Button from '@/components/ui/Button';
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
      <div className="flex h-64 items-center justify-center">
        <div className="text-slate-500">Loading template library…</div>
      </div>
    );
  }

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

      {error && <p className="text-rose-400">{error}</p>}

      {showCreate && (
        <div className="space-y-4 rounded-xl border border-white/[0.06] bg-slate-900/40 p-4">
          <div className="grid gap-4 md:grid-cols-2">
            <div>
              <label className="mb-2 block text-xs font-medium uppercase tracking-wider text-slate-500">
                Name
              </label>
              <input
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="Aurora light"
                className="w-full rounded-lg border border-white/[0.06] bg-slate-950/50 p-3 text-sm text-slate-200 placeholder:text-slate-600 focus:border-cyan-500/40 focus:outline-none"
              />
            </div>
            <div>
              <label className="mb-2 block text-xs font-medium uppercase tracking-wider text-slate-500">
                Description (optional)
              </label>
              <input
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                placeholder="Light-first brand template, product-triad hairline"
                className="w-full rounded-lg border border-white/[0.06] bg-slate-950/50 p-3 text-sm text-slate-200 placeholder:text-slate-600 focus:border-cyan-500/40 focus:outline-none"
              />
            </div>
          </div>
          <div>
            <div className="mb-2 flex items-center justify-between">
              <label className="block text-xs font-medium uppercase tracking-wider text-slate-500">
                Template source
              </label>
              <Button
                variant="ghost"
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
              placeholder="Paste a Go html/template here, or upload a file"
              className="w-full rounded-lg border border-white/[0.06] bg-slate-950/50 p-3 font-mono text-xs text-slate-200 placeholder:text-slate-600 focus:border-cyan-500/40 focus:outline-none"
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

      {items.length === 0 && !showCreate ? (
        <div className="flex flex-col items-center gap-3 rounded-xl border border-white/[0.06] bg-slate-900/40 py-16 text-center">
          <FileCode className="h-8 w-8 text-slate-600" strokeWidth={1.5} />
          <p className="text-sm text-slate-400">No templates in the library yet.</p>
          <p className="max-w-md text-xs text-slate-500">
            Add one here, or open a status page&apos;s template editor and use “Save to library”
            to store its current template for reuse.
          </p>
        </div>
      ) : (
        <div className="divide-y divide-white/[0.06] rounded-xl border border-white/[0.06] bg-slate-900/40 px-4">
          {items.map((item) => (
            <div key={item.id} className="flex flex-wrap items-center justify-between gap-3 py-4">
              <div className="min-w-0">
                <div className="flex items-center gap-2">
                  <FileCode className="h-4 w-4 shrink-0 text-cyan-400" strokeWidth={1.75} />
                  <span className="truncate text-sm font-medium text-slate-200">{item.name}</span>
                  <span className="text-xs text-slate-500">{formatBytes(item.size_bytes)}</span>
                </div>
                <div className="mt-0.5 truncate pl-6 text-xs text-slate-500">
                  {item.description || '—'} · updated {new Date(item.updated_at).toLocaleString()}
                </div>
              </div>
              <div className="flex items-center gap-2">
                <Button
                  variant="ghost"
                  size="xs"
                  icon={<Pencil strokeWidth={1.75} />}
                  onClick={() => handleRename(item)}
                  disabled={busy}
                >
                  Rename
                </Button>
                <Button
                  variant="ghost"
                  size="xs"
                  icon={<Download strokeWidth={1.75} />}
                  onClick={() => handleDownload(item)}
                >
                  Download
                </Button>
                <Button
                  variant="danger"
                  size="xs"
                  icon={<Trash2 strokeWidth={1.75} />}
                  onClick={() => handleDelete(item)}
                  disabled={busy}
                >
                  Delete
                </Button>
              </div>
            </div>
          ))}
        </div>
      )}

      <p className="text-xs text-slate-500">
        Applying a library template copies its source into a page&apos;s draft — later edits to
        the library entry never change a live page.
      </p>
    </div>
  );
}
