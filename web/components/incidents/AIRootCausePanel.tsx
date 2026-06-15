'use client';

import { useCallback, useEffect, useState } from 'react';

import { getIncidentAIAnalysis, requestIncidentAIAnalysis } from '@/lib/api';
import type { IncidentAIAnalysis } from '@/lib/types';
import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
import Pill from '@/components/ui/Pill';
import type { PillTone } from '@/components/ui/Pill';
import { formatDateTime } from '@/lib/format';

const POLL_INTERVAL_MS = 2500;

interface AIRootCausePanelProps {
  incidentId: string;
  initial?: IncidentAIAnalysis | null;
}

function errorMessage(err: unknown, fallback: string): string {
  if (err && typeof err === 'object' && 'message' in err) {
    const m = (err as { message?: unknown }).message;
    if (typeof m === 'string' && m) return m;
  }
  if (err instanceof Error && err.message) return err.message;
  return fallback;
}

const CONFIDENCE_TONE: Record<string, PillTone> = {
  high: 'success',
  medium: 'info',
  low: 'neutral',
};

export default function AIRootCausePanel({ incidentId, initial }: AIRootCausePanelProps) {
  const [analysis, setAnalysis] = useState<IncidentAIAnalysis | null>(initial ?? null);
  const [requesting, setRequesting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Poll while an analysis is pending. The effect re-runs each time `analysis`
  // changes (every poll), scheduling the next tick until the run terminates.
  useEffect(() => {
    if (analysis?.status !== 'pending') return;
    let cancelled = false;
    const timer = setTimeout(async () => {
      try {
        const latest = await getIncidentAIAnalysis(incidentId);
        if (!cancelled && latest) setAnalysis(latest);
      } catch {
        // Transient fetch error — keep the current state; next render retries.
      }
    }, POLL_INTERVAL_MS);
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, [analysis, incidentId]);

  const handleAnalyze = useCallback(async () => {
    setRequesting(true);
    setError(null);
    try {
      const pending = await requestIncidentAIAnalysis(incidentId);
      setAnalysis(pending);
    } catch (err) {
      setError(errorMessage(err, 'Failed to start analysis'));
    } finally {
      setRequesting(false);
    }
  }, [incidentId]);

  const isPending = analysis?.status === 'pending';
  const buttonLabel = analysis ? 'Re-analyze' : 'Analyze';

  return (
    <Panel
      title="AI root cause analysis"
      subtitle="LLM diagnosis over the incident's probe evidence"
      dotColor="#06b6d4"
      actions={(
        <Button
          variant="accent"
          size="xs"
          loading={requesting || isPending}
          disabled={requesting || isPending}
          onClick={handleAnalyze}
        >
          {buttonLabel}
        </Button>
      )}
    >
      {error && (
        <div className="mb-3 rounded-lg border border-rose-500/30 bg-rose-500/10 px-3 py-2 text-xs text-rose-200">
          {error}
        </div>
      )}

      {!analysis && !error && (
        <div className="text-sm text-slate-500">
          No analysis yet. Run one to get an LLM-generated root cause over the linked monitors,
          recent checks, and dependency signals.
        </div>
      )}

      {isPending && (
        <div className="text-sm text-slate-400">
          Analyzing the incident evidence… this can take a moment.
        </div>
      )}

      {analysis?.status === 'failed' && (
        <div className="text-sm text-rose-200">
          Analysis failed{analysis.error_message ? `: ${analysis.error_message}` : ''}. Try again.
        </div>
      )}

      {analysis?.status === 'ready' && (
        <div className="space-y-4">
          {analysis.summary && (
            <p className="text-sm leading-relaxed text-slate-200">{analysis.summary}</p>
          )}

          {analysis.probable_root_cause && (
            <div>
              <div className="mb-1 flex items-center gap-2 text-xs font-medium uppercase tracking-wide text-slate-400">
                Probable root cause
                {analysis.confidence && (
                  <Pill tone={CONFIDENCE_TONE[analysis.confidence] ?? 'neutral'} size="xs">
                    {analysis.confidence} confidence
                  </Pill>
                )}
              </div>
              <p className="text-sm text-slate-200">{analysis.probable_root_cause}</p>
            </div>
          )}

          {analysis.contributing_factors && analysis.contributing_factors.length > 0 && (
            <div>
              <div className="mb-1 text-xs font-medium uppercase tracking-wide text-slate-400">
                Contributing factors
              </div>
              <ul className="list-disc space-y-1 pl-5 text-sm text-slate-300">
                {analysis.contributing_factors.map((factor, i) => (
                  <li key={i}>{factor}</li>
                ))}
              </ul>
            </div>
          )}

          {analysis.recommended_actions && analysis.recommended_actions.length > 0 && (
            <div>
              <div className="mb-1 text-xs font-medium uppercase tracking-wide text-slate-400">
                Recommended actions
              </div>
              <ul className="list-disc space-y-1 pl-5 text-sm text-slate-300">
                {analysis.recommended_actions.map((action, i) => (
                  <li key={i}>{action}</li>
                ))}
              </ul>
            </div>
          )}

          <div className="text-[0.7rem] text-slate-500">
            {analysis.model ? `Generated by ${analysis.model}` : 'Generated'}
            {analysis.completed_at ? ` · ${formatDateTime(analysis.completed_at)}` : ''}
          </div>
        </div>
      )}
    </Panel>
  );
}
