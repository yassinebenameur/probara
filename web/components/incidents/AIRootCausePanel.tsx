'use client';

import { useCallback, useEffect, useState } from 'react';

import { getIncidentAIAnalysis, labelIncidentAIAnalysis, requestIncidentAIAnalysis } from '@/lib/api';
import type { AILabel, IncidentAIAnalysis } from '@/lib/types';
import { useAIAvailability } from '@/lib/useAIAvailability';
import { useCanWrite, useCurrentUser } from '@/components/providers/CurrentUserProvider';
import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import Pill from '@/components/ui/Pill';
import AILabelControl from '@/components/incidents/AILabelControl';
import { buildIncidentLabelRequest } from '@/lib/ai-labels';
import { pollAnalysis } from '@/lib/analysis-polling';
import { evidenceRefLabel, withMonitorNames } from '@/lib/ai-evidence';
import { formatDateTime } from '@/lib/format';
import { aiConfidenceTone } from '@/lib/status-tones';

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

const SECTION_LABEL = 'mb-1.5 text-xs font-medium text-slate-400';

export default function AIRootCausePanel({ incidentId, initial }: AIRootCausePanelProps) {
  const [analysis, setAnalysis] = useState<IncidentAIAnalysis | null>(initial ?? null);
  const [requesting, setRequesting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const canWrite = useCanWrite();
  // useCanWrite reads false while identity loads, so viewer copy waits for it.
  const { loading: userLoading } = useCurrentUser();
  const viewer = !canWrite && !userLoading;
  // Refreshed while AI is not ready, so the request button re-enables.
  const availability = useAIAvailability();

  useEffect(() => {
    if (analysis?.status !== 'pending') return;
    return pollAnalysis(
      () => getIncidentAIAnalysis(incidentId),
      (latest) => { setAnalysis(latest); setError(null); },
      () => setError('Could not refresh analysis status. Retrying…'),
      POLL_INTERVAL_MS,
    );
  }, [analysis?.id, analysis?.status, incidentId]);

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

  const handleLabelled = useCallback((label: AILabel) => {
    setAnalysis((a) => (a && a.label_context ? { ...a, label_context: { ...a.label_context, label } } : a));
  }, []);

  const isPending = analysis?.status === 'pending';
  // Disabled or offline: the callout says why, so the empty state does not
  // also invite a run the button refuses.
  const unavailable = availability?.enabled === false || availability?.available === false;
  const buttonLabel = analysis ? 'Re-analyze' : 'Analyze';

  return (
    <Panel
      title="AI root cause analysis"
      subtitle="Suspected causes selected from recorded monitoring evidence"
      actions={(
        <Button
          variant="ghost"
          size="xs"
          loading={requesting || isPending}
          disabled={requesting || isPending || !canWrite || unavailable}
          onClick={handleAnalyze}
          title={viewer ? 'Editors can run analysis' : unavailable ? (availability?.enabled === false ? 'AI analysis is disabled' : 'Local analysis service is unavailable') : undefined}
        >
          {buttonLabel}
        </Button>
      )}
    >
      {/* A ready analysis stays readable offline: the warning matters only when
          there is nothing to read; the button's tooltip covers re-runs. */}
      {availability && unavailable && analysis?.status !== 'ready' && <Callout tone="warning" className="mb-4">{!availability.enabled ? 'AI analysis is disabled for this workspace or installation.' : 'Local analysis service is unavailable. Check Settings → AI.'}</Callout>}
      {error && <Callout tone="danger" className="mb-4">{error}</Callout>}

      {!analysis && !error && (
        <p className="text-sm text-slate-400">
          {unavailable
            ? 'No analysis yet.'
            : canWrite
            ? 'No analysis yet. Run one to assess the linked monitor checks and configured upstream evidence.'
            : viewer
              ? 'No analysis yet. Editors can run one to assess the linked monitor checks and configured upstream evidence.'
              : 'No analysis yet.'}
        </p>
      )}

      {isPending && (
        <p className="text-sm text-slate-400">
          Analyzing the incident evidence… this can take a moment.
        </p>
      )}

      {analysis?.status === 'failed' && (
        <p className="text-sm text-rose-300">
          Analysis failed{analysis.error_message ? `: ${analysis.error_message}` : ''}.{canWrite ? ' Try again.' : ''}
        </p>
      )}

      {analysis?.status === 'ready' && (
        <div className="space-y-5">
          {(analysis.metadata?.engine === 'legacy-llm' || !analysis.metadata) && <Pill tone="neutral" size="xs">Legacy external-provider result</Pill>}
          {analysis.metadata?.outcome === 'insufficient_evidence' && <Callout tone="info">The evidence does not support a single cause. Review the observed symptoms and collect missing telemetry.</Callout>}
          {analysis.summary && (
            <p className="text-sm leading-relaxed text-slate-200">{analysis.summary}</p>
          )}

          {analysis.probable_root_cause && (
            <div>
              <div className={`flex items-center gap-2 ${SECTION_LABEL}`}>
                Suspected cause
                {analysis.confidence && (
                  <Pill tone={aiConfidenceTone(analysis.confidence)} size="xs">
                    {analysis.confidence} confidence
                  </Pill>
                )}
              </div>
              <p className="text-sm text-slate-200">{analysis.probable_root_cause}</p>
            </div>
          )}

          {analysis.contributing_factors && analysis.contributing_factors.length > 0 && (
            <div>
              <div className={SECTION_LABEL}>Contributing factors</div>
              <ul className="list-disc space-y-1 pl-5 text-sm text-slate-300 marker:text-slate-500">
                {analysis.contributing_factors.map((factor, i) => (
                  <li key={i}>{factor}</li>
                ))}
              </ul>
            </div>
          )}

          {analysis.recommended_actions && analysis.recommended_actions.length > 0 && (
            <div>
              <div className={SECTION_LABEL}>Recommended actions</div>
              <ul className="list-disc space-y-1 pl-5 text-sm text-slate-300 marker:text-slate-500">
                {analysis.recommended_actions.map((action, i) => (
                  <li key={i}>{action}</li>
                ))}
              </ul>
            </div>
          )}

          {analysis.metadata?.evidence_references?.length ? <div><div className={SECTION_LABEL}>Supporting evidence</div><ul className="space-y-2 text-ui text-slate-300">{analysis.metadata.evidence_references.map((e) => <li key={e.ref}><span className="text-xs text-slate-500">{evidenceRefLabel(e.ref, e.monitor_ref, analysis.monitor_names)}</span><p>{e.fact}</p></li>)}</ul></div> : null}
          {analysis.metadata?.incomplete_evidence?.length ? <Callout tone="warning"><div className={SECTION_LABEL}>Incomplete evidence</div><ul className="space-y-1">{analysis.metadata.incomplete_evidence.map((e, i) => <li key={i}>{withMonitorNames(e, analysis.monitor_names)}</li>)}</ul></Callout> : null}
          <div className="flex flex-wrap gap-x-3 text-xs text-slate-500">
            <span>{analysis.model ? `Generated by ${analysis.model}` : 'Generated'}</span>
            {analysis.completed_at && <span>{formatDateTime(analysis.completed_at)}</span>}
          </div>
          {analysis.label_context && (
            <AILabelControl
              key={analysis.id}
              context={analysis.label_context}
              submit={(verdict, cause, overrides) =>
                labelIncidentAIAnalysis(incidentId, buildIncidentLabelRequest(analysis.id, verdict, cause, overrides))}
              onLabelled={handleLabelled}
            />
          )}
        </div>
      )}
    </Panel>
  );
}
