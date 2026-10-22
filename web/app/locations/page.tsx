'use client';

import { useCallback, useMemo, useRef, useState } from 'react';
import { MapPin, Plus, Rocket, Pencil, Trash2 } from 'lucide-react';
import Button from '@/components/ui/Button';
import IconButton from '@/components/ui/IconButton';
import Callout from '@/components/ui/Callout';
import PageHeader from '@/components/ui/PageHeader';
import Page from '@/components/ui/Page';
import EmptyState from '@/components/ui/EmptyState';
import TableSkeleton from '@/components/ui/TableSkeleton';
import LiveStatus from '@/components/ui/LiveStatus';
import { RelativeTime } from '@/components/ui/RelativeTime';
import SortableHeader, { useUrlSort } from '@/components/ui/SortableHeader';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import { useToast } from '@/components/ui/ToastProvider';
import { useCanWrite } from '@/components/providers/CurrentUserProvider';
import { LocationDeployModal } from '@/components/locations/LocationDeployModal';
import { LocationFormModal } from '@/components/locations/LocationFormModal';
import MeshMatrix from '@/components/locations/MeshMatrix';
import Tabs, { useUrlTab } from '@/components/ui/Tabs';
import { Location } from '@/lib/types';
import { deleteLocation, getLocations } from '@/lib/api';
import { pluralize } from '@/lib/format';
import { sortRows, type SortState, type SortValue } from '@/lib/sort';
import { useLiveRefresh } from '@/lib/useLiveRefresh';

const REFRESH_MS = 30_000;

const SORT_KEYS = ['name', 'status', 'endpoint', 'monitors', 'last_seen'] as const;
type LocationSortKey = (typeof SORT_KEYS)[number];
const DEFAULT_SORT: SortState<LocationSortKey> = { key: 'name', dir: 'asc' };
// Most monitors first; Status ascending is worst first, Last seen stalest first.
const DEFAULT_DIR = { monitors: 'desc' } as const;

// Worst first, in the order ConnectionStatus words them: stranded monitors,
// offline, never connected, connected.
function connectionRank(location: Location): number {
  if (location.connected) return 3;
  if (!location.last_seen_at) return 2;
  return location.monitor_count > 0 ? 0 : 1;
}

const SORT_VALUE: Record<LocationSortKey, (location: Location) => SortValue> = {
  name: (location) => location.name,
  status: connectionRank,
  endpoint: (location) => location.mesh_endpoint || null,
  monitors: (location) => location.monitor_count,
  last_seen: (location) => (location.last_seen_at ? Date.parse(location.last_seen_at) : null),
};

function StatusDot({ className }: { className: string }) {
  return <span className={`h-2 w-2 flex-shrink-0 rounded-full ${className}`} aria-hidden="true" />;
}

// Rose is for something failing now: a disconnected location only alarms when
// monitors depend on it. An unused one is just offline, and one that never
// connected needs a worker, so that is the cell's next step.
function ConnectionStatus({ location, onDeploy }: { location: Location; onDeploy?: () => void }) {
  if (location.connected) {
    return (
      <span className="inline-flex items-center gap-2 whitespace-nowrap text-ui text-slate-300">
        <StatusDot className="bg-emerald-500" />
        Connected
      </span>
    );
  }
  if (!location.last_seen_at) {
    return onDeploy ? (
      <Button variant="subtle" size="xs" icon={<Rocket strokeWidth={1.75} />} onClick={onDeploy} className="-ml-2.5">
        Deploy worker
      </Button>
    ) : (
      <span className="inline-flex items-center gap-2 whitespace-nowrap text-ui text-slate-400">
        <StatusDot className="bg-slate-500" />
        Never connected
      </span>
    );
  }
  if (location.monitor_count > 0) {
    return (
      <span className="inline-flex flex-col">
        <span className="inline-flex items-center gap-2 whitespace-nowrap text-ui text-rose-300">
          <StatusDot className="bg-rose-500" />
          Disconnected
        </span>
        <span className="mt-0.5 pl-4 text-xs text-slate-400">
          {pluralize(location.monitor_count, 'monitor')} stranded
        </span>
      </span>
    );
  }
  return (
    <span className="inline-flex items-center gap-2 whitespace-nowrap text-ui text-slate-400">
      <StatusDot className="bg-slate-500" />
      Offline
    </span>
  );
}

export default function LocationsPage() {
  const { showToast } = useToast();
  const canManageLocations = useCanWrite();
  const [locations, setLocations] = useState<Location[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  // undefined = closed, null = create, a location = edit.
  const [formLocation, setFormLocation] = useState<Location | null | undefined>(undefined);
  const [pendingDelete, setPendingDelete] = useState<Location | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [deployLocation, setDeployLocation] = useState<Location | null>(null);
  const initialLoadDone = useRef(false);
  const [tab, setTab] = useUrlTab(['locations', 'mesh'] as const);
  const [sort, setSort] = useUrlSort(SORT_KEYS, DEFAULT_SORT, DEFAULT_DIR);

  // `silent` loads (the poll, a tab return) keep the last good list and stay
  // quiet on failure; LiveStatus says the update failed instead.
  const loadLocations = useCallback(async ({ silent }: { silent: boolean }) => {
    try {
      if (!silent && !initialLoadDone.current) setLoading(true);
      const response = await getLocations({ page_size: 100 });
      setLocations(response.items || []);
      setError('');
      return true;
    } catch (err: any) {
      if (!silent || !initialLoadDone.current) {
        setError(err.message || 'Failed to load locations');
      }
      return false;
    } finally {
      initialLoadDone.current = true;
      setLoading(false);
    }
  }, []);

  // Connected status refreshes every 30s while the tab is visible; worker
  // connections are not on the alert stream, so stream events don't reload.
  const live = useLiveRefresh(loadLocations, { intervalMs: REFRESH_MS, streamDebounceMs: 0 });
  const sorted = useMemo(
    () => sortRows(locations, SORT_VALUE[sort.key], sort.dir, (location) => location.name),
    [locations, sort]
  );

  const handleSaved = async (saved: Location) => {
    const created = formLocation === null;
    setFormLocation(undefined);
    if (created) {
      showToast('Location created', 'success');
      await loadLocations({ silent: true });
      setDeployLocation(saved);
    } else {
      setLocations((prev) => prev.map((item) => (item.id === saved.id ? saved : item)));
      showToast('Location updated', 'success');
    }
  };

  const handleDelete = async () => {
    if (!pendingDelete) return;
    setDeleting(true);
    try {
      const { monitors_detached } = await deleteLocation(pendingDelete.id);
      setLocations((prev) => prev.filter((item) => item.id !== pendingDelete.id));
      showToast(
        monitors_detached > 0
          ? `Location deleted — detached from ${monitors_detached} monitor${monitors_detached === 1 ? '' : 's'}`
          : 'Location deleted',
        'success'
      );
      setPendingDelete(null);
    } catch (err: any) {
      showToast(err.message || 'Failed to delete location', 'error');
    } finally {
      setDeleting(false);
    }
  };

  const openCreate = () => setFormLocation(null);
  const isEmpty = !loading && !error && locations.length === 0;

  return (
    <Page width="data">
      <PageHeader
        title="Locations"
        subtitle="Run checks from your own networks through private workers, and probe between them."
        action={
          tab === 'locations' && canManageLocations && !isEmpty ? (
            <Button variant="accent" size="sm" icon={<Plus strokeWidth={1.75} />} onClick={openCreate}>
              New location
            </Button>
          ) : undefined
        }
      />

      <Tabs
        tabs={[
          { id: 'locations', label: 'Locations', count: loading ? undefined : locations.length },
          { id: 'mesh', label: 'Mesh' },
        ]}
        active={tab}
        onChange={setTab}
        trailing={
          tab === 'locations' && live.updatedAt !== null ? (
            // A manual refresh (after deploying a worker) skips the 30s wait;
            // the list stays on screen meanwhile.
            <LiveStatus
              updatedAt={live.updatedAt}
              refreshing={live.refreshing}
              failed={live.failed && !error}
              onRefresh={live.refresh}
              refreshLabel="Refresh locations"
            />
          ) : undefined
        }
      />

      {tab === 'mesh' && <MeshMatrix />}

      {tab === 'locations' && (
        loading ? (
          <TableSkeleton rows={3} columns={4} label="Loading locations" />
        ) : error ? (
          <Callout
            tone="danger"
            action={
              <Button variant="ghost" size="xs" onClick={live.refresh} loading={live.refreshing}>
                Retry
              </Button>
            }
          >
            {error}
          </Callout>
        ) : locations.length === 0 ? (
          <EmptyState
            icon={<MapPin strokeWidth={1.5} />}
            title="No private locations yet"
            description="Create a location, then deploy a worker inside that network. Monitors assigned to it run their checks from there."
            action={
              canManageLocations
                ? { label: 'New location', onClick: openCreate, icon: <Plus strokeWidth={1.75} /> }
                : undefined
            }
          />
        ) : (
          <div className="glass overflow-hidden">
            <div className="overflow-x-auto">
              <table className="data-table">
                <thead>
                  <tr>
                    <SortableHeader label="Name" sortKey="name" sort={sort} onSort={setSort} defaultDir={DEFAULT_DIR} />
                    <SortableHeader
                      label="Status"
                      sortKey="status"
                      sort={sort}
                      onSort={setSort}
                      defaultDir={DEFAULT_DIR}
                      title="Disconnected first"
                    />
                    <SortableHeader label="Mesh endpoint" sortKey="endpoint" sort={sort} onSort={setSort} defaultDir={DEFAULT_DIR} />
                    <SortableHeader
                      label="Monitors"
                      sortKey="monitors"
                      sort={sort}
                      onSort={setSort}
                      defaultDir={DEFAULT_DIR}
                      align="right"
                      title="Most monitors first"
                    />
                    <SortableHeader
                      label="Last seen"
                      sortKey="last_seen"
                      sort={sort}
                      onSort={setSort}
                      defaultDir={DEFAULT_DIR}
                      title="Longest unseen first"
                    />
                    <th><span className="sr-only">Actions</span></th>
                  </tr>
                </thead>
                <tbody className="text-sm text-slate-200">
                  {sorted.map((location) => {
                    // The slug is derived from the name, so it only goes in
                    // the tooltip; the description shows when it adds something.
                    const description =
                      location.description && location.description.trim() !== location.name.trim()
                        ? location.description
                        : '';
                    const neverConnected = !location.connected && !location.last_seen_at;
                    return (
                      <tr key={location.id}>
                        <td className="max-w-[18rem]">
                          <p className="truncate font-medium text-white" title={`${location.name} (${location.slug})`}>
                            {location.name}
                          </p>
                          {description && (
                            <p className="mt-0.5 truncate text-xs text-slate-500" title={description}>
                              {description}
                            </p>
                          )}
                        </td>
                        <td>
                          <ConnectionStatus
                            location={location}
                            onDeploy={canManageLocations ? () => setDeployLocation(location) : undefined}
                          />
                        </td>
                        <td>
                          {location.mesh_endpoint ? (
                            <span className="whitespace-nowrap font-mono text-xs text-slate-400">{location.mesh_endpoint}</span>
                          ) : (
                            <span className="text-xs text-slate-500">—</span>
                          )}
                        </td>
                        <td className="text-right text-ui tabular-nums text-slate-300">
                          {location.monitor_count}
                        </td>
                        <td className="whitespace-nowrap text-ui text-slate-400">
                          <RelativeTime value={location.last_seen_at} />
                        </td>
                        <td className="!py-2">
                          {canManageLocations && <div className="flex items-center justify-end gap-0.5">
                            {/* A never-connected row offers "Deploy worker" in its status cell. */}
                            {!neverConnected && (
                              <IconButton
                                icon={<Rocket strokeWidth={1.75} />}
                                label="Deploy a worker"
                                onClick={() => setDeployLocation(location)}
                              />
                            )}
                            <IconButton
                              icon={<Pencil strokeWidth={1.75} />}
                              label="Edit"
                              onClick={() => setFormLocation(location)}
                            />
                            <IconButton
                              icon={<Trash2 strokeWidth={1.75} />}
                              label="Delete"
                              onClick={() => setPendingDelete(location)}
                              danger
                            />
                          </div>}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          </div>
        )
      )}

      {canManageLocations && formLocation !== undefined && (
        <LocationFormModal
          location={formLocation ?? undefined}
          onDone={handleSaved}
          onCancel={() => setFormLocation(undefined)}
        />
      )}

      <ConfirmDialog
        open={canManageLocations && pendingDelete !== null}
        title="Delete location"
        description={
          pendingDelete
            ? pendingDelete.monitor_count > 0
              ? `This location is used by ${pendingDelete.monitor_count} monitor${pendingDelete.monitor_count === 1 ? '' : 's'}; they will fall back to their remaining locations or the default fleet. Workers deployed for “${pendingDelete.name}” will stop receiving checks.`
              : `“${pendingDelete.name}” will be removed. Workers deployed for it will stop receiving checks.`
            : undefined
        }
        confirmLabel="Delete location"
        loading={deleting}
        onConfirm={handleDelete}
        onCancel={() => !deleting && setPendingDelete(null)}
      />

      {canManageLocations && deployLocation && (
        <LocationDeployModal location={deployLocation} onClose={() => setDeployLocation(null)} />
      )}
    </Page>
  );
}
