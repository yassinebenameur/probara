'use client';

import { useEffect, useRef, useState } from 'react';
import { Network, RefreshCw, Zap } from 'lucide-react';
import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
import IconButton from '@/components/ui/IconButton';
import Pill from '@/components/ui/Pill';
import Callout from '@/components/ui/Callout';
import EmptyState from '@/components/ui/EmptyState';
import TableSkeleton from '@/components/ui/TableSkeleton';
import { useToast } from '@/components/ui/ToastProvider';
import { MeshEdge, MeshLocation, MeshResponse } from '@/lib/types';
import { getMesh, probeMeshEdge } from '@/lib/api';
import { formatTimeAgo } from '@/lib/monitor-utils';
import { formatLatency } from '@/lib/format';

function edgeKey(sourceId: string, targetId: string) {
  return `${sourceId}→${targetId}`;
}

// Cell background/text classes per edge state, following the shared
// emerald/amber/rose/slate convention (see monitorStateColors).
function cellClasses(edge?: MeshEdge): string {
  if (!edge || edge.stale) {
    return 'bg-well-hover text-slate-400';
  }
  switch (edge.state) {
    case 'up':
      return 'bg-emerald-500/[0.12] text-emerald-300';
    case 'suspect':
      return 'bg-amber-500/[0.15] text-amber-300';
    case 'down':
      return 'bg-rose-500/20 text-rose-300';
    default:
      return 'bg-well-hover text-slate-400';
  }
}

function edgeTitle(source: MeshLocation, target: MeshLocation, edge?: MeshEdge): string {
  const lines = [`${source.name} → ${target.name}`];
  if (!edge) {
    lines.push('No probes yet');
    return lines.join('\n');
  }
  lines.push(`State: ${edge.stale ? 'stale' : edge.state}`);
  if (edge.last_latency_ms != null) lines.push(`Latency: ${formatLatency(edge.last_latency_ms)}`);
  if (edge.last_check_at) lines.push(`Last probe: ${formatTimeAgo(edge.last_check_at)}`);
  if (edge.last_error) lines.push(`Error: ${edge.last_error}`);
  if (edge.alert_open) lines.push('Alert open');
  return lines.join('\n');
}

function cellLabel(edge?: MeshEdge): string {
  if (!edge) return '—';
  if (edge.stale) return 'stale';
  if (edge.state === 'up' || edge.state === 'suspect') {
    return edge.last_latency_ms != null ? formatLatency(edge.last_latency_ms) : edge.state;
  }
  if (edge.state === 'down') return 'down';
  return '—';
}

export default function MeshMatrix() {
  const { showToast } = useToast();
  const [mesh, setMesh] = useState<MeshResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [probing, setProbing] = useState<string | null>(null);
  const [refreshing, setRefreshing] = useState(false);
  const initialLoadDone = useRef(false);

  const loadMesh = async (showSpinner = true) => {
    try {
      if (showSpinner) setLoading(true);
      const response = await getMesh();
      setMesh(response);
      setError('');
    } catch (err: any) {
      // Background refreshes keep showing the last good matrix on failure.
      if (showSpinner || !initialLoadDone.current) {
        setError(err.message || 'Failed to load mesh');
      }
    } finally {
      initialLoadDone.current = true;
      if (showSpinner) setLoading(false);
    }
  };

  useEffect(() => {
    loadMesh();
    const interval = setInterval(() => loadMesh(false), 30_000);
    return () => clearInterval(interval);
  }, []);

  const handleProbe = async (source: MeshLocation, target: MeshLocation) => {
    const key = edgeKey(source.id, target.id);
    setProbing(key);
    try {
      const result = await probeMeshEdge(source.id, target.id);
      if (result.status === 'success') {
        showToast(
          `${source.name} → ${target.name}: ${result.latency_ms != null ? formatLatency(result.latency_ms) : 'reachable'}`,
          'success'
        );
      } else {
        showToast(
          `${source.name} → ${target.name}: ${result.error_message || result.status}`,
          'error'
        );
      }
      await loadMesh(false);
    } catch (err: any) {
      showToast(err.message || 'Probe failed', 'error');
    } finally {
      setProbing(null);
    }
  };

  // Manual refresh without the 30s wait; the matrix stays on screen.
  const refreshMesh = async () => {
    setRefreshing(true);
    try {
      await loadMesh(false);
    } finally {
      setRefreshing(false);
    }
  };

  const locations = mesh?.locations ?? [];
  const edges = new Map((mesh?.edges ?? []).map((e) => [edgeKey(e.source_location_id, e.target_location_id), e]));
  const downCount = (mesh?.edges ?? []).filter((e) => !e.stale && e.state === 'down').length;

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-ui text-slate-400">
          Each row probes every column&apos;s echo endpoint
          {mesh ? ` every ${mesh.probe_interval_seconds}s` : ''}; this view refreshes every 30s. Hover a cell to probe on demand.
        </p>
        <div className="flex items-center gap-2">
          {downCount > 0 && (
            <Pill tone="danger" size="sm" dot>
              {downCount} path{downCount === 1 ? '' : 's'} down
            </Pill>
          )}
          <IconButton
            icon={<RefreshCw strokeWidth={1.75} />}
            label="Refresh mesh"
            onClick={refreshMesh}
            loading={refreshing}
          />
        </div>
      </div>

      {loading ? (
        <TableSkeleton rows={3} columns={3} label="Loading mesh" />
      ) : error ? (
        <Callout
          tone="danger"
          action={<Button variant="ghost" size="xs" onClick={() => loadMesh()}>Retry</Button>}
        >
          {error}
        </Callout>
      ) : locations.length < 2 ? (
        <EmptyState
          icon={<Network strokeWidth={1.5} />}
          title="Not enough mesh locations"
          description="The mesh needs at least two enabled locations with a mesh endpoint (host:port reachable from your other networks). Set it with the edit button on the Locations tab."
        />
      ) : (
        <Panel>
          <div className="overflow-x-auto">
            <table
              className="w-full table-fixed border-separate border-spacing-1"
              style={{ minWidth: `${10 + locations.length * 7}rem` }}
            >
              <thead>
                <tr>
                  <th className="w-40 px-3 py-2 text-left text-xs font-medium text-slate-500 sm:w-56">
                    From \ To
                  </th>
                  {locations.map((target) => (
                    <th
                      key={target.id}
                      className="px-3 py-2 text-center text-xs font-medium text-slate-300"
                    >
                      <div className="flex items-center justify-center gap-1.5">
                        <span
                          className={`h-2 w-2 shrink-0 rounded-full ${target.connected ? 'bg-emerald-500' : 'bg-slate-500'}`}
                        />
                        <span className="truncate" title={target.name}>{target.name}</span>
                      </div>
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {locations.map((source) => (
                  <tr key={source.id}>
                    <td
                      className={`whitespace-nowrap px-3 py-2 text-sm font-medium ${source.connected ? 'text-slate-200' : 'text-slate-400'}`}
                    >
                      <div className="flex items-center gap-1.5">
                        <span
                          className={`h-2 w-2 shrink-0 rounded-full ${source.connected ? 'bg-emerald-500' : 'bg-slate-500'}`}
                        />
                        <span className="min-w-0 sm:flex sm:items-center sm:gap-1.5">
                          <span className="block truncate" title={source.name}>{source.name}</span>
                          {!source.connected && (
                            <span className="block shrink-0 text-xs font-normal text-slate-500">worker offline</span>
                          )}
                        </span>
                      </div>
                    </td>
                    {locations.map((target) => {
                      if (source.id === target.id) {
                        return (
                          <td key={target.id} className="px-3 py-2 text-center text-xs text-slate-500">
                            —
                          </td>
                        );
                      }
                      const key = edgeKey(source.id, target.id);
                      const edge = edges.get(key);
                      return (
                        <td key={target.id} className="p-0">
                          <div
                            title={edgeTitle(source, target, edge)}
                            className={`group relative flex items-center justify-center gap-1 rounded-lg px-3 py-2.5 text-center text-sm tabular-nums ${cellClasses(edge)}`}
                          >
                            {edge?.alert_open && (
                              <span className="absolute left-1.5 top-1.5 h-1.5 w-1.5 rounded-full bg-rose-500" />
                            )}
                            {cellLabel(edge)}
                            <button
                              type="button"
                              title={`Probe ${source.name} → ${target.name} now`}
                              onClick={() => handleProbe(source, target)}
                              disabled={probing !== null}
                              className="absolute right-1 top-1 hidden rounded-md p-0.5 text-slate-400 hover:text-white group-hover:block disabled:opacity-50"
                            >
                              <Zap
                                className={`h-3 w-3 ${probing === key ? 'animate-pulse text-amber-400' : ''}`}
                                strokeWidth={1.75}
                              />
                            </button>
                          </div>
                        </td>
                      );
                    })}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <p className="mt-3 text-xs text-slate-500">
            Cells show the last measured latency of the directed path. Amber = confirming a
            possible outage, rose = path down (alerting), grey = no recent probes (source worker
            offline or edge not yet measured).
          </p>
        </Panel>
      )}
    </div>
  );
}
