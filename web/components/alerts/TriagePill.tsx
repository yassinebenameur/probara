import { Check, Sparkles } from 'lucide-react';
import Pill from '@/components/ui/Pill';
import { triagePresentation } from '@/lib/ai-triage';
import type { AlertTriage } from '@/lib/types';

// Automatic alert triage, as one pill. It reflects the last fetch (the live
// alert stream never carries triage) and never stands for a paging or routing
// change. Renders nothing when there is no triage or
// the alert is of a kind triage does not model.
export default function TriagePill({ triage }: { triage: AlertTriage | null | undefined }) {
  const view = triagePresentation(triage);
  if (!view.visible) return null;
  return (
    <span className={`inline-flex min-w-0 max-w-full ${view.muted ? 'opacity-70' : ''}`} title={view.tooltip}>
      <Pill
        size="xs"
        tone={view.tone}
        icon={<Sparkles strokeWidth={1.75} />}
        className={`min-w-0 max-w-full ${view.muted ? '!text-slate-400' : ''}`}
      >
        <span className="truncate">{view.label}</span>
        {triage?.status === 'ready' && triage.labelled && (
          <Check className="h-3 w-3 shrink-0" strokeWidth={2} aria-label="Labelled" />
        )}
      </Pill>
    </span>
  );
}
