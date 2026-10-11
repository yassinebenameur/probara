'use client';

import { FormEvent, useState } from 'react';
import { Check } from 'lucide-react';

import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import CollapsibleSection from '@/components/ui/CollapsibleSection';
import Select from '@/components/ui/Select';
import { useToast } from '@/components/ui/ToastProvider';
import { useCanWrite, useCurrentUser } from '@/components/providers/CurrentUserProvider';
import { labelIncidentAIAnalysis } from '@/lib/api';
import { buildIncidentLabelRequest, labelControlHasContent, labelErrorMessage, labelSummary } from '@/lib/ai-labels';
import { formatRelativeTime } from '@/lib/format';
import type { AILabel, AILabelContext } from '@/lib/types';

const ABSTAIN = 'insufficient_evidence';

interface AILabelControlProps {
  incidentId: string;
  analysisId: string;
  context: AILabelContext;
  onLabelled: (label: AILabel) => void;
}

type Mode = 'idle' | 'prompt' | 'correct';

// Confirm/Correct for a ready analysis. The verdict is stored as a training
// label: the server copies the evidence the model saw from its own snapshot,
// so this control only ever sends catalogue keys.
export default function AILabelControl({ incidentId, analysisId, context, onLabelled }: AILabelControlProps) {
  const { showToast } = useToast();
  const canWrite = useCanWrite();
  const { user, loading: userLoading } = useCurrentUser();
  const label = context.label;
  const choiceKeys = new Set(context.choices.map((c) => c.key));

  // Unlabelled analyses open on the prompt; "Change" reopens it on a labelled one.
  const [mode, setMode] = useState<Mode>(label ? 'idle' : 'prompt');
  const [cause, setCause] = useState('');
  const [overrides, setOverrides] = useState<Record<string, string>>({});
  const [saving, setSaving] = useState<'confirmed' | 'corrected' | null>(null);
  const [error, setError] = useState<string | null>(null);

  const openCorrect = () => {
    // Start from the stored correction when changing a label, else from the
    // prediction unless the model abstained (abstaining is rarely the answer
    // someone who clicked "Correct" means).
    const initial = label?.incident_label ?? (context.predicted !== ABSTAIN ? context.predicted : '');
    setCause(choiceKeys.has(initial) ? initial : '');
    const stored: Record<string, string> = {};
    for (const [id, a] of Object.entries(label?.answers ?? {})) {
      if (a.source === 'operator') stored[id] = a.label;
    }
    setOverrides(stored);
    setError(null);
    setMode('correct');
  };

  const submit = async (verdict: 'confirmed' | 'corrected') => {
    setSaving(verdict);
    setError(null);
    try {
      const req = verdict === 'confirmed'
        ? buildIncidentLabelRequest(analysisId, 'confirmed')
        : buildIncidentLabelRequest(analysisId, 'corrected', cause, overrides);
      const saved = await labelIncidentAIAnalysis(incidentId, req);
      onLabelled(saved);
      setMode('idle');
      showToast('Label saved', 'success');
    } catch (err) {
      setError(labelErrorMessage(err, 'Could not save the label'));
    } finally {
      setSaving(null);
    }
  };

  const onSubmitCorrect = (e: FormEvent) => {
    e.preventDefault();
    if (cause) void submit('corrected');
  };

  const showPrompt = canWrite && (mode === 'prompt' || (!label && mode === 'idle'));

  if (!labelControlHasContent(Boolean(label), canWrite, userLoading)) return null;

  return (
    <div className="space-y-3 border-t border-divider pt-4">
      {error && <Callout tone="danger">{error}</Callout>}

      {label && mode !== 'correct' && (
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
          <span className="inline-flex items-center gap-1.5 text-ui text-slate-400">
            <Check className="h-3.5 w-3.5 text-emerald-400" strokeWidth={2} aria-hidden="true" />
            {labelSummary(label, context.choices, user?.id)} · {formatRelativeTime(label.labeled_at)}
          </span>
          {canWrite && mode === 'idle' && (
            <Button variant="ghost" size="xs" onClick={() => { setError(null); setMode('prompt'); }}>Change</Button>
          )}
        </div>
      )}

      {showPrompt && (
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
          <span className="text-ui text-slate-300">Was this analysis right?</span>
          <div className="flex items-center gap-2">
            <Button
              variant="ghost"
              size="xs"
              loading={saving === 'confirmed'}
              disabled={saving !== null}
              onClick={() => void submit('confirmed')}
            >
              Confirm
            </Button>
            <Button variant="subtle" size="xs" disabled={saving !== null} onClick={openCorrect}>Correct</Button>
            {label && (
              <Button variant="subtle" size="xs" disabled={saving !== null} onClick={() => { setError(null); setMode('idle'); }}>
                Cancel
              </Button>
            )}
          </div>
        </div>
      )}

      {canWrite && mode === 'correct' && (
        <form className="glass-well space-y-3 p-4" onSubmit={onSubmitCorrect}>
          <Select label="Actual cause" value={cause} onChange={(e) => setCause(e.target.value)} required>
            <option value="">Select a cause</option>
            {context.choices.map((c) => <option key={c.key} value={c.key}>{c.title}</option>)}
          </Select>

          {context.decisions.length > 1 && (
            <CollapsibleSection
              variant="row"
              title="Per-monitor labels (optional)"
              summary={Object.values(overrides).some(Boolean) ? 'Some monitors differ' : 'All follow the actual cause'}
            >
              <div className="space-y-3 pb-1">
                <p className="text-xs text-slate-500">
                  Monitors that follow the actual cause take it only where their own evidence supports it; otherwise they are recorded as insufficient evidence.
                </p>
                {context.decisions.map((d) => (
                  <div key={d.id} className="grid items-center gap-2 sm:grid-cols-2">
                    <span className="truncate text-ui text-slate-300">{d.monitor_name ?? 'Removed monitor'}</span>
                    <Select
                      aria-label={`Label for ${d.monitor_name ?? 'removed monitor'}`}
                      value={overrides[d.id] ?? ''}
                      onChange={(e) => setOverrides((prev) => ({ ...prev, [d.id]: e.target.value }))}
                    >
                      <option value="">Follow the actual cause</option>
                      {context.choices.map((c) => <option key={c.key} value={c.key}>{c.title}</option>)}
                    </Select>
                  </div>
                ))}
              </div>
            </CollapsibleSection>
          )}

          <div className="flex justify-end gap-2">
            <Button
              type="button"
              variant="ghost"
              size="xs"
              disabled={saving !== null}
              onClick={() => { setError(null); setMode(label ? 'idle' : 'prompt'); }}
            >
              Cancel
            </Button>
            <Button type="submit" variant="accent" size="xs" loading={saving === 'corrected'} disabled={!cause || saving !== null}>
              Save label
            </Button>
          </div>
        </form>
      )}

      {!canWrite && !userLoading && !label && (
        <p className="text-ui text-slate-500">Not labelled yet. Editors can confirm or correct this analysis for retraining.</p>
      )}
    </div>
  );
}
