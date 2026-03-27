'use client';

import Link from 'next/link';

import type { IncidentListItem } from '@/lib/types';
import Panel from '@/components/ui/Panel';

interface IncidentListProps {
  incidents: IncidentListItem[];
}

function stateTone(state: IncidentListItem['state']): string {
  switch (state) {
    case 'resolved':
      return 'badge-success';
    case 'monitoring':
      return 'badge-warning';
    case 'identified':
    case 'investigating':
      return 'badge-danger';
    default:
      return 'badge-default';
  }
}

function formatDate(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return date.toLocaleString();
}

export default function IncidentList({ incidents }: IncidentListProps) {
  return (
    <Panel
      title="Incidents"
      subtitle={`${incidents.length} incident${incidents.length === 1 ? '' : 's'} in view`}
      dotColor="var(--danger)"
    >
      <div className="table-card mt-1">
        {incidents.length === 0 ? (
          <div className="flex flex-col items-center justify-center border border-dashed border-white/[0.08] py-12 text-center">
            <div className="text-sm font-medium text-white">No incidents yet</div>
            <div className="mt-1 text-xs text-slate-500">
              Create a manual incident or enable automatic creation on an alert policy.
            </div>
          </div>
        ) : (
          <table className="data-table text-xs">
            <thead>
              <tr>
                <th>Incident</th>
                <th>State</th>
                <th>Links</th>
                <th>Published</th>
                <th>Updated</th>
                <th className="text-right">Actions</th>
              </tr>
            </thead>
            <tbody>
              {incidents.map((incident, index) => (
                <tr key={incident.id} className={index % 2 === 1 ? 'bg-[rgba(15,23,42,0.35)]' : ''}>
                  <td>
                    <div className="text-sm font-medium text-white">{incident.title}</div>
                    <div className="mt-1 flex items-center gap-2 text-[0.7rem] text-slate-500">
                      <span className={`badge ${incident.source === 'auto' ? 'badge-warning' : 'badge-default'}`}>
                        {incident.source === 'auto' ? 'Auto' : 'Manual'}
                      </span>
                      {incident.resolved_at ? (
                        <span>Resolved {formatDate(incident.resolved_at)}</span>
                      ) : null}
                    </div>
                  </td>
                  <td>
                    <span className={`badge ${stateTone(incident.state)}`}>
                      {incident.state}
                    </span>
                  </td>
                  <td className="text-slate-400">
                    {incident.linked_alert_count} alerts / {incident.linked_monitor_count} monitors
                  </td>
                  <td className="text-slate-400">
                    {incident.publication_count}
                  </td>
                  <td className="text-slate-400">
                    {formatDate(incident.updated_at)}
                  </td>
                  <td className="text-right">
                    <Link href={`/incidents/${incident.id}`} className="inline-flex">
                      <button className="btn btn-secondary btn-xs">
                        Open
                      </button>
                    </Link>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </Panel>
  );
}
