'use client';

import { useEffect, useState } from 'react';
import AILabelControl from '@/components/incidents/AILabelControl';
import TriagePill from '@/components/alerts/TriagePill';
import Callout from '@/components/ui/Callout';
import Dialog from '@/components/ui/Dialog';
import { getAlert, labelAlertTriage } from '@/lib/api';
import { labelErrorMessage } from '@/lib/ai-labels';
import { buildTriageLabelRequest } from '@/lib/ai-triage';
import type { AILabel, Alert, AlertTriage } from '@/lib/types';

interface TriageLabelDialogProps {
  alert: Alert;
  /** 'correct' opens on the correction form; 'prompt' offers Confirm/Correct. */
  initialMode: 'prompt' | 'correct';
  onClose: () => void;
  onLabelled: (label: AILabel) => void;
}

// Confirm or correct an alert's automatic triage as a training label. The
// label choices come from GET /alerts/{id} (the list omits them); the server
// copies the evidence the model saw from the triage's own snapshot.
export default function TriageLabelDialog({ alert, initialMode, onClose, onLabelled }: TriageLabelDialogProps) {
  const [triage, setTriage] = useState<AlertTriage | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    getAlert(alert.id)
      .then((fresh) => {
        if (cancelled) return;
        if (!fresh.triage?.label_context) setError('This triage cannot be labelled.');
        else setTriage(fresh.triage);
      })
      .catch((err) => { if (!cancelled) setError(labelErrorMessage(err, 'Could not load the triage')); });
    return () => { cancelled = true; };
  }, [alert.id]);

  const subject = alert.kind === 'mesh_edge'
    ? `${alert.source_location_name || 'unknown'} → ${alert.target_location_name || 'unknown'}`
    : alert.monitor_name || alert.monitor_id || 'Alert';

  return (
    <Dialog
      onClose={onClose}
      title="Label suspected cause"
      description={<span className="block truncate">{subject}</span>}
      bodyClassName="space-y-4"
    >
      {error && <Callout tone="danger">{error}</Callout>}
      {!triage && !error && <p role="status" className="text-sm text-slate-400">Loading…</p>}
      {triage?.label_context && (
        <>
          <div className="space-y-2">
            <TriagePill triage={triage} />
            {triage.summary && <p className="text-ui leading-relaxed text-slate-300">{triage.summary}</p>}
            <p className="text-xs text-slate-500">
              Labels are kept for retraining the local model. Triage never changes who is paged or when; correcting the cause stops any follow-up not sent yet.
            </p>
          </div>
          <AILabelControl
            key={triage.id}
            bare
            noun="triage"
            initialMode={initialMode}
            context={triage.label_context}
            submit={(verdict, cause, overrides) =>
              labelAlertTriage(alert.id, buildTriageLabelRequest(verdict, cause, overrides))}
            onLabelled={onLabelled}
          />
        </>
      )}
    </Dialog>
  );
}
