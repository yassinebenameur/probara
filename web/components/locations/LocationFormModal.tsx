'use client';

import { useId, useState } from 'react';
import { createLocation, updateLocation } from '@/lib/api';
import type { Location } from '@/lib/types';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import Dialog from '@/components/ui/Dialog';
import Input from '@/components/ui/Input';

interface LocationFormModalProps {
  location?: Location; // present = edit mode
  onDone: (saved: Location) => void;
  onCancel: () => void;
}

// One form for create and edit, so every field a location has can also be
// changed after the fact (the old inline editor could not touch the
// description).
export function LocationFormModal({ location, onDone, onCancel }: LocationFormModalProps) {
  const [name, setName] = useState(location?.name ?? '');
  const [description, setDescription] = useState(location?.description ?? '');
  const [meshEndpoint, setMeshEndpoint] = useState(location?.mesh_endpoint ?? '');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const formId = useId();
  const meshHintId = useId();

  const save = async (event: React.FormEvent) => {
    event.preventDefault();
    if (!name.trim()) {
      setError('Location name is required.');
      return;
    }
    setSaving(true);
    setError(null);
    try {
      const saved = location
        ? // Description and mesh endpoint are always sent on edit: an emptied
          // field clears it (an empty endpoint opts the location out of the mesh).
          await updateLocation(location.id, {
            name: name.trim(),
            description: description.trim(),
            mesh_endpoint: meshEndpoint.trim(),
          })
        : await createLocation({
            name: name.trim(),
            ...(description.trim() ? { description: description.trim() } : {}),
            ...(meshEndpoint.trim() ? { mesh_endpoint: meshEndpoint.trim() } : {}),
          });
      onDone(saved);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to save location');
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog
      onClose={onCancel}
      busy={saving}
      title={location ? 'Edit location' : 'New location'}
      description={
        location
          ? 'Renaming changes the slug; deployed workers keep working.'
          : 'Name the network or site this location represents.'
      }
      footer={
        <>
          <Button type="button" variant="ghost" size="sm" onClick={onCancel} disabled={saving}>
            Cancel
          </Button>
          <Button type="submit" form={formId} variant="accent" size="sm" disabled={saving || !name.trim()} loading={saving}>
            {location ? 'Save changes' : 'Create location'}
          </Button>
        </>
      }
    >
      <form id={formId} onSubmit={save} noValidate className="space-y-4">
        <Input
          label="Name"
          type="text"
          value={name}
          onChange={(event) => setName(event.target.value)}
          placeholder="e.g. Paris VPC"
          autoFocus
        />
        <Input
          label="Description (optional)"
          type="text"
          value={description}
          onChange={(event) => setDescription(event.target.value)}
          placeholder="e.g. Production VPC in eu-west-3"
        />
        <div>
          <Input
            label="Mesh endpoint (optional)"
            type="text"
            value={meshEndpoint}
            onChange={(event) => setMeshEndpoint(event.target.value)}
            placeholder="host:port"
            className="font-mono"
            aria-describedby={meshHintId}
          />
          <p id={meshHintId} className="mt-1 text-xs text-slate-500">
            Reachable from your other locations. Set it to include this location in the mesh; leave it empty to opt out.
          </p>
        </div>

        {error && <Callout tone="danger">{error}</Callout>}
      </form>
    </Dialog>
  );
}
