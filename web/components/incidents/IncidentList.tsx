'use client';

import Link from 'next/link';

import type { IncidentListItem } from '@/lib/types';
import { formatDateTime, pluralize } from '@/lib/format';
import EmptyState from '@/components/ui/EmptyState';
import Pill, { PillTone } from '@/components/ui/Pill';

interface IncidentListProps {
  incidents: IncidentListItem[];
}

function stateTone(state: IncidentListItem['state']): PillTone {
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
        title="No incidents yet"
        description="Create one manually, or have monitors open incidents automatically in Settings."
      />
    );
  }

  return (
    <div className="table-card">
      <table className="data-table text-xs">
        <thead>
          <tr>
            <th>Incident</th>
            <th>State</th>
            <th>Links</th>
            <th>Published</th>
            <th>Updated</th>
          </tr>
        </thead>
        <tbody>
          {incidents.map((incident, index) => {
            const links = [
              incident.linked_alert_count > 0 ? pluralize(incident.linked_alert_count, 'alert') : null,
              incident.linked_monitor_count > 0 ? pluralize(incident.linked_monitor_count, 'monitor') : null,
            ].filter(Boolean);
            return (
              <tr key={incident.id} className={index % 2 === 1 ? 'bg-slate-900/[0.35]' : ''}>
                <td>
                  <Link
                    href={`/incidents/${incident.id}`}
                    className="text-sm font-medium text-white hover:text-cyan-400"
                  >
                    {incident.title}
                  </Link>
                  {(incident.source === 'auto' || incident.resolved_at) && (
                    <div className="mt-1 flex items-center gap-2 text-[0.7rem] text-slate-500">
                      {incident.source === 'auto' && (
                        <Pill tone="warning" size="xs">Auto</Pill>
                      )}
                      {incident.resolved_at ? (
                        <span>Resolved {formatDateTime(incident.resolved_at)}</span>
                      ) : null}
                    </div>
                  )}
                </td>
                <td>
                  <Pill tone={stateTone(incident.state)} size="xs" dot className="capitalize">
                    {incident.state}
                  </Pill>
                </td>
                <td className="text-slate-400">{links.length > 0 ? links.join(' · ') : '—'}</td>
                <td className="text-slate-400">
                  {incident.publication_count > 0 ? pluralize(incident.publication_count, 'page') : '—'}
                </td>
                <td className="text-slate-400">
                  {formatDateTime(incident.updated_at)}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
