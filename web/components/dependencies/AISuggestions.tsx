'use client';

import { useState } from 'react';

import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
import Pill from '@/components/ui/Pill';
import type { PillTone } from '@/components/ui/Pill';
import { useToast } from '@/components/ui/ToastProvider';
import { suggestDependencies, addMonitorDependency } from '@/lib/api';
import type { DependencySuggestion } from '@/lib/types';

const CONFIDENCE_TONE: Record<string, PillTone> = {
  high: 'success',
  medium: 'info',
  low: 'neutral',
};

export default function AISuggestions({ onAccepted }: { onAccepted?: () => void }) {
  const { showToast } = useToast();
  const [loading, setLoading] = useState(false);
  const [ran, setRan] = useState(false);
  const [analyzed, setAnalyzed] = useState(0);
  const [suggestions, setSuggestions] = useState<DependencySuggestion[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [acceptingKey, setAcceptingKey] = useState<string | null>(null);

  const keyOf = (s: DependencySuggestion) => `${s.monitor_id}->${s.depends_on_id}`;

  const run = async () => {
    setLoading(true);
    setError(null);
    try {
      const result = await suggestDependencies();
      setSuggestions(result.suggestions || []);
      setAnalyzed(result.analyzed_pairs || 0);
      setRan(true);
    } catch (err) {
      const msg = err instanceof Error ? err.message : 'request failed';
      setError(
        `Couldn't get suggestions (${msg}). Make sure AI is enabled in Settings → AI root cause analysis.`
      );
    } finally {
      setLoading(false);
    }
  };

  const accept = async (s: DependencySuggestion) => {
    setAcceptingKey(keyOf(s));
    try {
      await addMonitorDependency(s.monitor_id, s.depends_on_id);
      setSuggestions((prev) => prev.filter((x) => keyOf(x) !== keyOf(s)));
      showToast(`Added dependency: ${s.monitor_name} → ${s.depends_on_name}`, 'success');
      onAccepted?.();
    } catch (err) {
      const msg = err instanceof Error ? err.message : 'failed';
      showToast(`Could not add dependency (${msg})`, 'error');
    } finally {
      setAcceptingKey(null);
    }
  };

  const dismiss = (s: DependencySuggestion) => {
    setSuggestions((prev) => prev.filter((x) => keyOf(x) !== keyOf(s)));
  };

  return (
    <Panel
      title="AI dependency suggestions"
      subtitle="Infer likely dependencies from co-firing alerts, names, tags, and groups"
      dotColor="#06b6d4"
      actions={(
        <Button variant="accent" size="xs" loading={loading} disabled={loading} onClick={run}>
          {ran ? 'Re-scan' : 'Suggest with AI'}
        </Button>
      )}
    >
      {error && (
        <div className="rounded-lg border border-rose-500/30 bg-rose-500/10 px-3 py-2 text-xs text-rose-200">
          {error}
        </div>
      )}

      {!error && !ran && (
        <div className="text-sm text-slate-500">
          Run a scan to get AI-proposed dependency edges from co-firing alerts, monitor names, tags, and
          groups. Review each and accept the ones that look right — accepting adds the dependency to the graph.
        </div>
      )}

      {!error && ran && suggestions.length === 0 && (
        <div className="text-sm text-slate-500">
          No new dependencies to suggest{analyzed > 0 ? ` (analyzed ${analyzed} co-firing pair${analyzed === 1 ? '' : 's'})` : ''}.
        </div>
      )}

      {suggestions.length > 0 && (
        <div className="space-y-2">
          {suggestions.map((s) => (
            <div
              key={keyOf(s)}
              className="flex items-start justify-between gap-4 rounded-xl border border-white/[0.06] bg-slate-950/40 px-4 py-3"
            >
              <div className="min-w-0">
                <div className="flex items-center gap-2 text-sm text-white">
                  <span className="font-medium">{s.monitor_name}</span>
                  <span className="text-slate-500">depends on</span>
                  <span className="font-medium">{s.depends_on_name}</span>
                  <Pill tone={CONFIDENCE_TONE[s.confidence] ?? 'neutral'} size="xs">
                    {s.confidence}
                  </Pill>
                </div>
                {s.reason && <div className="mt-1 text-xs text-slate-400">{s.reason}</div>}
              </div>
              <div className="flex flex-shrink-0 items-center gap-2">
                <Button
                  variant="accent"
                  size="xs"
                  loading={acceptingKey === keyOf(s)}
                  disabled={acceptingKey !== null}
                  onClick={() => accept(s)}
                >
                  Accept
                </Button>
                <Button variant="ghost" size="xs" disabled={acceptingKey !== null} onClick={() => dismiss(s)}>
                  Dismiss
                </Button>
              </div>
            </div>
          ))}
        </div>
      )}
    </Panel>
  );
}
