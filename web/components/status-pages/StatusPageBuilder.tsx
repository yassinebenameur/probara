'use client';

import { useMemo, useState } from 'react';
import {
  statusPageSchema,
  StatusPageBlock,
  StatusPageBlockType,
  StatusPageDocument,
  StatusPageGlobalStyle,
  StatusPageVersion,
} from '@/lib/statusPageSchema';

const blockLibrary: Array<{
  type: StatusPageBlockType;
  label: string;
  description: string;
}> = [
  { type: 'hero', label: 'Hero', description: 'Headline, description, status badge.' },
  { type: 'status', label: 'System Status', description: 'Service health summary.' },
  { type: 'incidents', label: 'Incidents', description: 'Active + recent incidents.' },
  { type: 'metrics', label: 'Metrics', description: 'SLOs, uptime, response time.' },
  { type: 'updates', label: 'Updates', description: 'Changelog or announcements.' },
  { type: 'cta', label: 'CTA', description: 'Subscribe or contact support.' },
  { type: 'custom', label: 'Custom', description: 'Freeform text section.' },
];

const defaultGlobalStyles: StatusPageGlobalStyle = {
  pageBackground: '#0b1120',
  textColor: '#e2e8f0',
  accentColor: '#22d3ee',
  fontFamily: 'Inter, system-ui, sans-serif',
  baseFontSize: 14,
  headingFontSize: 20,
  sectionSpacing: 16,
  borderRadius: 16,
  borderColor: 'rgba(148,163,184,0.25)',
};

const createId = () => {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) {
    return crypto.randomUUID();
  }
  return `block-${Date.now()}-${Math.random().toString(16).slice(2)}`;
};

const createBlock = (type: StatusPageBlockType): StatusPageBlock => ({
  id: createId(),
  type,
  title: `${blockLibrary.find((block) => block.type === type)?.label ?? 'Section'} Title`,
  content: 'Click to edit content. Add links, announcements, or notes here.',
  visible: true,
  style: {
    backgroundColor: 'rgba(15,23,42,0.6)',
    textColor: '#e2e8f0',
    borderColor: 'rgba(148,163,184,0.2)',
    borderRadius: 16,
    padding: 16,
    align: 'left',
  },
});

const createVersion = (name: string): StatusPageVersion => ({
  name,
  updatedAt: new Date().toISOString(),
  layout: {
    logo: {
      placement: 'left',
      visible: true,
      url: '',
    },
    blocks: [createBlock('hero'), createBlock('status'), createBlock('incidents')],
  },
  styles: { ...defaultGlobalStyles },
});

const defaultDocument: StatusPageDocument = {
  version: '1.0',
  draft: createVersion('Draft'),
  published: createVersion('Published'),
};

const previewSizes = {
  desktop: 'w-full',
  tablet: 'w-[780px]',
  mobile: 'w-[420px]',
};

const fontOptions = [
  'Inter, system-ui, sans-serif',
  'Space Grotesk, system-ui, sans-serif',
  'IBM Plex Sans, system-ui, sans-serif',
  'Merriweather, Georgia, serif',
];

export default function StatusPageBuilder() {
  const [draft, setDraft] = useState<StatusPageVersion>(defaultDocument.draft);
  const [published, setPublished] = useState<StatusPageVersion>(defaultDocument.published);
  const [previewVersion, setPreviewVersion] = useState<'draft' | 'published'>('draft');
  const [selectedBlockId, setSelectedBlockId] = useState<string | null>(
    defaultDocument.draft.layout.blocks[0]?.id ?? null,
  );
  const [previewDevice, setPreviewDevice] = useState<'desktop' | 'tablet' | 'mobile'>('desktop');
  const [importJson, setImportJson] = useState('');
  const [importError, setImportError] = useState('');

  const activeVersion = previewVersion === 'draft' ? draft : published;

  const serializedDocument = useMemo(() => {
    const document: StatusPageDocument = {
      version: '1.0',
      draft,
      published,
    };
    return JSON.stringify(document, null, 2);
  }, [draft, published]);

  const updateDraft = (next: StatusPageVersion) => {
    setDraft({ ...next, updatedAt: new Date().toISOString() });
  };

  const updateBlock = (blockId: string, updater: (block: StatusPageBlock) => StatusPageBlock) => {
    updateDraft({
      ...draft,
      layout: {
        ...draft.layout,
        blocks: draft.layout.blocks.map((block) =>
          block.id === blockId ? updater(block) : block,
        ),
      },
    });
  };

  const handleDrop = (index: number, event: React.DragEvent) => {
    event.preventDefault();
    const blockType = event.dataTransfer.getData('application/x-block-type') as StatusPageBlockType;
    const blockId = event.dataTransfer.getData('application/x-block-id');

    if (blockType) {
      const newBlock = createBlock(blockType);
      const nextBlocks = [...draft.layout.blocks];
      nextBlocks.splice(index, 0, newBlock);
      updateDraft({ ...draft, layout: { ...draft.layout, blocks: nextBlocks } });
      setSelectedBlockId(newBlock.id);
      return;
    }

    if (blockId) {
      const fromIndex = draft.layout.blocks.findIndex((block) => block.id === blockId);
      if (fromIndex === -1 || fromIndex === index) return;
      const nextBlocks = [...draft.layout.blocks];
      const [moved] = nextBlocks.splice(fromIndex, 1);
      nextBlocks.splice(index, 0, moved);
      updateDraft({ ...draft, layout: { ...draft.layout, blocks: nextBlocks } });
    }
  };

  const handleImport = () => {
    setImportError('');
    try {
      const parsed = JSON.parse(importJson) as StatusPageDocument;
      if (!parsed?.draft || !parsed?.published) {
        throw new Error('Missing draft/published versions.');
      }
      setDraft(parsed.draft);
      setPublished(parsed.published);
    } catch (error) {
      setImportError(error instanceof Error ? error.message : 'Invalid JSON document.');
    }
  };

  const publishDraft = () => {
    setPublished({ ...draft, name: 'Published', updatedAt: new Date().toISOString() });
    setPreviewVersion('published');
  };

  return (
    <div className="grid gap-6 lg:grid-cols-[380px_1fr]">
      <SettingsPanel
        draft={draft}
        published={published}
        selectedBlockId={selectedBlockId}
        onSelectBlock={setSelectedBlockId}
        onUpdateDraft={updateDraft}
        onUpdateBlock={updateBlock}
        onPublish={publishDraft}
        previewVersion={previewVersion}
        onPreviewVersionChange={setPreviewVersion}
        serializedDocument={serializedDocument}
        importJson={importJson}
        onImportJsonChange={setImportJson}
        onImport={handleImport}
        importError={importError}
      />
      <PageRenderer
        version={activeVersion}
        draft={draft}
        selectedBlockId={selectedBlockId}
        onSelectBlock={setSelectedBlockId}
        onUpdateBlock={updateBlock}
        onDropBlock={handleDrop}
        previewDevice={previewDevice}
        onPreviewDeviceChange={setPreviewDevice}
        editable={previewVersion === 'draft'}
      />
    </div>
  );
}

interface SettingsPanelProps {
  draft: StatusPageVersion;
  published: StatusPageVersion;
  selectedBlockId: string | null;
  onSelectBlock: (id: string | null) => void;
  onUpdateDraft: (next: StatusPageVersion) => void;
  onUpdateBlock: (blockId: string, updater: (block: StatusPageBlock) => StatusPageBlock) => void;
  onPublish: () => void;
  previewVersion: 'draft' | 'published';
  onPreviewVersionChange: (value: 'draft' | 'published') => void;
  serializedDocument: string;
  importJson: string;
  onImportJsonChange: (value: string) => void;
  onImport: () => void;
  importError: string;
}

function SettingsPanel({
  draft,
  published,
  selectedBlockId,
  onSelectBlock,
  onUpdateDraft,
  onUpdateBlock,
  onPublish,
  previewVersion,
  onPreviewVersionChange,
  serializedDocument,
  importJson,
  onImportJsonChange,
  onImport,
  importError,
}: SettingsPanelProps) {
  const selectedBlock = draft.layout.blocks.find((block) => block.id === selectedBlockId) || null;

  return (
    <aside className="space-y-6">
      <section className="rounded-xl border border-white/[0.08] bg-slate-900/60 p-5">
        <h2 className="text-base font-semibold text-white">Status Page Builder</h2>
        <p className="mt-2 text-xs text-slate-400">
          Drag blocks to build your layout, edit text inline, and style every section in real time.
        </p>
        <div className="mt-4 flex items-center gap-2">
          <button
            type="button"
            onClick={() => onPreviewVersionChange('draft')}
            className={`rounded-full px-3 py-1 text-xs font-medium transition-colors ${
              previewVersion === 'draft'
                ? 'bg-cyan-500 text-white'
                : 'border border-white/[0.1] text-slate-300 hover:bg-white/[0.05]'
            }`}
          >
            Draft Preview
          </button>
          <button
            type="button"
            onClick={() => onPreviewVersionChange('published')}
            className={`rounded-full px-3 py-1 text-xs font-medium transition-colors ${
              previewVersion === 'published'
                ? 'bg-emerald-500 text-white'
                : 'border border-white/[0.1] text-slate-300 hover:bg-white/[0.05]'
            }`}
          >
            Published Preview
          </button>
        </div>
        <div className="mt-4 grid gap-2 text-xs text-slate-400">
          <div className="flex items-center justify-between">
            <span>Draft updated</span>
            <span className="text-slate-300">{new Date(draft.updatedAt).toLocaleString()}</span>
          </div>
          <div className="flex items-center justify-between">
            <span>Published updated</span>
            <span className="text-slate-300">{new Date(published.updatedAt).toLocaleString()}</span>
          </div>
        </div>
        <button
          type="button"
          onClick={onPublish}
          className="mt-4 w-full rounded-lg bg-emerald-500 px-4 py-2 text-xs font-semibold text-white transition-colors hover:bg-emerald-400"
        >
          Publish Draft
        </button>
      </section>

      <section className="rounded-xl border border-white/[0.08] bg-slate-900/60 p-5">
        <h3 className="text-sm font-semibold text-white">Block Library</h3>
        <div className="mt-3 space-y-2">
          {blockLibrary.map((block) => (
            <div
              key={block.type}
              draggable
              onDragStart={(event) =>
                event.dataTransfer.setData('application/x-block-type', block.type)
              }
              className="cursor-grab rounded-lg border border-white/[0.08] bg-slate-950/60 p-3 text-left transition-colors hover:border-cyan-500/60"
            >
              <div className="text-xs font-semibold text-white">{block.label}</div>
              <div className="mt-1 text-[11px] text-slate-400">{block.description}</div>
            </div>
          ))}
        </div>
      </section>

      <section className="rounded-xl border border-white/[0.08] bg-slate-900/60 p-5">
        <h3 className="text-sm font-semibold text-white">Layout + Visibility</h3>
        <p className="mt-2 text-xs text-slate-400">Drag to reorder or toggle visibility per block.</p>
        <div className="mt-4 space-y-2">
          {draft.layout.blocks.map((block) => (
            <div
              key={block.id}
              draggable
              onDragStart={(event) =>
                event.dataTransfer.setData('application/x-block-id', block.id)
              }
              className={`flex items-center justify-between gap-2 rounded-lg border px-3 py-2 text-xs transition-colors ${
                selectedBlockId === block.id
                  ? 'border-cyan-500/60 bg-cyan-500/10'
                  : 'border-white/[0.08] bg-slate-950/60 hover:border-white/20'
              }`}
              onClick={() => onSelectBlock(block.id)}
            >
              <span className="font-medium text-white">{block.title}</span>
              <div className="flex items-center gap-2">
                <button
                  type="button"
                  onClick={(event) => {
                    event.stopPropagation();
                    onUpdateBlock(block.id, (current) => ({
                      ...current,
                      visible: !current.visible,
                    }));
                  }}
                  className={`rounded-full px-2 py-1 text-[10px] font-semibold transition-colors ${
                    block.visible
                      ? 'bg-emerald-500/20 text-emerald-300'
                      : 'bg-slate-700/60 text-slate-300'
                  }`}
                >
                  {block.visible ? 'Visible' : 'Hidden'}
                </button>
              </div>
            </div>
          ))}
        </div>
      </section>

      <section className="rounded-xl border border-white/[0.08] bg-slate-900/60 p-5">
        <h3 className="text-sm font-semibold text-white">Logo + Branding</h3>
        <div className="mt-3 space-y-3 text-xs text-slate-300">
          <label className="flex flex-col gap-2">
            Logo upload
            <input
              type="file"
              accept="image/*"
              className="w-full rounded-lg border border-white/[0.08] bg-slate-950/60 px-3 py-2 text-xs"
              onChange={(event) => {
                const file = event.target.files?.[0];
                if (!file) return;
                const reader = new FileReader();
                reader.onload = () => {
                  onUpdateDraft({
                    ...draft,
                    layout: {
                      ...draft.layout,
                      logo: { ...draft.layout.logo, url: String(reader.result) },
                    },
                  });
                };
                reader.readAsDataURL(file);
              }}
            />
          </label>
          <label className="flex items-center justify-between">
            Show logo
            <input
              type="checkbox"
              checked={draft.layout.logo.visible}
              onChange={(event) =>
                onUpdateDraft({
                  ...draft,
                  layout: {
                    ...draft.layout,
                    logo: { ...draft.layout.logo, visible: event.target.checked },
                  },
                })
              }
              className="h-4 w-4 rounded border border-white/[0.2] bg-slate-950/60"
            />
          </label>
          <label className="flex flex-col gap-2">
            Logo placement
            <select
              value={draft.layout.logo.placement}
              onChange={(event) =>
                onUpdateDraft({
                  ...draft,
                  layout: {
                    ...draft.layout,
                    logo: {
                      ...draft.layout.logo,
                      placement: event.target.value as 'left' | 'center' | 'right',
                    },
                  },
                })
              }
              className="w-full rounded-lg border border-white/[0.08] bg-slate-950/60 px-3 py-2 text-xs"
            >
              <option value="left">Left</option>
              <option value="center">Center</option>
              <option value="right">Right</option>
            </select>
          </label>
        </div>
      </section>

      <StyleEditor
        label="Global Styles"
        globalStyles={draft.styles}
        onChange={(nextStyles) => onUpdateDraft({ ...draft, styles: nextStyles })}
      />

      <StyleEditor
        label="Selected Section Styles"
        block={selectedBlock}
        onChange={(nextBlock) =>
          selectedBlock &&
          onUpdateBlock(selectedBlock.id, () => ({
            ...selectedBlock,
            ...nextBlock,
          }))
        }
      />

      <section className="rounded-xl border border-white/[0.08] bg-slate-900/60 p-5">
        <h3 className="text-sm font-semibold text-white">Architecture</h3>
        <ul className="mt-3 space-y-2 text-xs text-slate-300">
          <li>
            <span className="font-semibold text-white">PageRenderer</span> renders the live preview and
            handles inline edits.
          </li>
          <li>
            <span className="font-semibold text-white">SettingsPanel</span> orchestrates state + versioning.
          </li>
          <li>
            <span className="font-semibold text-white">BlockLibrary</span> supplies draggable sections.
          </li>
          <li>
            <span className="font-semibold text-white">StyleEditor</span> manages global + per-section styling.
          </li>
        </ul>
      </section>

      <section className="rounded-xl border border-white/[0.08] bg-slate-900/60 p-5">
        <h3 className="text-sm font-semibold text-white">Example code</h3>
        <div className="mt-3 space-y-3 text-[11px] text-slate-300">
          <div>
            <p className="text-xs font-semibold text-white">Drag + drop</p>
            <pre className="mt-2 whitespace-pre-wrap rounded-lg bg-slate-950/60 p-3 text-[10px] text-slate-300">
{`const handleDrop = (index, event) => {
  event.preventDefault();
  const type = event.dataTransfer.getData('application/x-block-type');
  if (type) insertBlock(index, type);
};`}
            </pre>
          </div>
          <div>
            <p className="text-xs font-semibold text-white">Inline editing</p>
            <pre className="mt-2 whitespace-pre-wrap rounded-lg bg-slate-950/60 p-3 text-[10px] text-slate-300">
{`<div
  contentEditable
  onBlur={(e) => updateBlockTitle(e.currentTarget.textContent)}
/ >`}
            </pre>
          </div>
        </div>
      </section>

      <section className="rounded-xl border border-white/[0.08] bg-slate-900/60 p-5">
        <h3 className="text-sm font-semibold text-white">Schema + JSON</h3>
        <p className="mt-2 text-xs text-slate-400">
          Schema defines layout + styling. All edits serialize and reload from this JSON.
        </p>
        <pre className="mt-3 max-h-56 overflow-auto rounded-lg bg-slate-950/60 p-3 text-[10px] text-slate-300">
          {JSON.stringify(statusPageSchema, null, 2)}
        </pre>
        <label className="mt-4 block text-xs text-slate-300">Serialized document</label>
        <textarea
          readOnly
          value={serializedDocument}
          rows={8}
          className="mt-2 w-full rounded-lg border border-white/[0.08] bg-slate-950/60 p-3 text-[10px] text-slate-300"
        />
        <label className="mt-4 block text-xs text-slate-300">Load document JSON</label>
        <textarea
          value={importJson}
          onChange={(event) => onImportJsonChange(event.target.value)}
          rows={6}
          placeholder="Paste JSON to load"
          className="mt-2 w-full rounded-lg border border-white/[0.08] bg-slate-950/60 p-3 text-[10px] text-slate-300"
        />
        {importError && <p className="mt-2 text-xs text-rose-400">{importError}</p>}
        <button
          type="button"
          onClick={onImport}
          className="mt-3 w-full rounded-lg border border-cyan-500/40 px-3 py-2 text-xs font-semibold text-cyan-300 hover:bg-cyan-500/10"
        >
          Load JSON
        </button>
      </section>
    </aside>
  );
}

interface StyleEditorProps {
  label: string;
  globalStyles?: StatusPageGlobalStyle;
  block?: StatusPageBlock | null;
  onChange: (next: any) => void;
}

function StyleEditor({ label, globalStyles, block, onChange }: StyleEditorProps) {
  const isBlock = Boolean(block);

  return (
    <section className="rounded-xl border border-white/[0.08] bg-slate-900/60 p-5">
      <h3 className="text-sm font-semibold text-white">{label}</h3>
      {isBlock ? (
        <div className="mt-3 space-y-3 text-xs text-slate-300">
          <label className="flex flex-col gap-2">
            Background
            <input
              type="color"
              value={block?.style.backgroundColor ?? '#0f172a'}
              onChange={(event) =>
                onChange({
                  ...block,
                  style: { ...block?.style, backgroundColor: event.target.value },
                })
              }
              className="h-10 w-full rounded-lg border border-white/[0.08] bg-transparent"
            />
          </label>
          <label className="flex flex-col gap-2">
            Text color
            <input
              type="color"
              value={block?.style.textColor ?? '#e2e8f0'}
              onChange={(event) =>
                onChange({
                  ...block,
                  style: { ...block?.style, textColor: event.target.value },
                })
              }
              className="h-10 w-full rounded-lg border border-white/[0.08] bg-transparent"
            />
          </label>
          <label className="flex flex-col gap-2">
            Border radius
            <input
              type="range"
              min={0}
              max={32}
              value={block?.style.borderRadius ?? 16}
              onChange={(event) =>
                onChange({
                  ...block,
                  style: { ...block?.style, borderRadius: Number(event.target.value) },
                })
              }
            />
          </label>
          <label className="flex flex-col gap-2">
            Padding
            <input
              type="range"
              min={8}
              max={40}
              value={block?.style.padding ?? 16}
              onChange={(event) =>
                onChange({
                  ...block,
                  style: { ...block?.style, padding: Number(event.target.value) },
                })
              }
            />
          </label>
          <label className="flex flex-col gap-2">
            Alignment
            <select
              value={block?.style.align ?? 'left'}
              onChange={(event) =>
                onChange({
                  ...block,
                  style: { ...block?.style, align: event.target.value as 'left' | 'center' | 'right' },
                })
              }
              className="w-full rounded-lg border border-white/[0.08] bg-slate-950/60 px-3 py-2 text-xs"
            >
              <option value="left">Left</option>
              <option value="center">Center</option>
              <option value="right">Right</option>
            </select>
          </label>
        </div>
      ) : (
        <div className="mt-3 grid gap-3 text-xs text-slate-300">
          <label className="flex flex-col gap-2">
            Page background
            <input
              type="color"
              value={globalStyles?.pageBackground ?? '#0b1120'}
              onChange={(event) =>
                onChange({ ...globalStyles, pageBackground: event.target.value })
              }
              className="h-10 w-full rounded-lg border border-white/[0.08] bg-transparent"
            />
          </label>
          <label className="flex flex-col gap-2">
            Accent color
            <input
              type="color"
              value={globalStyles?.accentColor ?? '#22d3ee'}
              onChange={(event) =>
                onChange({ ...globalStyles, accentColor: event.target.value })
              }
              className="h-10 w-full rounded-lg border border-white/[0.08] bg-transparent"
            />
          </label>
          <label className="flex flex-col gap-2">
            Text color
            <input
              type="color"
              value={globalStyles?.textColor ?? '#e2e8f0'}
              onChange={(event) =>
                onChange({ ...globalStyles, textColor: event.target.value })
              }
              className="h-10 w-full rounded-lg border border-white/[0.08] bg-transparent"
            />
          </label>
          <label className="flex flex-col gap-2">
            Font family
            <select
              value={globalStyles?.fontFamily ?? fontOptions[0]}
              onChange={(event) =>
                onChange({ ...globalStyles, fontFamily: event.target.value })
              }
              className="w-full rounded-lg border border-white/[0.08] bg-slate-950/60 px-3 py-2 text-xs"
            >
              {fontOptions.map((font) => (
                <option key={font} value={font}>
                  {font.split(',')[0]}
                </option>
              ))}
            </select>
          </label>
          <label className="flex flex-col gap-2">
            Base font size
            <input
              type="range"
              min={12}
              max={18}
              value={globalStyles?.baseFontSize ?? 14}
              onChange={(event) =>
                onChange({ ...globalStyles, baseFontSize: Number(event.target.value) })
              }
            />
          </label>
          <label className="flex flex-col gap-2">
            Heading size
            <input
              type="range"
              min={18}
              max={28}
              value={globalStyles?.headingFontSize ?? 20}
              onChange={(event) =>
                onChange({ ...globalStyles, headingFontSize: Number(event.target.value) })
              }
            />
          </label>
          <label className="flex flex-col gap-2">
            Section spacing
            <input
              type="range"
              min={8}
              max={32}
              value={globalStyles?.sectionSpacing ?? 16}
              onChange={(event) =>
                onChange({ ...globalStyles, sectionSpacing: Number(event.target.value) })
              }
            />
          </label>
          <label className="flex flex-col gap-2">
            Border radius
            <input
              type="range"
              min={8}
              max={24}
              value={globalStyles?.borderRadius ?? 16}
              onChange={(event) =>
                onChange({ ...globalStyles, borderRadius: Number(event.target.value) })
              }
            />
          </label>
        </div>
      )}
    </section>
  );
}

interface PageRendererProps {
  version: StatusPageVersion;
  draft: StatusPageVersion;
  selectedBlockId: string | null;
  onSelectBlock: (id: string | null) => void;
  onUpdateBlock: (blockId: string, updater: (block: StatusPageBlock) => StatusPageBlock) => void;
  onDropBlock: (index: number, event: React.DragEvent) => void;
  previewDevice: 'desktop' | 'tablet' | 'mobile';
  onPreviewDeviceChange: (value: 'desktop' | 'tablet' | 'mobile') => void;
  editable: boolean;
}

function PageRenderer({
  version,
  draft,
  selectedBlockId,
  onSelectBlock,
  onUpdateBlock,
  onDropBlock,
  previewDevice,
  onPreviewDeviceChange,
  editable,
}: PageRendererProps) {
  const sectionSpacing = version.styles.sectionSpacing ?? 16;
  return (
    <section className="rounded-xl border border-white/[0.08] bg-slate-900/60 p-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h2 className="text-base font-semibold text-white">Live Preview</h2>
          <p className="text-xs text-slate-400">Editing draft content updates immediately.</p>
        </div>
        <div className="flex items-center gap-2">
          {(['desktop', 'tablet', 'mobile'] as const).map((device) => (
            <button
              key={device}
              type="button"
              onClick={() => onPreviewDeviceChange(device)}
              className={`rounded-full px-3 py-1 text-xs font-semibold transition-colors ${
                previewDevice === device
                  ? 'bg-white text-slate-900'
                  : 'border border-white/[0.1] text-slate-300 hover:bg-white/[0.05]'
              }`}
            >
              {device}
            </button>
          ))}
        </div>
      </div>

      <div className="mt-6 flex justify-center">
        <div
          className={`${previewSizes[previewDevice]} rounded-2xl border border-white/[0.08] bg-slate-950/70 p-6 transition-all`}
          style={{
            background: version.styles.pageBackground,
            color: version.styles.textColor,
            fontFamily: version.styles.fontFamily,
            fontSize: version.styles.baseFontSize,
            borderColor: version.styles.borderColor,
            borderRadius: version.styles.borderRadius,
          }}
        >
          <header
            className={`flex items-center gap-4 border-b border-white/[0.1] pb-4 ${
              version.layout.logo.placement === 'center'
                ? 'justify-center text-center'
                : version.layout.logo.placement === 'right'
                  ? 'justify-end text-right'
                  : 'justify-start text-left'
            }`}
          >
            {version.layout.logo.visible && (
              <div className="flex items-center gap-3">
                {version.layout.logo.url ? (
                  <img
                    src={version.layout.logo.url}
                    alt="Logo"
                    className="h-12 w-12 rounded-xl object-cover"
                  />
                ) : (
                  <div
                    className="flex h-12 w-12 items-center justify-center rounded-xl"
                    style={{
                      background: `linear-gradient(135deg, ${version.styles.accentColor}, #6366f1)`,
                    }}
                  >
                    <div className="h-5 w-5 rounded-full bg-slate-950" />
                  </div>
                )}
              </div>
            )}
            <div>
              <div
                contentEditable={editable}
                suppressContentEditableWarning
                onBlur={(event) => {
                  if (!editable) return;
                  const title = event.currentTarget.textContent || 'Status Page';
                  onUpdateBlock(draft.layout.blocks[0]?.id ?? '', (block) => ({
                    ...block,
                    title,
                  }));
                }}
                className="text-lg font-semibold"
                style={{ fontSize: version.styles.headingFontSize }}
              >
                {version.layout.blocks[0]?.title || 'Status Page'}
              </div>
              <div className="text-xs text-slate-400">Realtime status and incident updates</div>
            </div>
          </header>

          <div className="mt-6" style={{ display: 'grid', gap: sectionSpacing }}>
            {version.layout.blocks.map((block, index) => (
              <div key={block.id} style={{ display: 'grid', gap: sectionSpacing }}>
                <DropZone onDrop={(event) => onDropBlock(index, event)} />
                <BlockCard
                  block={block}
                  selected={selectedBlockId === block.id}
                  onSelect={() => onSelectBlock(block.id)}
                  onUpdate={(updater) => onUpdateBlock(block.id, updater)}
                  editable={editable}
                  globalStyles={version.styles}
                />
              </div>
            ))}
            <DropZone onDrop={(event) => onDropBlock(version.layout.blocks.length, event)} />
          </div>
        </div>
      </div>
    </section>
  );
}

function DropZone({ onDrop }: { onDrop: (event: React.DragEvent) => void }) {
  return (
    <div
      onDragOver={(event) => event.preventDefault()}
      onDrop={onDrop}
      className="h-6 rounded-lg border border-dashed border-white/[0.1] text-center text-[10px] text-slate-500"
    >
      Drop here to add or reorder
    </div>
  );
}

interface BlockCardProps {
  block: StatusPageBlock;
  selected: boolean;
  onSelect: () => void;
  onUpdate: (updater: (block: StatusPageBlock) => StatusPageBlock) => void;
  editable: boolean;
  globalStyles: StatusPageGlobalStyle;
}

function BlockCard({ block, selected, onSelect, onUpdate, editable, globalStyles }: BlockCardProps) {
  if (!block.visible) {
    return (
      <div className="rounded-xl border border-dashed border-white/[0.1] bg-slate-950/60 p-4 text-xs text-slate-500">
        {block.title} is hidden
      </div>
    );
  }

  return (
    <article
      draggable
      onDragStart={(event) => event.dataTransfer.setData('application/x-block-id', block.id)}
      onClick={onSelect}
      className={`rounded-2xl border px-4 py-3 transition-colors ${
        selected ? 'border-cyan-500/70 shadow-lg shadow-cyan-500/10' : 'border-white/[0.08]'
      }`}
      style={{
        background: block.style.backgroundColor,
        color: block.style.textColor,
        borderColor: block.style.borderColor ?? globalStyles.borderColor,
        borderRadius: block.style.borderRadius ?? globalStyles.borderRadius,
        padding: block.style.padding ?? globalStyles.sectionSpacing,
        textAlign: block.style.align,
      }}
    >
      <div
        contentEditable={editable}
        suppressContentEditableWarning
        onBlur={(event) => {
          if (!editable) return;
          const title = event.currentTarget.textContent || block.title;
          onUpdate((current) => ({ ...current, title }));
        }}
        className="text-sm font-semibold"
      >
        {block.title}
      </div>
      <div
        contentEditable={editable}
        suppressContentEditableWarning
        onBlur={(event) => {
          if (!editable) return;
          const content = event.currentTarget.textContent || block.content;
          onUpdate((current) => ({ ...current, content }));
        }}
        className="mt-2 text-xs text-slate-300"
      >
        {block.content}
      </div>
      {block.type === 'status' && (
        <div className="mt-3 inline-flex items-center gap-2 rounded-full bg-emerald-500/15 px-3 py-1 text-[10px] text-emerald-200">
          <span className="h-2 w-2 rounded-full bg-emerald-400" />
          All systems operational
        </div>
      )}
    </article>
  );
}
