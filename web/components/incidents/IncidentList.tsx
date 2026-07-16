'use client';

import Link from 'next/link';

import type { IncidentListItem } from '@/lib/types';
import { formatDateTime, pluralize } from '@/lib/format';
import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
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
  return (
    <Panel
      title="Incidents"
      subtitle={`${pluralize(incidents.length, 'incident')} in view`}
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
                      <Pill tone={incident.source === 'auto' ? 'warning' : 'neutral'} size="xs">
                        {incident.source === 'auto' ? 'Auto' : 'Manual'}
                      </Pill>
                      {incident.resolved_at ? (
                        <span>Resolved {formatDateTime(incident.resolved_at)}</span>
                      ) : null}
                    </div>
                  </td>
                  <td>
                    <Pill tone={stateTone(incident.state)} size="xs" dot className="capitalize">
                      {incident.state}
                    </Pill>
                  </td>
                  <td className="text-slate-400">
                    {incident.linked_alert_count} alerts / {incident.linked_monitor_count} monitors
                  </td>
                  <td className="text-slate-400">
                    {incident.publication_count}
                  </td>
                  <td className="text-slate-400">
                    {formatDateTime(incident.updated_at)}
                  </td>
                  <td className="text-right">
                    <Button variant="ghost" size="xs" asChild>
                      <Link href={`/incidents/${incident.id}`}>Open</Link>
                    </Button>
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
