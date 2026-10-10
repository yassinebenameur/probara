interface TableSkeletonProps {
  /** Placeholder rows; 3 by default. */
  rows?: number;
  /** Placeholder cells per row after the first (wide) one; 3 by default. */
  columns?: number;
  /** Render without the glass panel, for use inside an existing one. */
  bare?: boolean;
  /** Accessible loading text. */
  label?: string;
}

/**
 * First-load placeholder for a list or table: rows at table-row height. Only
 * for the first load; a refetch keeps the rows already on screen.
 */
export default function TableSkeleton({ rows = 3, columns = 3, bare = false, label = 'Loading' }: TableSkeletonProps) {
  return (
    <div className={bare ? '' : 'glass overflow-hidden'} role="status" aria-label={label}>
      {Array.from({ length: rows }, (_, r) => (
        <div
          key={r}
          className="flex items-center gap-6 border-b border-divider px-4 py-4 last:border-b-0"
          aria-hidden="true"
        >
          <div className="flex min-w-0 flex-1 flex-col gap-2">
            <div className="h-3 w-40 max-w-full animate-pulse rounded bg-well-hover" />
            <div className="h-2.5 w-56 max-w-full animate-pulse rounded bg-well" />
          </div>
          {Array.from({ length: columns }, (_, c) => (
            <div key={c} className="hidden h-3 w-14 animate-pulse rounded bg-well-hover sm:block" />
          ))}
        </div>
      ))}
    </div>
  );
}
