'use client';

import Link from 'next/link';
import { Siren } from 'lucide-react';

import type { IncidentListItem } from '@/lib/types';
import { formatDateTime, formatDateTimeFull, formatDurationBetween, pluralize } from '@/lib/format';
import {
  INCIDENT_STATE_LABEL,
  incidentSeverityLabel,
  incidentSeverityTone,
  incidentStateTone,
} from '@/lib/status-tones';
import EmptyState from '@/components/ui/EmptyState';
import Pill from '@/components/ui/Pill';
import SortableHeader from '@/components/ui/SortableHeader';
import type { SortState } from '@/lib/sort';

export type IncidentRow = IncidentListItem;

export const INCIDENT_SORT_KEYS = ['triage', 'title', 'severity', 'state', 'updated'] as const;
export type IncidentSortKey = (typeof INCIDENT_SORT_KEYS)[number];
/** Worst severity, earliest lifecycle state and newest update lead. */
export const INCIDENT_SORT_DEFAULT_DIR = { updated: 'desc' } as const;

interface IncidentListProps {
  incidents: IncidentRow[];
  emptyTitle?: string;
  emptyDescription?: string;
  /** Column sorting; the owner sorts `incidents` (all of them, before paging). */
  sort?: SortState<IncidentSortKey>;
  onSort?: (next: SortState<IncidentSortKey>) => void;
}

export default function IncidentList({
  incidents,
  emptyTitle = 'No incidents yet',
  emptyDescription = 'Create one manually, or have monitors open incidents automatically in Settings.',
  sort,
  onSort,
}: IncidentListProps) {
  if (incidents.length === 0) {
    return (
      <EmptyState
        icon={<Siren strokeWidth={1.75} />}
        title={emptyTitle}
        description={emptyDescription}
      />
    );
  }

  return (
    <div className="glass overflow-hidden">
      <div className="overflow-x-auto">
        <table className="data-table text-ui">
          <thead>
            {sort && onSort ? (
              <tr>
                <SortableHeader label="Incident" sortKey="title" sort={sort} onSort={onSort} className="w-full" />
                <SortableHeader
                  label="Severity"
                  sortKey="severity"
                  sort={sort}
                  onSort={onSort}
                  title="Worst first"
                  className="hidden sm:table-cell"
                />
                <SortableHeader label="State" sortKey="state" sort={sort} onSort={onSort} title="Investigating first" />
                <SortableHeader
                  label="Updated"
                  sortKey="updated"
                  sort={sort}
                  onSort={onSort}
                  defaultDir={INCIDENT_SORT_DEFAULT_DIR}
                  title="Most recent first"
                  className="hidden md:table-cell"
                />
              </tr>
            ) : (
              <tr>
                <th className="w-full">Incident</th>
                <th className="hidden sm:table-cell">Severity</th>
                <th>State</th>
                <th className="hidden md:table-cell">Updated</th>
              </tr>
            )}
          </thead>
          <tbody>
            {incidents.map((incident) => {
              // Only the facts that exist: no column of dashes.
              const facts = [
                incident.owner_username ? `Owner ${incident.owner_username}` : null,
                incident.linked_alert_count > 0 ? pluralize(incident.linked_alert_count, 'alert') : null,
                incident.linked_monitor_count > 0 ? pluralize(incident.linked_monitor_count, 'monitor') : null,
                incident.publication_count > 0
                  ? `public on ${pluralize(incident.publication_count, 'page')}`
                  : null,
                // The list response has no created_at yet (IncidentListItem),
                // so fall back to when it was resolved.
                incident.resolved_at
                  ? incident.created_at
                    ? `resolved after ${formatDurationBetween(incident.created_at, incident.resolved_at)}`
                    : `resolved ${formatDateTime(incident.resolved_at)}`
                  : null,
              ].filter((f): f is string => Boolean(f));
              return (
                // The title link stretches over the row (row-link), so the
                // whole row navigates and the title stays the one tab stop.
                <tr key={incident.id} className="relative">
                  <td className="max-w-0">
                    <div className="flex min-w-0 items-center gap-2">
                      <Link
                        href={`/incidents/${incident.id}`}
                        className="row-link truncate text-sm font-medium text-white transition-colors hover:text-cyan-300 focus:outline-none after:absolute after:inset-0 focus-visible:after:ring-2 focus-visible:after:ring-inset focus-visible:after:ring-focus"
                      >
                        {incident.title}
                      </Link>
                      {incident.source === 'auto' && (
                        <Pill tone="neutral" size="xs">Auto</Pill>
                      )}
                    </div>
                    {/* Below md the Updated (and below sm the Severity)
                        column folds into this caption. */}
                    <div className={`mt-1 flex flex-wrap gap-x-3 gap-y-0.5 text-xs tabular-nums text-slate-500 ${facts.length === 0 ? 'md:hidden' : ''}`}>
                      {incident.severity && (
                        <span className={`sm:hidden ${incidentSeverityTone(incident.severity) === 'danger' ? 'text-rose-300' : ''}`}>
                          {incidentSeverityLabel(incident.severity)}
                        </span>
                      )}
                      {facts.map((fact) => <span key={fact}>{fact}</span>)}
                      <span className="md:hidden" title={formatDateTimeFull(incident.updated_at)}>
                        updated {formatDateTime(incident.updated_at)}
                      </span>
                    </div>
                  </td>
                  <td className="hidden sm:table-cell">
                    {incident.severity ? (
                      <Pill tone={incidentSeverityTone(incident.severity)} size="sm">
                        {incidentSeverityLabel(incident.severity)}
                      </Pill>
                    ) : (
                      <span className="text-slate-500">—</span>
                    )}
                  </td>
                  <td>
                    <Pill tone={incidentStateTone(incident.state)} size="sm" dot className="whitespace-nowrap">
                      {INCIDENT_STATE_LABEL[incident.state] ?? incident.state}
                    </Pill>
                  </td>
                  <td
                    className="hidden whitespace-nowrap tabular-nums text-slate-400 md:table-cell"
                    title={formatDateTimeFull(incident.updated_at)}
                  >
                    {formatDateTime(incident.updated_at)}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </div>
  );
}
