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
    <div className="flex w-64 shrink-0 flex-col border-r border-white/[0.06] bg-slate-950/40 lg:w-72">
      <div className="border-b border-white/[0.06] px-3 py-2.5">
        <div className="relative">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-slate-500" />
          <input
            type="text"
            value={query}
            onChange={(e) => onQueryChange(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && rows.length > 0) onSelect(rows[0].monitor.id);
              if (e.key === 'Escape') onQueryChange('');
            }}
            placeholder="Find a service…"
            className="w-full rounded-lg border border-white/[0.08] bg-slate-900/80 py-1.5 pl-8 pr-2 text-xs text-white placeholder:text-slate-600 focus:border-cyan-500/50 focus:outline-none"
          />
        </div>
        <p className="mt-1.5 text-[10px] uppercase tracking-wider text-slate-600">
          {filtered.length} of {entries.length} service{entries.length === 1 ? '' : 's'}
        </p>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto py-1">
        {entries.length === 0 ? (
          <div className="px-4 py-8 text-center">
            <Workflow className="mx-auto h-6 w-6 text-slate-600" strokeWidth={1.5} />
            <p className="mt-2 text-xs text-slate-500">
              No dependencies yet. Add monitors to the canvas and drag between them, or run the AI
              suggestions above.
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
                  'flex w-full items-center gap-2 border-l-2 px-3 py-1.5 text-left transition-colors',
                  active
                    ? 'border-cyan-400 bg-cyan-500/10'
                    : 'border-transparent hover:bg-white/[0.04]',
                ].join(' ')}
              >
                <span className={`h-1.5 w-1.5 shrink-0 rounded-full ${s.dot}`} />
                <Icon className="h-3.5 w-3.5 shrink-0 text-slate-500" strokeWidth={1.75} />
                <span
                  className={`min-w-0 flex-1 truncate text-xs ${active ? 'text-cyan-300' : 'text-slate-300'}`}
                  title={monitor.name}
                >
                  {monitor.name}
                </span>
                <span className="flex shrink-0 items-center gap-1.5 text-[10px] tabular-nums text-slate-500">
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
          <p className="px-4 py-2 text-center text-[10px] text-slate-600">
            {overflow} more — refine your search
          </p>
        )}
      </div>
    </div>
  );
}
