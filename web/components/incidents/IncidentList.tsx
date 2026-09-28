'use client';

import Link from 'next/link';
import { Siren } from 'lucide-react';

import type { IncidentListItem } from '@/lib/types';
import { formatDateTime, pluralize } from '@/lib/format';
import EmptyState from '@/components/ui/EmptyState';
import Pill, { PillTone } from '@/components/ui/Pill';

interface IncidentListProps {
  incidents: IncidentListItem[];
}

export function incidentStateTone(state: IncidentListItem['state']): PillTone {
  switch (state) {
    case 'resolved':
      return 'success';
    case 'monitoring':
      return 'warning';
    case 'identified':
    case 'investigating':
      return 'danger';
    default:
      return 'neutral';
  }
}

export default function IncidentList({ incidents }: IncidentListProps) {
  if (incidents.length === 0) {
    return (
      <EmptyState
        icon={<Siren strokeWidth={1.75} />}
        title="No incidents yet"
        description="Create one manually, or have monitors open incidents automatically in Settings."
      />
    );
  }

  return (
    <div className="glass overflow-hidden">
      <div className="overflow-x-auto">
        <table className="data-table text-[13px]">
          <thead>
            <tr>
              <th className="w-full">Incident</th>
              <th>State</th>
              <th className="hidden md:table-cell">Links</th>
              <th className="hidden md:table-cell">Published</th>
              <th className="hidden sm:table-cell">Updated</th>
            </tr>
          </thead>
          <tbody>
            {incidents.map((incident) => {
              const links = [
                incident.linked_alert_count > 0 ? pluralize(incident.linked_alert_count, 'alert') : null,
                incident.linked_monitor_count > 0 ? pluralize(incident.linked_monitor_count, 'monitor') : null,
              ].filter(Boolean);
              return (
                <tr key={incident.id} className="transition-colors hover:bg-well">
                  <td>
                    <Link
                      href={`/incidents/${incident.id}`}
                      className="text-sm font-medium text-white transition-colors hover:text-cyan-300"
                    >
                      {incident.title}
                    </Link>
                    {(incident.source === 'auto' || incident.resolved_at) && (
                      <div className="mt-1 flex items-center gap-2 text-xs text-slate-500">
                        {incident.source === 'auto' && (
                          <Pill tone="neutral" size="xs">Auto</Pill>
                        )}
                        {incident.resolved_at ? (
                          <span>Resolved {formatDateTime(incident.resolved_at)}</span>
                        ) : null}
                      </div>
                    )}
                    {!incident.resolved_at && (
                      <div className="mt-1 text-xs text-slate-500 sm:hidden">
                        Updated {formatDateTime(incident.updated_at)}
                      </div>
                    )}
                  </td>
                  <td>
                    <Pill tone={incidentStateTone(incident.state)} size="xs" dot className="capitalize">
                      {incident.state}
                    </Pill>
                  </td>
                  <td className="hidden whitespace-nowrap text-slate-400 md:table-cell">{links.length > 0 ? links.join(' · ') : '—'}</td>
                  <td className="hidden whitespace-nowrap text-slate-400 md:table-cell">
                    {incident.publication_count > 0 ? pluralize(incident.publication_count, 'page') : '—'}
                  </td>
                  <td className="hidden whitespace-nowrap text-slate-400 sm:table-cell">
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
