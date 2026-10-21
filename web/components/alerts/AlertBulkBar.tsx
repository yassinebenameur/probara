'use client';

import { useState } from 'react';
import { createPortal } from 'react-dom';
import { CircleCheck, UserCheck, X } from 'lucide-react';
import { bulkAcknowledgeAlerts, bulkResolveAlerts } from '@/lib/api';
import { bulkOutcome, bulkTargets, type BulkAction } from '@/lib/alert-bulk';
import type { Alert, AlertBulkResult } from '@/lib/types';
import { useCanWrite } from '@/components/providers/CurrentUserProvider';
import { useOptionalAlertEvents } from '@/components/alerts/AlertStreamProvider';
import Button from '@/components/ui/Button';
import IconButton from '@/components/ui/IconButton';
import { useToast } from '@/components/ui/ToastProvider';

interface AlertBulkBarProps {
  /** The selected alerts, as listed. */
  selected: Alert[];
  /**
   * The action's result: `items` changed (in their new state), `failed` were
   * skipped because they changed elsewhere meanwhile, so the owner refreshes
   * those rows rather than leaving them stale.
   */
  onApplied: (result: AlertBulkResult) => void;
  onClear: () => void;
}

/**
 * The alerts list's bulk triage bar: "N selected · Acknowledge · Resolve ·
 * Clear", floating over the page while a selection exists (so the list never
 * shifts), like the monitors list's. Portaled: a fixed bar inside a .glass
 * would be offset and clipped. Writers only; each action sends just the
 * selected alerts it can move and reports the rest (POST /alerts/bulk/*).
 */
export default function AlertBulkBar({ selected, onApplied, onClear }: AlertBulkBarProps) {
  const canWrite = useCanWrite();
  const stream = useOptionalAlertEvents();
  const { showToast } = useToast();
  const [busy, setBusy] = useState<BulkAction | null>(null);

  if (!canWrite || selected.length === 0 || typeof document === 'undefined') return null;

  const targets = bulkTargets(selected);

  const run = async (action: BulkAction) => {
    const ids = targets[action];
    if (ids.length === 0) return;
    setBusy(action);
    // The stream echoes each change; marking them keeps those echoes silent.
    ids.forEach((id) => stream?.markLocalAction(id));
    try {
      const result = action === 'acknowledge' ? await bulkAcknowledgeAlerts(ids) : await bulkResolveAlerts(ids);
      onApplied(result);
      onClear();
      const outcome = bulkOutcome(action, result);
      showToast(outcome.message, outcome.tone);
    } catch (err) {
      showToast(err instanceof Error && err.message ? err.message : `Failed to ${action} alerts`, 'error');
    } finally {
      setBusy(null);
    }
  };

  // A count on the button only when it covers part of the selection.
  const count = (n: number) => (n > 0 && n < selected.length ? ` ${n}` : '');

  return createPortal(
    <div
      data-floating-bar
      className="pointer-events-none fixed inset-x-0 bottom-4 z-50 flex justify-center px-4 transition-[left] duration-slow ease-emphasized sm:bottom-6 md:left-[var(--sidebar-offset)]"
    >
      <div
        role="toolbar"
        aria-label="Bulk alert actions"
        className="glass-overlay pointer-events-auto flex max-w-full animate-toast-in flex-wrap items-center gap-2 rounded-xl px-3 py-2"
      >
        {/* Dividers drop below sm so the bar stays one line on a phone. */}
        <span className="px-1 text-ui font-medium tabular-nums text-white" aria-live="polite">
          {selected.length} selected
        </span>

        <div className="h-4 w-px bg-divider max-sm:hidden" />

        <Button
          variant="ghost"
          size="xs"
          icon={<UserCheck strokeWidth={1.75} />}
          onClick={() => void run('acknowledge')}
          disabled={targets.acknowledge.length === 0 || busy !== null}
          loading={busy === 'acknowledge'}
          title={targets.acknowledge.length === 0 ? 'None of the selected alerts is active' : undefined}
        >
          Acknowledge{count(targets.acknowledge.length)}
        </Button>
        <Button
          variant="ghost"
          size="xs"
          icon={<CircleCheck strokeWidth={1.75} />}
          onClick={() => void run('resolve')}
          disabled={targets.resolve.length === 0 || busy !== null}
          loading={busy === 'resolve'}
        >
          Resolve{count(targets.resolve.length)}
        </Button>

        <div className="h-4 w-px bg-divider max-sm:hidden" />

        <IconButton label="Clear selection (Esc)" icon={<X strokeWidth={1.75} />} onClick={onClear} size="sm" />
      </div>
    </div>,
    document.body
  );
}
