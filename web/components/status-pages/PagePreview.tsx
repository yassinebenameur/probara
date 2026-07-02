'use client';

import { useEffect, useMemo, useState } from 'react';
import { Check, Copy } from 'lucide-react';
import type { Monitor } from '@/lib/types';
import { DEFAULT_PRIMARY, DEFAULT_SECONDARY, type Basics, type EditableSection } from './useStatusPageFormState';

interface PagePreviewProps {
  basics: Basics;
  sections: EditableSection[];
  monitors: Monitor[];
  /** Resolved public URL (may include auth query params — stripped for display). */
  publicUrl: string;
}

const HEX_RE = /^#([0-9a-f]{3}|[0-9a-f]{6})$/i;

function safeHex(value: string, fallback: string): string {
  return HEX_RE.test(value.trim()) ? value.trim() : fallback;
}

/**
 * Stylized live mock of the public status page. Reflects branding, title,
 * and the first section's monitors so edits give immediate visual feedback.
 */
export default function PagePreview({ basics, sections, monitors, publicUrl }: PagePreviewProps) {
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

  const previewSection = sections.find((section) => section.monitors.length > 0) ?? sections[0];
  const previewRows = (previewSection?.monitors ?? []).slice(0, 3).map((entry) => ({
    id: entry.monitor_id,
    name: entry.display_name.trim() || monitorNames.get(entry.monitor_id) || 'Monitor',
  }));
  const overflow = (previewSection?.monitors.length ?? 0) - previewRows.length;

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

  return (
    <div className="self-start">
      <div className="overflow-hidden rounded-xl border border-white/[0.08] bg-slate-950/60">
        {/* Browser chrome */}
        <div className="flex items-center gap-2 border-b border-white/[0.06] bg-slate-950/80 px-3 py-2">
          <span className="flex gap-1.5" aria-hidden="true">
            <span className="h-2 w-2 rounded-full bg-rose-400/40" />
            <span className="h-2 w-2 rounded-full bg-amber-400/40" />
            <span className="h-2 w-2 rounded-full bg-emerald-400/40" />
          </span>
          <span className="min-w-0 flex-1 truncate rounded-md bg-white/[0.04] px-2 py-0.5 text-center font-mono text-[10px] text-slate-500">
            {displayUrl}
          </span>
          <button
            type="button"
            onClick={handleCopy}
            title="Copy public URL"
            aria-label="Copy public URL"
            className="flex h-5 w-5 flex-shrink-0 items-center justify-center rounded text-slate-500 transition-colors hover:bg-white/[0.06] hover:text-slate-200"
          >
            {copied ? (
              <Check className="h-3 w-3 text-emerald-400" strokeWidth={2} />
            ) : (
              <Copy className="h-3 w-3" strokeWidth={1.75} />
            )}
          </button>
        </div>

        {/* Page body */}
        <div className="bg-[#0a0a0f]">
          <div
            aria-hidden="true"
            className="h-0.5 w-full"
            style={{ background: `linear-gradient(90deg, ${primary}, ${secondary})` }}
          />
          <div className="space-y-3.5 p-4">
            <div className="flex items-center gap-3">
              {showLogo ? (
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
                <p className="truncate text-[11px] text-slate-500">
                  {basics.description.trim() || 'System status'}
                </p>
              </div>
            </div>

            <div className="flex items-center gap-2 rounded-lg border border-emerald-500/20 bg-emerald-500/[0.07] px-3 py-2">
              <span className="relative flex h-2 w-2 flex-shrink-0" aria-hidden="true">
                <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-emerald-400 opacity-40 motion-reduce:hidden" />
                <span className="relative inline-flex h-2 w-2 rounded-full bg-emerald-400" />
              </span>
              <span className="text-xs font-medium text-emerald-300">All systems operational</span>
            </div>

            <div className="space-y-2">
              <p className="text-[10px] font-medium uppercase tracking-[0.14em] text-slate-600">
                {previewSection?.title.trim() || 'Services'}
              </p>
              {previewRows.length === 0 ? (
                <div className="space-y-1.5" aria-hidden="true">
                  {[0, 1].map((i) => (
                    <div
                      key={i}
                      className="flex items-center justify-between rounded-lg border border-dashed border-white/[0.07] px-3 py-2"
                    >
                      <span className="h-2 w-24 rounded bg-white/[0.05]" />
                      <UptimeBars muted />
                    </div>
                  ))}
                </div>
              ) : (
                <div className="space-y-1.5">
                  {previewRows.map((row) => (
                    <div
                      key={row.id}
                      className="flex items-center justify-between gap-3 rounded-lg border border-white/[0.05] bg-white/[0.02] px-3 py-2"
                    >
                      <span className="min-w-0 truncate text-xs text-slate-300">{row.name}</span>
                      <UptimeBars />
                    </div>
                  ))}
                  {overflow > 0 && (
                    <p className="px-1 text-[10px] text-slate-600">
                      + {overflow} more in this section
                    </p>
                  )}
                </div>
              )}
            </div>
          </div>
        </div>
      </div>
      <p className="mt-1.5 text-center text-[10px] uppercase tracking-[0.14em] text-slate-600">
        Live preview · updates as you edit
      </p>
    </div>
  );
}

function UptimeBars({ muted = false }: { muted?: boolean }) {
  return (
    <span className="flex flex-shrink-0 items-center gap-[2px]" aria-hidden="true">
      {Array.from({ length: 18 }, (_, i) => (
        <span
          key={i}
          className={`h-3 w-[3px] rounded-sm ${muted ? 'bg-white/[0.06]' : 'bg-emerald-400'}`}
          style={muted ? undefined : { opacity: 0.55 + ((i * 7) % 5) * 0.1 }}
        />
      ))}
    </span>
  );
}
