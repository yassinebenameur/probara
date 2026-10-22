'use client';

import { useEffect, useMemo, useState, type CSSProperties } from 'react';
import { Check, Copy } from 'lucide-react';
import type { Monitor } from '@/lib/types';
import {
  COMPONENT_STATUS_LABEL,
  pageHeadline,
  type ComponentStatus,
} from '@/lib/status-page-status';
import IconButton from '@/components/ui/IconButton';
import { DEFAULT_PRIMARY, DEFAULT_SECONDARY, type Basics, type EditableSection } from './useStatusPageFormState';

interface PagePreviewProps {
  basics: Basics;
  sections: EditableSection[];
  monitors: Monitor[];
  /** What visitors see per monitor id now; null while a group's members load. */
  statuses: ReadonlyMap<string, ComponentStatus | null>;
  /** The monitor list is still loading, so no status is known yet. */
  statusLoading: boolean;
  /** Resolved public URL (may include auth query params — stripped for display). */
  publicUrl: string;
}

const HEX_RE = /^#([0-9a-f]{3}|[0-9a-f]{6})$/i;

function safeHex(value: string, fallback: string): string {
  return HEX_RE.test(value.trim()) ? value.trim() : fallback;
}

// The mock shows the public page's default dark theme in either console theme.
// The Tailwind palette inverts in light mode, so pin the scales used inside
// the mock to their dark values.
const DARK_PAGE_PALETTE = {
  '--tw-white': '255 255 255',
  '--tw-slate-300': '174 182 194',
  '--tw-slate-500': '121 130 143',
  '--tw-emerald-300': '125 227 167',
  '--tw-emerald-400': '70 209 127',
  '--tw-emerald-500': '47 189 106',
  '--tw-amber-300': '238 202 107',
  '--tw-amber-400': '230 178 63',
  '--tw-amber-500': '220 158 38',
  '--tw-rose-300': '247 151 162',
  '--tw-rose-400': '244 112 125',
  '--tw-rose-500': '240 74 90',
  '--tw-sky-300': '151 204 230',
  '--tw-sky-400': '111 181 221',
  '--tw-sky-500': '83 157 201',
  '--tw-slate-400': '139 147 160',
} as CSSProperties;

// The public page's own tones (ok / warn / down / maint / unknown), drawn
// from the pinned dark palette above.
const STATUS_TEXT: Record<ComponentStatus, string> = {
  up: 'text-emerald-300',
  degraded: 'text-amber-300',
  down: 'text-rose-300',
  maintenance: 'text-sky-300',
  unknown: 'text-slate-400',
};

const STATUS_DOT: Record<ComponentStatus, string> = {
  up: 'bg-emerald-400',
  degraded: 'bg-amber-400',
  down: 'bg-rose-400',
  maintenance: 'bg-sky-400',
  unknown: 'bg-slate-400',
};

const HEADLINE_STYLE = {
  ok: { box: 'border-emerald-500/20 bg-emerald-500/[0.07]', dot: 'bg-emerald-400', text: 'text-emerald-300' },
  issues: { box: 'border-amber-500/25 bg-amber-500/[0.08]', dot: 'bg-amber-400', text: 'text-amber-300' },
  maintenance: { box: 'border-sky-500/25 bg-sky-500/[0.08]', dot: 'bg-sky-400', text: 'text-sky-300' },
} as const;

/** Rows a section shows before folding the rest into "+ N more". */
const ROWS_PER_SECTION = 6;

/**
 * Stylized live mock of the public status page: branding, title, the page
 * headline and every section with its components' current public status, so
 * structure edits give immediate visual feedback.
 */
export default function PagePreview({
  basics,
  sections,
  monitors,
  statuses,
  statusLoading,
  publicUrl,
}: PagePreviewProps) {
  const primary = safeHex(basics.primary_color, DEFAULT_PRIMARY);
  const secondary = safeHex(basics.secondary_color, DEFAULT_SECONDARY);
  const displayUrl = publicUrl.split('?')[0];

  const [copied, setCopied] = useState(false);
  const [logoBroken, setLogoBroken] = useState(false);
  useEffect(() => setLogoBroken(false), [basics.logo_url]);

  const monitorNames = useMemo(() => {
    const map = new Map<string, string>();
    monitors.forEach((monitor) => map.set(monitor.id, monitor.name));
    return map;
  }, [monitors]);

  // The headline counts every component on the page, like the public page;
  // it waits until each one's status is known rather than guess "operational".
  const allStatuses = sections.flatMap((section) =>
    section.monitors
      .filter((entry) => statuses.has(entry.monitor_id))
      .map((entry) => statuses.get(entry.monitor_id) ?? null),
  );
  const headline =
    statusLoading || allStatuses.some((status) => status === null)
      ? null
      : allStatuses.length === 0
        ? null
        : pageHeadline(allStatuses as ComponentStatus[]);

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(displayUrl);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // clipboard unavailable — ignore
    }
  };

  const showLogo = /^https?:\/\//.test(basics.logo_url.trim()) && !logoBroken;
  const shownSections = sections.length > 0 ? sections : [{ id: 'placeholder', title: '', monitors: [] }];

  return (
    <div className="mx-auto w-full max-w-xl xl:max-w-none">
      <div className="glass-well overflow-hidden">
        {/* Browser chrome */}
        <div className="flex items-center gap-1.5 py-1 pl-2 pr-1">
          <span className="flex flex-shrink-0 gap-1 pl-1 pr-1.5" aria-hidden="true">
            {[0, 1, 2].map((i) => (
              <span key={i} className="h-2 w-2 rounded-full bg-well-hover" />
            ))}
          </span>
          <span className="min-w-0 flex-1 truncate rounded-md bg-well-hover px-2 py-1 text-center font-mono text-2xs text-slate-400">
            {displayUrl}
          </span>
          <IconButton
            size="sm"
            label="Copy public URL"
            onClick={handleCopy}
            icon={
              copied ? (
                <Check className="text-emerald-400" strokeWidth={2} />
              ) : (
                <Copy strokeWidth={1.75} />
              )
            }
          />
        </div>

        {/* Page body: scrolls inside its frame, so a long page neither runs past
            the pinned pane nor pushes the editor far down on a phone. Focusable
            so keyboard users can scroll to the lower sections too. */}
        <div
          tabIndex={0}
          role="region"
          aria-label="Status page preview"
          className="max-h-[26rem] overflow-y-auto bg-[#0a0a0f] [scrollbar-width:thin] focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-focus xl:max-h-[calc(100dvh-10.5rem)]"
          style={DARK_PAGE_PALETTE}
        >
          <div
            aria-hidden="true"
            className="h-0.5 w-full"
            style={{ background: `linear-gradient(90deg, ${primary}, ${secondary})` }}
          />
          <div className="space-y-4 p-4">
            <div className="flex items-center gap-3">
              {showLogo ? (
                // eslint-disable-next-line @next/next/no-img-element -- arbitrary user-supplied logo URL
                <img
                  src={basics.logo_url.trim()}
                  alt="Logo"
                  onError={() => setLogoBroken(true)}
                  className="h-9 w-9 flex-shrink-0 rounded-lg object-cover"
                />
              ) : (
                <div
                  aria-hidden="true"
                  className="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-lg"
                  style={{
                    background: `linear-gradient(135deg, ${primary} 0%, ${secondary} 100%)`,
                    boxShadow: `0 0 16px ${primary}40`,
                  }}
                >
                  <div className="h-4 w-4 rounded-full border-2 border-white/25 bg-[#0a0a0f]" />
                </div>
              )}
              <div className="min-w-0">
                <p className="truncate text-sm font-semibold text-white">
                  {basics.title.trim() || 'Untitled status page'}
                </p>
                <p className="line-clamp-2 text-2xs text-slate-500">
                  {basics.description.trim() || 'System status'}
                </p>
              </div>
            </div>

            {headline ? (
              <div
                className={`flex flex-wrap items-center gap-x-2 gap-y-0.5 rounded-lg border px-3 py-2 ${HEADLINE_STYLE[headline.tone].box}`}
              >
                <span
                  className={`h-2 w-2 flex-shrink-0 rounded-full ${HEADLINE_STYLE[headline.tone].dot}`}
                  aria-hidden="true"
                />
                <span className={`whitespace-nowrap text-xs font-medium ${HEADLINE_STYLE[headline.tone].text}`}>
                  {headline.label}
                </span>
                {headline.tone !== 'ok' && (
                  <span className="ml-auto text-2xs tabular-nums text-slate-400">{headline.summary}</span>
                )}
              </div>
            ) : (
              <div
                className="h-[34px] rounded-lg border border-white/[0.05] bg-white/[0.02]"
                aria-hidden="true"
              />
            )}

            {shownSections.map((section) => {
              const rows = section.monitors.slice(0, ROWS_PER_SECTION).map((entry) => ({
                id: entry.monitor_id,
                name: entry.display_name.trim() || monitorNames.get(entry.monitor_id) || 'Monitor',
                status: statuses.get(entry.monitor_id) ?? null,
              }));
              const overflow = section.monitors.length - rows.length;
              return (
                <div key={section.id} className="space-y-1.5">
                  <p className="truncate text-2xs font-medium uppercase tracking-[0.14em] text-slate-500">
                    {section.title.trim() || 'Untitled section'}
                  </p>
                  {rows.length === 0 ? (
                    <div className="space-y-1.5" aria-hidden="true">
                      {[0, 1].map((i) => (
                        <div
                          key={i}
                          className="flex items-center justify-between rounded-lg border border-dashed border-white/[0.07] px-3 py-2"
                        >
                          <span className="h-2 w-24 rounded bg-white/[0.05]" />
                          <PlaceholderBars />
                        </div>
                      ))}
                    </div>
                  ) : (
                    <div className="overflow-hidden rounded-lg border border-white/[0.05] bg-white/[0.02]">
                      {rows.map((row, index) => (
                        <div
                          key={row.id}
                          className={`flex items-center justify-between gap-3 px-3 py-1.5 ${
                            index > 0 ? 'border-t border-white/[0.04]' : ''
                          }`}
                        >
                          <span className="min-w-0 truncate text-xs text-slate-300">{row.name}</span>
                          <ComponentState status={row.status} />
                        </div>
                      ))}
                      {overflow > 0 && (
                        <p className="border-t border-white/[0.04] px-3 py-1.5 text-2xs text-slate-500">
                          + {overflow} more
                        </p>
                      )}
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        </div>
      </div>
      <p className="mt-2 text-center text-xs text-slate-500">
        Live preview with current status · updates as you edit
      </p>
    </div>
  );
}

/** Placeholder strip for an empty section. */
function PlaceholderBars() {
  return (
    <span className="flex flex-shrink-0 items-center gap-[2px]" aria-hidden="true">
      {Array.from({ length: 18 }, (_, i) => (
        <span key={i} className="h-3 w-[3px] rounded-sm bg-white/[0.06]" />
      ))}
    </span>
  );
}

/** The component's current public status, as the published page labels it. */
function ComponentState({ status }: { status: ComponentStatus | null }) {
  if (!status) {
    return <span className="h-2 w-16 flex-shrink-0 rounded bg-white/[0.05]" aria-hidden="true" />;
  }
  return (
    <span className={`flex flex-shrink-0 items-center gap-1.5 text-2xs font-medium ${STATUS_TEXT[status]}`}>
      <span className={`h-1.5 w-1.5 rounded-full ${STATUS_DOT[status]}`} aria-hidden="true" />
      {COMPONENT_STATUS_LABEL[status]}
    </span>
  );
}
