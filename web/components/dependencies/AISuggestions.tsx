'use client';

import { useState } from 'react';
import { Check, Sparkles, X } from 'lucide-react';

import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
import Pill from '@/components/ui/Pill';
import type { PillTone } from '@/components/ui/Pill';
import Callout from '@/components/ui/Callout';
import IconButton from '@/components/ui/IconButton';
import RowMenu, { RowMenuItem } from '@/components/ui/RowMenu';
import { useCanWrite } from '@/components/providers/CurrentUserProvider';
import { useToast } from '@/components/ui/ToastProvider';
import { suggestDependencies, addMonitorDependency, labelDependencySuggestion } from '@/lib/api';
import { dependencyLabelRequest, labelErrorMessage } from '@/lib/ai-labels';
import type { DependencyLabelCorrection, DependencySuggestion } from '@/lib/types';

// Dismiss reasons beyond the default "not a dependency" (the X button). Each
// is recorded as a rejected training label with that correction.
const DISMISS_REASONS: { corrected: DependencyLabelCorrection; label: string }[] = [
  { corrected: 'reversed', label: 'Dismiss: wrong direction' },
  { corrected: 'shared_upstream', label: 'Dismiss: shared upstream explains both' },
  { corrected: 'insufficient_evidence', label: 'Dismiss: not enough evidence' },
];

const CONFIDENCE_TONE: Record<string, PillTone> = {
  high: 'success',
  medium: 'info',
  low: 'neutral',
};

// Returns the scan trigger (rendered in the graph toolbar, or in the empty
// canvas) and the results panel, which stays hidden until a scan has run.
// `renderButton` sizes the trigger for where it sits.
export function useAISuggestions(onAccepted?: () => void) {
  const { showToast } = useToast();
  const canWrite = useCanWrite();
  const [abstained, setAbstained] = useState(0);
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
      setAbstained(result.abstained_pairs || 0);
      setRan(true);
    } catch (err) {
      const msg = err instanceof Error ? err.message : 'request failed';
      setError(
        `Couldn't get suggestions (${msg}). Make sure AI is enabled in Settings → AI.`
      );
    } finally {
      setLoading(false);
    }
  };

  const accept = async (s: DependencySuggestion) => {
    setAcceptingKey(keyOf(s));
    try {
      await addMonitorDependency(s.monitor_id, s.depends_on_id);
    } catch (err) {
      showToast(`Could not add dependency (${labelErrorMessage(err, 'failed')})`, 'error');
      setAcceptingKey(null);
      return;
    }
    setSuggestions((prev) => prev.filter((x) => keyOf(x) !== keyOf(s)));
    showToast(`Added dependency: ${s.monitor_name} → ${s.depends_on_name}`, 'success');
    setAcceptingKey(null);
    onAccepted?.();
    // Labelled only once the edge exists. Saving the edge does not change this
    // pair's evidence, which the server recomputes for the label.
    void labelDependencySuggestion(dependencyLabelRequest(s, 'confirmed')).catch(() =>
      showToast('Dependency added, but the training label was not saved', 'error')
    );
  };

  const dismiss = async (s: DependencySuggestion, corrected?: DependencyLabelCorrection) => {
    setSuggestions((prev) => prev.filter((x) => keyOf(x) !== keyOf(s)));
    try {
      await labelDependencySuggestion(dependencyLabelRequest(s, 'rejected', corrected));
      showToast('Suggestion dismissed and recorded', 'success');
    } catch (err) {
      showToast(`Dismissed, but the label was not saved (${labelErrorMessage(err, 'request failed')})`, 'error');
    }
  };

  const renderButton = (size: 'sm' | 'xs' = 'sm') => (
    <Button variant="ghost" size={size} icon={<Sparkles strokeWidth={1.75} />} loading={loading} disabled={loading} onClick={run}>
      {ran ? 'Re-scan with AI' : 'Suggest with AI'}
    </Button>
  );

  const results = !error && !ran ? null : (
    <Panel
      title="AI dependency suggestions"
      subtitle="Suspected relationships supported by repeated alert timing and service signals. Acceptance validates and updates the graph."
    >
      {error && <Callout tone="danger">{error}</Callout>}

      {!error && ran && suggestions.length === 0 && (
        <div className="text-sm text-slate-500">
          No proposals passed the evidence and calibrated score thresholds{analyzed > 0 ? ` (analyzed ${analyzed} candidate pair${analyzed === 1 ? '' : 's'})` : ''}.
        </div>
      )}

      {ran && abstained > 0 && <p className="text-ui text-slate-400">Abstained on {abstained} candidate pairs. Correlation alone does not prove a dependency.</p>}

      {suggestions.length > 0 && (
        <div className="-mx-5 -mb-5 divide-y divide-divider">
          {suggestions.map((s) => (
            <div
              key={keyOf(s)}
              className="flex items-start justify-between gap-4 px-5 py-3.5 transition-colors hover:bg-well"
            >
              <div className="min-w-0">
                <div className="flex flex-wrap items-center gap-2 text-sm text-white">
                  <span className="font-medium">{s.monitor_name}</span>
                  <span className="text-slate-500">depends on</span>
                  <span className="font-medium">{s.depends_on_name}</span>
                  <Pill tone={CONFIDENCE_TONE[s.confidence] ?? 'neutral'} size="xs" className="capitalize">
                    {s.confidence}
                  </Pill>
                </div>
                {s.reason && <div className="mt-1 text-ui text-slate-400">{s.reason}</div>}
                {s.metadata?.model_version && <p className="mt-1 text-xs text-slate-500">Local model: {s.metadata.model_version} · {s.metadata.hypothesis}</p>}
              </div>
              <div className="flex flex-shrink-0 items-center gap-1">
                <IconButton
                  size="sm"
                  icon={<Check strokeWidth={1.75} />}
                  label="Accept and add dependency"
                  loading={acceptingKey === keyOf(s)}
                  disabled={acceptingKey !== null || !canWrite}
                  onClick={() => void accept(s)}
                />
                <IconButton
                  size="sm"
                  icon={<X strokeWidth={1.75} />}
                  label="Dismiss: not a dependency"
                  disabled={acceptingKey !== null || !canWrite}
                  onClick={() => void dismiss(s)}
                />
                <RowMenu label="More dismiss reasons" size="sm" disabled={acceptingKey !== null || !canWrite}>
                  {DISMISS_REASONS.map((r) => (
                    <RowMenuItem key={r.corrected} onSelect={() => void dismiss(s, r.corrected)}>
                      {r.label}
                    </RowMenuItem>
                  ))}
                </RowMenu>
              </div>
            </div>
          ))}
        </div>
      )}
    </Panel>
  );

  return { renderButton, results };
}
