'use client';

import { useEffect, useRef, useState } from 'react';
import Link from 'next/link';
import { Network, RefreshCw, Zap } from 'lucide-react';
import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
import Pill from '@/components/ui/Pill';
import PageHeader from '@/components/ui/PageHeader';
import EmptyState from '@/components/ui/EmptyState';
import { useToast } from '@/components/ui/ToastProvider';
import { MeshEdge, MeshLocation, MeshResponse } from '@/lib/types';
import { getMesh, probeMeshEdge } from '@/lib/api';
import { formatTimeAgo } from '@/lib/monitor-utils';

function edgeKey(sourceId: string, targetId: string) {
  return `${sourceId}→${targetId}`;
}

// Cell background/text classes per edge state, following the shared
// emerald/amber/rose/slate convention (see monitorStateColors).
function cellClasses(edge?: MeshEdge): string {
  if (!edge || edge.stale) {
    return 'bg-slate-500/10 text-slate-400 border-slate-500/20';
  }
  switch (edge.state) {
    case 'up':
      return 'bg-emerald-500/10 text-emerald-300 border-emerald-500/25';
    case 'suspect':
      return 'bg-amber-500/10 text-amber-300 border-amber-500/30';
    case 'down':
      return 'bg-rose-500/15 text-rose-300 border-rose-500/40';
    default:
      return 'bg-slate-500/10 text-slate-400 border-slate-500/20';
  }
}

function edgeTitle(source: MeshLocation, target: MeshLocation, edge?: MeshEdge): string {
  const lines = [`${source.name} → ${target.name}`];
  if (!edge) {
    lines.push('No probes yet');
    return lines.join('\n');
  }
  lines.push(`State: ${edge.stale ? 'stale' : edge.state}`);
  if (edge.last_latency_ms != null) lines.push(`Latency: ${edge.last_latency_ms}ms`);
  if (edge.last_check_at) lines.push(`Last probe: ${formatTimeAgo(edge.last_check_at)}`);
  if (edge.last_error) lines.push(`Error: ${edge.last_error}`);
  if (edge.alert_open) lines.push('Alert open');
  return lines.join('\n');
}

function cellLabel(edge?: MeshEdge): string {
  if (!edge) return '—';
  if (edge.stale) return 'stale';
  if (edge.state === 'up' || edge.state === 'suspect') {
    return edge.last_latency_ms != null ? `${edge.last_latency_ms}ms` : edge.state;
  }
  if (edge.state === 'down') return 'down';
  return '—';
}

export default function MeshPage() {
  const { showToast } = useToast();
  const [mesh, setMesh] = useState<MeshResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [probing, setProbing] = useState<string | null>(null);
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
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const handleProbe = async (source: MeshLocation, target: MeshLocation) => {
    const key = edgeKey(source.id, target.id);
    setProbing(key);
    try {
      const result = await probeMeshEdge(source.id, target.id);
      if (result.status === 'success') {
        showToast(
          `${source.name} → ${target.name}: ${result.latency_ms != null ? `${result.latency_ms}ms` : 'reachable'}`,
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

  const locations = mesh?.locations ?? [];
  const edges = new Map((mesh?.edges ?? []).map((e) => [edgeKey(e.source_location_id, e.target_location_id), e]));
  const downCount = (mesh?.edges ?? []).filter((e) => !e.stale && e.state === 'down').length;

  return (
    <div className="space-y-6">
      <PageHeader
        title="Connectivity mesh"
        subtitle="Every location probes every other location's echo endpoint — rows are the probing side, columns the target"
        action={
          downCount > 0 ? (
            <Pill tone="danger" size="sm" dot>
              {downCount} path{downCount === 1 ? '' : 's'} down
            </Pill>
          ) : undefined
        }
      />

      <Panel
        title="Location matrix"
        subtitle={
          mesh
            ? `Probed every ${mesh.probe_interval_seconds}s from each source location`
            : undefined
        }
        actions={(
          <Button
            variant="ghost"
            size="sm"
            icon={<RefreshCw strokeWidth={1.75} />}
            onClick={() => loadMesh()}
          >
            Refresh
          </Button>
        )}
      >
        {loading ? (
          <div className="text-sm text-slate-500">Loading mesh…</div>
        ) : error ? (
          <div className="space-y-3">
            <p className="text-sm text-rose-400">{error}</p>
            <Button variant="ghost" size="sm" onClick={() => loadMesh()}>Retry</Button>
          </div>
        ) : locations.length < 2 ? (
          <EmptyState
            icon={<Network strokeWidth={1.5} />}
            title="Not enough mesh locations"
            description="The mesh needs at least two locations with a mesh endpoint configured. Set each location's endpoint (host:port reachable from your other networks) on the Locations page."
            action={
              <Link href="/locations">
                <Button variant="accent" size="sm">Go to Locations</Button>
              </Link>
            }
          />
        ) : (
          <div className="overflow-x-auto">
            <table className="min-w-full border-separate border-spacing-1">
              <thead>
                <tr>
                  <th className="px-3 py-2 text-left text-xs font-medium uppercase tracking-wide text-slate-400">
                    From \ To
                  </th>
                  {locations.map((target) => (
                    <th
                      key={target.id}
                      className="px-3 py-2 text-center text-xs font-medium text-slate-300"
                    >
                      <div className="flex items-center justify-center gap-1.5">
                        <span
                          className={`h-1.5 w-1.5 rounded-full ${target.connected ? 'bg-emerald-500' : 'bg-slate-600'}`}
                        />
                        {target.name}
                      </div>
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {locations.map((source) => (
                  <tr key={source.id} className={source.connected ? '' : 'opacity-50'}>
                    <td className="whitespace-nowrap px-3 py-2 text-sm font-medium text-slate-200">
                      <div className="flex items-center gap-1.5">
                        <span
                          className={`h-1.5 w-1.5 rounded-full ${source.connected ? 'bg-emerald-500' : 'bg-slate-600'}`}
                        />
                        {source.name}
                        {!source.connected && (
                          <span className="text-xs font-normal text-slate-500">worker offline</span>
                        )}
                      </div>
                    </td>
                    {locations.map((target) => {
                      if (source.id === target.id) {
                        return (
                          <td key={target.id} className="px-3 py-2 text-center text-xs text-slate-600">
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
                            className={`group relative flex min-w-[6.5rem] items-center justify-center gap-1 rounded-md border px-3 py-2.5 text-center text-sm tabular-nums ${cellClasses(edge)}`}
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
                              className="absolute right-1 top-1 hidden rounded p-0.5 text-slate-400 hover:text-white group-hover:block disabled:opacity-50"
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
            <p className="mt-3 text-xs text-slate-500">
              Cells show the last measured latency of the directed path. Amber = confirming a
              possible outage, rose = path down (alerting), grey = no recent probes (source worker
              offline or edge not yet measured). Hover a cell to probe on demand.
            </p>
          </div>
        )}
      </Panel>
    </div>
  );
}
