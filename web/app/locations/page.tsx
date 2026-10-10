'use client';

import { useEffect, useRef, useState } from 'react';
import { MapPin, Plus, Rocket, Pencil, RefreshCw, Trash2 } from 'lucide-react';
import Button from '@/components/ui/Button';
import IconButton from '@/components/ui/IconButton';
import Callout from '@/components/ui/Callout';
import PageHeader from '@/components/ui/PageHeader';
import EmptyState from '@/components/ui/EmptyState';
import TableSkeleton from '@/components/ui/TableSkeleton';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import { useToast } from '@/components/ui/ToastProvider';
import { useCanWrite } from '@/components/providers/CurrentUserProvider';
import { LocationDeployModal } from '@/components/locations/LocationDeployModal';
import { LocationFormModal } from '@/components/locations/LocationFormModal';
import MeshMatrix from '@/components/locations/MeshMatrix';
import Tabs, { useUrlTab } from '@/components/ui/Tabs';
import { Location } from '@/lib/types';
import { deleteLocation, getLocations } from '@/lib/api';
import { formatDateTimeFull, formatRelativeTime, pluralize } from '@/lib/format';

const REFRESH_MS = 30_000;

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
  const [lastLoadedAt, setLastLoadedAt] = useState<number | null>(null);
  const [refreshing, setRefreshing] = useState(false);
  const [now, setNow] = useState(() => Date.now());
  // undefined = closed, null = create, a location = edit.
  const [formLocation, setFormLocation] = useState<Location | null | undefined>(undefined);
  const [pendingDelete, setPendingDelete] = useState<Location | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [deployLocation, setDeployLocation] = useState<Location | null>(null);
  const initialLoadDone = useRef(false);
  const [tab, setTab] = useUrlTab(['locations', 'mesh'] as const);

  const loadLocations = async (showSpinner = true) => {
    try {
      if (showSpinner) setLoading(true);
      const response = await getLocations({ page_size: 100 });
      setLocations(response.items || []);
      setLastLoadedAt(Date.now());
      setError('');
    } catch (err: any) {
      // Background refreshes keep showing the last good list on failure.
      if (showSpinner || !initialLoadDone.current) {
        setError(err.message || 'Failed to load locations');
      }
      if (!initialLoadDone.current) setLocations([]);
    } finally {
      initialLoadDone.current = true;
      if (showSpinner) setLoading(false);
    }
  };

  useEffect(() => {
    loadLocations();
    // Refresh connected status while the page is open; the tick keeps the
    // "updated …" caption honest between refreshes.
    const interval = setInterval(() => loadLocations(false), REFRESH_MS);
    const tick = setInterval(() => setNow(Date.now()), 10_000);
    return () => {
      clearInterval(interval);
      clearInterval(tick);
    };
  }, []);

  const handleSaved = async (saved: Location) => {
    const created = formLocation === null;
    setFormLocation(undefined);
    if (created) {
      showToast('Location created', 'success');
      await loadLocations(false);
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

  // A manual refresh (after deploying a worker) without the 30s wait; the
  // list stays on screen meanwhile.
  const refreshLocations = async () => {
    setRefreshing(true);
    try {
      await loadLocations(false);
    } finally {
      setRefreshing(false);
    }
  };

  const openCreate = () => setFormLocation(null);
  const isEmpty = !loading && !error && locations.length === 0;

  return (
    <div className="space-y-6">
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
          tab === 'locations' && lastLoadedAt !== null ? (
            <>
              <span className="text-xs text-slate-500" title={formatDateTimeFull(lastLoadedAt)}>
                Updated {formatRelativeTime(lastLoadedAt, new Date(Math.max(now, lastLoadedAt)))}
              </span>
              <IconButton
                icon={<RefreshCw strokeWidth={1.75} />}
                label="Refresh locations"
                onClick={refreshLocations}
                loading={refreshing}
              />
            </>
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
            action={<Button variant="ghost" size="xs" onClick={() => loadLocations()}>Retry</Button>}
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
                    <th>Name</th>
                    <th>Status</th>
                    <th>Mesh endpoint</th>
                    <th className="text-right">Monitors</th>
                    <th>Last seen</th>
                    <th><span className="sr-only">Actions</span></th>
                  </tr>
                </thead>
                <tbody className="text-sm text-slate-200">
                  {locations.map((location) => {
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
                          {location.last_seen_at ? (
                            <span title={formatDateTimeFull(location.last_seen_at)}>
                              {formatRelativeTime(location.last_seen_at)}
                            </span>
                          ) : (
                            '—'
                          )}
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
    </div>
  );
}
