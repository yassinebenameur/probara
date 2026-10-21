import type { Alert, AlertBulkFailureReason, AlertBulkResult } from './types';

/**
 * Bulk triage on the alerts list: which of the selected alerts each action
 * applies to, and how its per-alert result reads. The API never fails a
 * batch because some alerts changed meanwhile (POST /alerts/bulk/*); it
 * reports them in `failed`, and this turns that into one message.
 */

export type BulkAction = 'acknowledge' | 'resolve';

/** The selected alerts an action can move: acknowledge takes active ones, resolve any open one. */
export function bulkTargets(alerts: readonly Alert[]): Record<BulkAction, string[]> {
  return {
    acknowledge: alerts.filter((a) => a.status === 'active').map((a) => a.id),
    resolve: alerts.filter((a) => a.status !== 'resolved').map((a) => a.id),
  };
}

const PAST: Record<BulkAction, string> = { acknowledge: 'acknowledged', resolve: 'resolved' };

const REASON_LABEL: Record<AlertBulkFailureReason, string> = {
  already_acknowledged: 'already acknowledged',
  already_resolved: 'already resolved',
  not_found: 'not found',
};

const plural = (n: number, word: string) => `${n} ${n === 1 ? word : `${word}s`}`;

/** "2 already resolved, 1 not found", most common reason first. */
function reasonSummary(failed: AlertBulkResult['failed']): string {
  const counts = new Map<string, number>();
  for (const f of failed) {
    const label = REASON_LABEL[f.reason] ?? f.reason;
    counts.set(label, (counts.get(label) ?? 0) + 1);
  }
  return Array.from(counts)
    .sort((a, b) => b[1] - a[1])
    .map(([label, n]) => `${n} ${label}`)
    .join(', ');
}

/** The toast for a bulk result: success when everything moved, a warning when some (or all) did not. */
export function bulkOutcome(action: BulkAction, result: AlertBulkResult): { message: string; tone: 'success' | 'warning' } {
  const changed = result.items.length;
  const skipped = result.failed.length;
  if (skipped === 0) return { message: `${plural(changed, 'alert')} ${PAST[action]}`, tone: 'success' };
  if (changed === 0) return { message: `No alerts ${PAST[action]}: ${reasonSummary(result.failed)}`, tone: 'warning' };
  return {
    message: `${plural(changed, 'alert')} ${PAST[action]} · ${skipped} skipped (${reasonSummary(result.failed)})`,
    tone: 'warning',
  };
}
