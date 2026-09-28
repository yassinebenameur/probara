'use client';

import { useEffect, useRef, useState } from 'react';
import { MapPin, Plus, RefreshCw, Rocket, Pencil, Trash2 } from 'lucide-react';
import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
import IconButton from '@/components/ui/IconButton';
import Input from '@/components/ui/Input';
import Callout from '@/components/ui/Callout';
import PageHeader from '@/components/ui/PageHeader';
import EmptyState from '@/components/ui/EmptyState';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import { useToast } from '@/components/ui/ToastProvider';
import { useCurrentUser } from '@/components/providers/CurrentUserProvider';
import { LocationDeployModal } from '@/components/locations/LocationDeployModal';
import MeshMatrix from '@/components/locations/MeshMatrix';
import Tabs, { useUrlTab } from '@/components/ui/Tabs';
import { Location } from '@/lib/types';
import { createLocation, deleteLocation, getLocations, updateLocation } from '@/lib/api';
import { formatTimeAgo } from '@/lib/monitor-utils';

function connectionStatus(location: Location) {
  const [dot, label] = location.connected
    ? ['bg-emerald-500', 'Connected']
    : !location.last_seen_at
      ? ['bg-slate-500', 'Never connected']
      : ['bg-rose-500', 'Disconnected'];
  return (
    <span className="inline-flex items-center gap-2 whitespace-nowrap text-[13px] text-slate-300">
      <span className={`h-2 w-2 rounded-full ${dot}`} aria-hidden="true" />
      {label}
    </span>
  );
}

export default function LocationsPage() {
  const { showToast } = useToast();
  const { canWrite, loading: loadingUser } = useCurrentUser();
  const canManageLocations = canWrite && !loadingUser;
  const [locations, setLocations] = useState<Location[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [showCreate, setShowCreate] = useState(false);
  const [newName, setNewName] = useState('');
  const [newDescription, setNewDescription] = useState('');
  const [newMeshEndpoint, setNewMeshEndpoint] = useState('');
  const [creating, setCreating] = useState(false);
  const [renamingId, setRenamingId] = useState<string | null>(null);
  const [renameValue, setRenameValue] = useState('');
  const [renameMeshValue, setRenameMeshValue] = useState('');
  const [savingRename, setSavingRename] = useState(false);
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
    // Refresh connected status while the page is open.
    const interval = setInterval(() => loadLocations(false), 30_000);
    return () => clearInterval(interval);
  }, []);

  const handleCreate = async (event: React.FormEvent) => {
    event.preventDefault();
    if (!newName.trim()) {
      showToast('Location name is required', 'error');
      return;
    }

    try {
      setCreating(true);
      const created = await createLocation({
        name: newName.trim(),
        ...(newDescription.trim() ? { description: newDescription.trim() } : {}),
        ...(newMeshEndpoint.trim() ? { mesh_endpoint: newMeshEndpoint.trim() } : {}),
      });
      setNewName('');
      setNewDescription('');
      setNewMeshEndpoint('');
      setShowCreate(false);
      showToast('Location created', 'success');
      await loadLocations(false);
      setDeployLocation(created);
    } catch (err: any) {
      showToast(err.message || 'Failed to create location', 'error');
    } finally {
      setCreating(false);
    }
  };

  const startRename = (location: Location) => {
    setRenamingId(location.id);
    setRenameValue(location.name);
    setRenameMeshValue(location.mesh_endpoint || '');
  };

  const handleRename = async () => {
    if (!renamingId) return;
    if (!renameValue.trim()) {
      showToast('Location name is required', 'error');
      return;
    }

    try {
      setSavingRename(true);
      // mesh_endpoint is always sent: an emptied field opts out of the mesh.
      const updated = await updateLocation(renamingId, {
        name: renameValue.trim(),
        mesh_endpoint: renameMeshValue.trim(),
      });
      setLocations((prev) => prev.map((item) => (item.id === updated.id ? updated : item)));
      setRenamingId(null);
      showToast('Location renamed', 'success');
    } catch (err: any) {
      showToast(err.message || 'Failed to rename location', 'error');
    } finally {
      setSavingRename(false);
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

  const addAction = canManageLocations ? (
    <Button
      variant="accent"
      size="sm"
      icon={<Plus strokeWidth={1.75} />}
      onClick={() => setShowCreate(true)}
    >
      Add location
    </Button>
  ) : undefined;

  return (
    <div className="space-y-6">
      <PageHeader
        title="Private locations"
        subtitle="Run checks from your own networks via remote workers"
        action={tab === 'locations' ? (
          <>
            <Button
              variant="ghost"
              size="sm"
              icon={<RefreshCw strokeWidth={1.75} />}
              onClick={() => loadLocations()}
            >
              Refresh
            </Button>
            {addAction}
          </>
        ) : undefined}
      />

      <Tabs
        tabs={[
          { id: 'locations', label: 'Locations' },
          { id: 'mesh', label: 'Mesh' },
        ]}
        active={tab}
        onChange={setTab}
      />

      {tab === 'mesh' && <MeshMatrix />}

      {tab === 'locations' && canManageLocations && showCreate && (
        <Panel title="New location" subtitle="Name the network or site this location represents.">
          <form onSubmit={handleCreate} className="flex flex-col gap-3 lg:flex-row lg:items-end">
            <div className="flex-1">
              <Input
                label="Name"
                type="text"
                value={newName}
                onChange={(event) => setNewName(event.target.value)}
                placeholder="e.g. Paris VPC"
                autoFocus
              />
            </div>
            <div className="flex-1">
              <Input
                label="Description (optional)"
                type="text"
                value={newDescription}
                onChange={(event) => setNewDescription(event.target.value)}
                placeholder="e.g. Production VPC in eu-west-3"
              />
            </div>
            <div className="flex-1 lg:flex-[1.3]">
              <Input
                label="Mesh endpoint (optional)"
                type="text"
                value={newMeshEndpoint}
                onChange={(event) => setNewMeshEndpoint(event.target.value)}
                placeholder="host:port reachable from your other locations"
              />
            </div>
            <div className="flex items-center gap-2 lg:h-10">
              <Button type="submit" variant="accent" size="sm" disabled={creating} loading={creating}>
                {creating ? 'Creating…' : 'Create'}
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={() => setShowCreate(false)}
                disabled={creating}
              >
                Cancel
              </Button>
            </div>
          </form>
        </Panel>
      )}

      {tab === 'locations' && (
        loading ? (
          <div className="glass p-5 text-sm text-slate-500">Loading locations…</div>
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
            description="Create a location, then deploy a worker inside that network. Monitors assigned to it will run their checks from there."
            action={addAction}
          />
        ) : (
          <div className="glass overflow-hidden">
            <div className="overflow-x-auto">
              <table className="data-table">
                <thead>
                  <tr>
                    <th>Name</th>
                    <th>Slug</th>
                    <th>Status</th>
                    <th>Mesh</th>
                    <th>Monitors</th>
                    <th>Last seen</th>
                    <th><span className="sr-only">Actions</span></th>
                  </tr>
                </thead>
                <tbody className="text-sm text-slate-200">
                  {locations.map((location) => (
                    <tr key={location.id}>
                      <td>
                        {canManageLocations && renamingId === location.id ? (
                          <div className="flex items-center gap-1.5">
                            <input
                              type="text"
                              value={renameValue}
                              onChange={(event) => setRenameValue(event.target.value)}
                              onKeyDown={(event) => {
                                if (event.key === 'Enter') {
                                  event.preventDefault();
                                  handleRename();
                                }
                                if (event.key === 'Escape') setRenamingId(null);
                              }}
                              className="input input-sm h-8 w-28 !py-0"
                              autoFocus
                            />
                            <input
                              type="text"
                              value={renameMeshValue}
                              onChange={(event) => setRenameMeshValue(event.target.value)}
                              onKeyDown={(event) => {
                                if (event.key === 'Enter') {
                                  event.preventDefault();
                                  handleRename();
                                }
                                if (event.key === 'Escape') setRenamingId(null);
                              }}
                              placeholder="Mesh endpoint host:port (empty = off)"
                              className="input input-sm h-8 w-40 !py-0"
                            />
                            <Button
                              variant="accent"
                              size="xs"
                              className="!h-8"
                              onClick={handleRename}
                              disabled={savingRename}
                              loading={savingRename}
                            >
                              Save
                            </Button>
                            <Button
                              variant="ghost"
                              size="xs"
                              className="!h-8"
                              onClick={() => setRenamingId(null)}
                              disabled={savingRename}
                            >
                              Cancel
                            </Button>
                          </div>
                        ) : (
                          <div>
                            <p className="whitespace-nowrap font-medium text-white">{location.name}</p>
                            {location.description && (
                              <p className="mt-0.5 text-xs text-slate-500">{location.description}</p>
                            )}
                          </div>
                        )}
                      </td>
                      <td className="whitespace-nowrap font-mono text-xs text-slate-400">{location.slug}</td>
                      <td>{connectionStatus(location)}</td>
                      <td>
                        {location.mesh_endpoint ? (
                          <span className="whitespace-nowrap font-mono text-xs text-slate-400">{location.mesh_endpoint}</span>
                        ) : (
                          <span className="text-xs text-slate-500">—</span>
                        )}
                      </td>
                      <td className="text-[13px] tabular-nums text-slate-300">
                        {location.monitor_count}
                      </td>
                      <td className="whitespace-nowrap text-[13px] text-slate-400">
                        {location.last_seen_at ? formatTimeAgo(location.last_seen_at) : '—'}
                      </td>
                      <td className="!py-2">
                        {canManageLocations && <div className="flex items-center justify-end gap-0.5">
                          <IconButton
                            icon={<Rocket strokeWidth={1.75} />}
                            label="Deploy a worker"
                            onClick={() => setDeployLocation(location)}
                          />
                          <IconButton
                            icon={<Pencil strokeWidth={1.75} />}
                            label="Edit name and mesh endpoint"
                            onClick={() => startRename(location)}
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
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        )
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
