'use client';

import Page from '@/components/ui/Page';
import { GRID, CELL_ATTENTION, CELL_CHART, CELL_CHANGES, CELL_GROUPS } from '@/components/dashboard/layout';

// Mirrors the page: header, KPI strip, the grid's first rows and the groups
// row, so the first paint does not jump.
export function LoadingSkeleton() {
  return (
    <Page width="data" className="animate-pulse">
      <div role="status" aria-label="Loading dashboard" className="space-y-6">
        <div className="mb-7 flex items-center justify-between">
          <div className="h-8 w-40 rounded-lg bg-well-hover" />
          <div className="hidden h-9 w-72 rounded-lg bg-well-hover sm:block" />
        </div>
        <div className="glass h-36" />
        <div className={GRID}>
          <div className={`glass h-[22rem] xl:h-auto ${CELL_ATTENTION}`} />
          <div className={`glass h-[20rem] xl:h-auto ${CELL_CHART}`} />
          <div className={`glass h-[20rem] xl:h-auto ${CELL_CHANGES}`} />
          <div className={`glass h-40 ${CELL_GROUPS}`} />
        </div>
      </div>
    </Page>
  );
}
