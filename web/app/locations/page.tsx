'use client';

import { useEffect, useRef, useState } from 'react';
import { MapPin, Plus, RefreshCw, Rocket, Pencil, Trash2 } from 'lucide-react';
import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
import Pill from '@/components/ui/Pill';
import PageHeader from '@/components/ui/PageHeader';
import EmptyState from '@/components/ui/EmptyState';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import { useToast } from '@/components/ui/ToastProvider';
import { LocationDeployModal } from '@/components/locations/LocationDeployModal';
import { Location } from '@/lib/types';
import { createLocation, deleteLocation, getLocations, updateLocation } from '@/lib/api';
import { formatTimeAgo } from '@/lib/monitor-utils';

function connectionPill(location: Location) {
  if (location.connected) {
    return (
      <Pill tone="success" size="xs" dot>
        Connected
      </Pill>
    );
  }
  if (!location.last_seen_at) {
    return (
      <Pill tone="neutral" size="xs">
        Never connected
      </Pill>
    );
  }
  return (
    <Pill tone="danger" size="xs" dot>
      Disconnected
    </Pill>
  );
}

export default function LocationsPage() {
  const { showToast } = useToast();
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
    // eslint-disable-next-line react-hooks/exhaustive-deps
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

  const addAction = (
    <Button
      variant="accent"
      size="sm"
      icon={<Plus strokeWidth={1.75} />}
      onClick={() => setShowCreate(true)}
    >
      Add location
    </Button>
  );

  return (
    <div className="space-y-6">
      <PageHeader
        title="Private locations"
        subtitle="Run checks from your own networks via remote workers"
        action={addAction}
      />

      {showCreate && (
        <Panel title="New location" subtitle="Name the network or site this location represents.">
          <form onSubmit={handleCreate} className="flex flex-col gap-3 lg:flex-row lg:items-end">
            <div className="flex-1">
              <label className="mb-1.5 block text-xs font-medium text-slate-400">Name</label>
              <input
                type="text"
                value={newName}
                onChange={(event) => setNewName(event.target.value)}
                placeholder="e.g. Paris VPC"
                className="input"
                autoFocus
              />
            </div>
            <div className="flex-1">
              <label className="mb-1.5 block text-xs font-medium text-slate-400">
                Description (optional)
              </label>
              <input
                type="text"
                value={newDescription}
                onChange={(event) => setNewDescription(event.target.value)}
                placeholder="e.g. Production VPC in eu-west-3"
                className="input"
              />
            </div>
            <div className="flex-1">
              <label className="mb-1.5 block text-xs font-medium text-slate-400">
                Mesh endpoint (optional)
              </label>
              <input
                type="text"
                value={newMeshEndpoint}
                onChange={(event) => setNewMeshEndpoint(event.target.value)}
                placeholder="host:port reachable from your other locations"
                className="input"
              />
            </div>
            <div className="flex items-center gap-2">
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

      <Panel
        title="Locations"
        subtitle="Each location groups the workers deployed in one of your networks."
        actions={(
          <Button
            variant="ghost"
            size="sm"
            icon={<RefreshCw strokeWidth={1.75} />}
            onClick={() => loadLocations()}
          >
            Refresh
          </Button>
        )}
      >
        {loading ? (
          <div className="text-sm text-slate-500">Loading locations…</div>
        ) : error ? (
          <div className="space-y-3">
            <p className="text-sm text-rose-400">{error}</p>
            <Button variant="ghost" size="sm" onClick={() => loadLocations()}>Retry</Button>
          </div>
        ) : locations.length === 0 ? (
          <EmptyState
            icon={<MapPin strokeWidth={1.5} />}
            title="No private locations yet"
            description="Create a location, then deploy a worker inside that network. Monitors assigned to it will run their checks from there."
            action={addAction}
          />
        ) : (
          <div className="overflow-x-auto">
            <table className="min-w-full divide-y divide-white/[0.06]">
              <thead>
                <tr className="text-left text-xs uppercase tracking-wide text-slate-400">
                  <th className="px-3 py-2">Name</th>
                  <th className="px-3 py-2">Slug</th>
                  <th className="px-3 py-2">Status</th>
                  <th className="px-3 py-2">Mesh</th>
                  <th className="px-3 py-2">Monitors</th>
                  <th className="px-3 py-2">Last seen</th>
                  <th className="px-3 py-2">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-white/[0.04] text-sm text-slate-200">
                {locations.map((location) => (
                  <tr key={location.id}>
                    <td className="px-3 py-3">
                      {renamingId === location.id ? (
                        <div className="flex flex-wrap items-center gap-2">
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
                            className="input max-w-[14rem]"
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
                            className="input max-w-[16rem]"
                          />
                          <Button
                            variant="accent"
                            size="xs"
                            onClick={handleRename}
                            disabled={savingRename}
                            loading={savingRename}
                          >
                            Save
                          </Button>
                          <Button
                            variant="ghost"
                            size="xs"
                            onClick={() => setRenamingId(null)}
                            disabled={savingRename}
                          >
                            Cancel
                          </Button>
                        </div>
                      ) : (
                        <div>
                          <p className="font-medium text-white">{location.name}</p>
                          {location.description && (
                            <p className="text-xs text-slate-500">{location.description}</p>
                          )}
                        </div>
                      )}
                    </td>
                    <td className="px-3 py-3 font-mono text-xs text-slate-400">{location.slug}</td>
                    <td className="px-3 py-3">{connectionPill(location)}</td>
                    <td className="px-3 py-3">
                      {location.mesh_endpoint ? (
                        <div className="flex items-center gap-1.5">
                          <Pill tone="info" size="xs">Mesh</Pill>
                          <span className="font-mono text-xs text-slate-400">{location.mesh_endpoint}</span>
                        </div>
                      ) : (
                        <span className="text-xs text-slate-500">—</span>
                      )}
                    </td>
                    <td className="px-3 py-3 text-xs text-slate-400 tabular-nums">
                      {location.monitor_count}
                    </td>
                    <td className="px-3 py-3 text-xs text-slate-400">
                      {location.last_seen_at ? formatTimeAgo(location.last_seen_at) : '—'}
                    </td>
                    <td className="px-3 py-3">
                      <div className="flex items-center gap-1.5">
                        <Button
                          variant="ghost"
                          size="xs"
                          icon={<Rocket strokeWidth={1.75} />}
                          onClick={() => setDeployLocation(location)}
                        >
                          Deploy
                        </Button>
                        <Button
                          variant="ghost"
                          size="xs"
                          icon={<Pencil strokeWidth={1.75} />}
                          onClick={() => startRename(location)}
                        >
                          Rename
                        </Button>
                        <Button
                          variant="ghost"
                          size="xs"
                          icon={<Trash2 strokeWidth={1.75} />}
                          onClick={() => setPendingDelete(location)}
                        >
                          Delete
                        </Button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Panel>

      <ConfirmDialog
        open={pendingDelete !== null}
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

      {deployLocation && (
        <LocationDeployModal location={deployLocation} onClose={() => setDeployLocation(null)} />
      )}
    </div>
  );
}
