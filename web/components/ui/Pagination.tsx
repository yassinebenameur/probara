import type { ReactNode } from 'react';
import Button from './Button';

interface PaginationProps {
  /** 1-based current page. */
  page: number;
  totalPages: number;
  onPageChange: (page: number) => void;
  /**
   * `centred` stands alone under a list and disappears on a single page.
   * `footer` closes a table panel: a summary on the left, the buttons right.
   */
  variant?: 'centred' | 'footer';
  /** Footer only: the left-hand text (default "Page X of Y"). */
  summary?: ReactNode;
  /** Footer only: keep the (disabled) buttons when there is a single page. */
  singlePageControls?: boolean;
}

/** The one Previous / Next pager for paged lists and tables. */
export default function Pagination({
  page,
  totalPages,
  onPageChange,
  variant = 'centred',
  summary,
  singlePageControls = false,
}: PaginationProps) {
  const atStart = page <= 1;
  const atEnd = page >= totalPages;
  const previous = () => onPageChange(Math.max(1, page - 1));
  const next = () => onPageChange(Math.min(totalPages, page + 1));

  if (variant === 'footer') {
    return (
      <div className="flex items-center justify-between gap-3 border-t border-divider px-4 py-3 text-xs text-slate-500">
        <span className="tabular-nums">{summary ?? `Page ${page} of ${totalPages}`}</span>
        {(totalPages > 1 || singlePageControls) && (
          <div className="flex items-center gap-2">
            <Button variant="ghost" size="xs" disabled={atStart} onClick={previous}>
              Previous
            </Button>
            <Button variant="ghost" size="xs" disabled={atEnd} onClick={next}>
              Next
            </Button>
          </div>
        )}
      </div>
    );
  }

  if (totalPages <= 1) return null;
  return (
    <div className="flex items-center justify-center gap-2">
      <Button variant="ghost" size="sm" onClick={previous} disabled={atStart}>
        Previous
      </Button>
      <span className="text-ui tabular-nums text-slate-400">
        Page {page} of {totalPages}
      </span>
      <Button variant="ghost" size="sm" onClick={next} disabled={atEnd}>
        Next
      </Button>
    </div>
  );
}
