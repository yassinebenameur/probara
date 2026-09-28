import Link from 'next/link';

/**
 * One line under the brand that answers "is anything wrong?" on every page.
 * The same count drives the ambient alarm bloom (useFleetHealth).
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
        ? `${openAlerts} open alert${openAlerts === 1 ? '' : 's'}`
        : 'No open alerts';

  return (
    <Link
      href="/alerts"
      onClick={onNavigate}
      title={label}
      className={`flex items-center gap-2 rounded-lg text-xs font-medium transition-colors ${
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
      <span className={compact ? 'sr-only sm:not-sr-only' : ''}>{label}</span>
    </Link>
  );
}
