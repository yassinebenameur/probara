import Link from 'next/link';

// The active-alerts view: the alerts page reads ?status= on load. "Active"
// (not "open"): the alerts page's Open view also holds acknowledged alerts,
// which this count leaves out.
export const OPEN_ALERTS_HREF = '/alerts?status=active';

/**
 * One line under the brand that answers "is anything wrong?" on every page.
 * The same count drives the ambient alarm bloom (useFleetHealth). It links to
 * the active alerts, the list that the number counts.
 */
export default function FleetHealth({
  openAlerts,
  onNavigate,
  compact = false,
}: {
  openAlerts: number | null;
  onNavigate?: () => void;
  compact?: boolean;
}) {
  const alerting = openAlerts !== null && openAlerts > 0;
  const label =
    openAlerts === null
      ? 'Checking alerts'
      : alerting
        ? `${openAlerts} active alert${openAlerts === 1 ? '' : 's'}`
        : 'No active alerts';

  return (
    <Link
      href={alerting ? OPEN_ALERTS_HREF : '/alerts'}
      onClick={onNavigate}
      title={label}
      aria-label={label}
      className={`flex items-center gap-2 rounded-lg text-xs font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-500/50 ${
        compact ? 'px-2 py-1' : 'px-2.5 py-2'
      } ${
        alerting
          ? 'bg-rose-500/10 text-rose-300 hover:bg-rose-500/15'
          : 'text-slate-400 hover:bg-well-hover hover:text-slate-200'
      }`}
    >
      <span
        className={`h-2 w-2 flex-shrink-0 rounded-full ${
          openAlerts === null ? 'bg-slate-500' : alerting ? 'bg-rose-500' : 'bg-emerald-500'
        }`}
        aria-hidden="true"
      />
      {compact ? (
        // Narrow top bar: the count stays visible, the words come back from sm.
        <span aria-hidden="true">
          {alerting && <span className="tabular-nums sm:hidden">{openAlerts}</span>}
          <span className="hidden sm:inline">{label}</span>
        </span>
      ) : (
        <span aria-hidden="true">{label}</span>
      )}
    </Link>
  );
}
