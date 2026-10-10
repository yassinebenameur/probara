'use client';

import { useMemo } from 'react';
import { ChevronLeft, ChevronRight, Search, Workflow } from 'lucide-react';
import { stateStyle, TYPE_ICONS } from './graphTheme';
import { type RailEntry } from './graphModel';

const MAX_ROWS = 400;

// Left panel: every service participating in the dependency map, worst state
// first. Selecting a row focuses the canvas on that service's neighborhood.
export function ServiceRail({
  entries,
  focusId,
  query,
  onQueryChange,
  onSelect,
}: {
  entries: RailEntry[];
  focusId: string | null;
  query: string;
  onQueryChange: (q: string) => void;
  onSelect: (id: string) => void;
}) {
  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return entries;
    return entries.filter(
      (e) => e.monitor.name.toLowerCase().includes(q) || e.monitor.type.toLowerCase().includes(q)
    );
  }, [entries, query]);

  const rows = filtered.slice(0, MAX_ROWS);
  const overflow = filtered.length - rows.length;

  return (
    <div className="flex w-64 shrink-0 flex-col border-r border-divider lg:w-72">
      <div className="flex h-12 shrink-0 items-center border-b border-divider pl-12 pr-3 md:px-3">
        <div className="relative w-full">
          <Search
            className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-slate-500"
            strokeWidth={1.75}
          />
          <input
            type="text"
            value={query}
            onChange={(e) => onQueryChange(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && rows.length > 0) onSelect(rows[0].monitor.id);
              if (e.key === 'Escape') onQueryChange('');
            }}
            placeholder="Find a service…"
            className="input input-sm h-8 !py-0 !pl-8"
          />
        </div>
      </div>

      <div className="dashboard-scroll min-h-0 flex-1 space-y-px overflow-y-auto px-2 pb-2">
        {entries.length > 0 && (
          <p className="px-2.5 pb-1.5 pt-3 text-2xs text-slate-500">
            {filtered.length} of {entries.length} service{entries.length === 1 ? '' : 's'}
          </p>
        )}
        {entries.length === 0 ? (
          <div className="px-4 py-6 text-center">
            <p className="text-ui text-slate-500">
              Nothing linked yet. Drag from one monitor to another on the canvas to declare a dependency.
            </p>
          </div>
        ) : rows.length === 0 ? (
          <p className="px-4 py-6 text-center text-xs text-slate-500">No services match</p>
        ) : (
          rows.map(({ monitor, dependents, dependencies }) => {
            const s = stateStyle(monitor.current_state);
            const Icon = TYPE_ICONS[monitor.type] ?? Workflow;
            const active = monitor.id === focusId;
            return (
              <button
                key={monitor.id}
                type="button"
                onClick={() => onSelect(monitor.id)}
                className={[
                  'flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left transition-colors',
                  active
                    ? 'bg-control-hover shadow-[inset_0_0_0_1px_var(--control-edge)]'
                    : 'hover:bg-well-hover',
                ].join(' ')}
              >
                <span className={`h-1.5 w-1.5 shrink-0 rounded-full ${s.dot}`} />
                <Icon
                  className={`h-3.5 w-3.5 shrink-0 ${active ? 'text-cyan-500' : 'text-slate-500'}`}
                  strokeWidth={1.75}
                />
                <span
                  className={`min-w-0 flex-1 truncate text-ui ${active ? 'font-medium text-white' : 'text-slate-300'}`}
                  title={monitor.name}
                >
                  {monitor.name}
                </span>
                <span className="flex shrink-0 items-center gap-1.5 text-2xs tabular-nums text-slate-500">
                  {dependents > 0 && (
                    <span
                      className="flex items-center"
                      title={`${dependents} service${dependents === 1 ? '' : 's'} depend${dependents === 1 ? 's' : ''} on this`}
                    >
                      <ChevronLeft className="h-3 w-3" strokeWidth={2} />
                      {dependents}
                    </span>
                  )}
                  {dependencies > 0 && (
                    <span
                      className="flex items-center"
                      title={`depends on ${dependencies} service${dependencies === 1 ? '' : 's'}`}
                    >
                      {dependencies}
                      <ChevronRight className="h-3 w-3" strokeWidth={2} />
                    </span>
                  )}
                </span>
              </button>
            );
          })
        )}
        {overflow > 0 && (
          <p className="px-4 py-2 text-center text-2xs text-slate-500">
            {overflow} more — refine your search
          </p>
        )}
      </div>
    </div>
  );
}
