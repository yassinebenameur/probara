'use client';

import { useEffect, useMemo, useState, type FormEvent } from 'react';
import {
  AlertChannel,
  CreateAlertChannelRequest,
  PluginManifest,
  UpdateAlertChannelRequest,
} from '@/lib/types';
import { getAlertChannelPlugins } from '@/lib/api';
import Callout from '@/components/ui/Callout';
import FormField from '@/components/ui/FormField';
import FormActions from '@/components/ui/FormActions';
import PluginCatalog from './PluginCatalog';
import SchemaForm, { CLEARED_SECRET } from './SchemaForm';
import Switch from '@/components/ui/Switch';

interface AlertChannelFormProps {
  channel?: AlertChannel;
  initialType?: string;
  onSubmit: (data: CreateAlertChannelRequest | UpdateAlertChannelRequest) => Promise<void>;
  onCancel?: () => void;
  loading?: boolean;
}

export default function AlertChannelForm({
  channel,
  initialType,
  onSubmit,
  onCancel,
  loading = false,
}: AlertChannelFormProps) {
  const isEdit = !!channel;

  const [manifests, setManifests] = useState<PluginManifest[]>([]);
  const [loadingManifests, setLoadingManifests] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);

  const [selectedType, setSelectedType] = useState<string>(channel?.type ?? initialType ?? '');
  const [name, setName] = useState(channel?.name ?? '');
  const [isActive, setIsActive] = useState(channel?.is_active ?? true);
  const [configValue, setConfigValue] = useState<Record<string, unknown>>(
    () => initialConfig(channel)
  );
  const [errors, setErrors] = useState<Record<string, string>>({});

  useEffect(() => {
    let cancelled = false;
    setLoadingManifests(true);
    getAlertChannelPlugins()
      .then((list) => {
        if (cancelled) return;
        setManifests(list);
        if (!selectedType && list.length > 0) {
          // If initialType was provided and matches a known plugin, honor it;
          // otherwise fall back to the first manifest so the form is usable.
          const preselect =
            (initialType && list.find((m) => m.type === initialType)?.type) || list[0].type;
          setSelectedType(preselect);
          const chosen = list.find((m) => m.type === preselect);
          if (chosen) setConfigValue(defaultsFromManifest(chosen));
        }
      })
      .catch((err: Error) => {
        if (cancelled) return;
        setLoadError(err.message || 'Failed to load plugin catalog');
      })
      .finally(() => {
        if (!cancelled) setLoadingManifests(false);
      });
    return () => {
      cancelled = true;
    };
    // Mount-only: the preselect reads the initial props, not later changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const selectedManifest = useMemo(
    () => manifests.find((m) => m.type === selectedType),
    [manifests, selectedType]
  );

  const handleSelectPlugin = (m: PluginManifest) => {
    if (isEdit) return;
    setSelectedType(m.type);
    setConfigValue(defaultsFromManifest(m));
    setErrors({});
  };

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    const newErrors = validate(name, selectedManifest, configValue, isEdit);
    setErrors(newErrors);
    if (Object.keys(newErrors).length > 0) return;

    const submittableConfig = stripEmptySecrets(
      configValue,
      selectedManifest,
      isEdit
    );

    if (isEdit) {
      const updateData: UpdateAlertChannelRequest = {
        name: name.trim(),
        is_active: isActive,
        config: submittableConfig,
      };
      await onSubmit(updateData);
    } else {
      const createData: CreateAlertChannelRequest = {
        name: name.trim(),
        type: selectedType,
        is_active: isActive,
        config: submittableConfig,
      };
      await onSubmit(createData);
    }
  };

  if (loadingManifests) {
    return <p className="text-sm text-slate-500">Loading plugin catalog…</p>;
  }
  if (loadError) {
    return <Callout tone="danger">{loadError}</Callout>;
  }

  // The type is fixed once a channel exists, so edit shows just that one.
  const catalogManifests = isEdit && selectedManifest ? [selectedManifest] : manifests;

  // Plain sections rather than FormSection panels: the form is embedded in a
  // FormCard on its own pages and in a well inside ChannelPicker.
  return (
    <form onSubmit={handleSubmit} className="space-y-6">
      <section>
        <h3 className="mb-3 text-[15px] font-semibold text-white">Channel type</h3>
        <PluginCatalog
          manifests={catalogManifests}
          selectedType={selectedType}
          onSelect={handleSelectPlugin}
          disabled={isEdit}
        />
      </section>

      <section className="space-y-4">
        <h3 className="text-[15px] font-semibold text-white">Configuration</h3>
        <FormField label="Channel name" required error={errors.name}>
          <input
            type="text"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder={selectedManifest?.display_name ?? 'Channel name'}
            className="input"
          />
        </FormField>

        {selectedManifest && (
          <SchemaForm
            manifest={selectedManifest}
            value={configValue}
            onChange={setConfigValue}
            errors={errors}
            isEdit={isEdit}
          />
        )}

        <div className="flex items-center justify-between gap-4">
          <div className="min-w-0">
            <p className="text-sm font-medium text-slate-200">Channel active</p>
            <p className="text-xs text-slate-500">Deliver alerts using this channel</p>
          </div>
          <Switch checked={isActive} onChange={() => setIsActive(!isActive)} label="Toggle channel active" />
        </div>
      </section>

      <FormActions
        cancel={onCancel ? { label: 'Cancel', onClick: onCancel, disabled: loading } : undefined}
        submit={{
          label: loading ? 'Saving…' : isEdit ? 'Update channel' : 'Create channel',
          loading,
          disabled: loading || !selectedManifest,
          type: 'submit',
        }}
      />
    </form>
  );
}

function initialConfig(channel?: AlertChannel): Record<string, unknown> {
  if (!channel?.config) return {};
  return { ...channel.config };
}

function defaultsFromManifest(m: PluginManifest): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const f of m.fields) {
    if (f.default !== undefined) out[f.key] = f.default;
  }
  return out;
}

function validate(
  name: string,
  manifest: PluginManifest | undefined,
  config: Record<string, unknown>,
  isEdit: boolean
): Record<string, string> {
  const errors: Record<string, string> = {};
  if (!name.trim()) errors.name = 'Name is required';
  if (!manifest) {
    errors.type = 'Select a channel type';
    return errors;
  }
  for (const field of manifest.fields) {
    if (!field.required) continue;
    // On edit, an empty secret means "keep existing" — don't flag it.
    if (field.secret && isEdit && isEmpty(config[field.key])) continue;
    if (isEmpty(config[field.key])) {
      errors[field.key] = `${field.label} is required`;
    }
  }
  return errors;
}

function isEmpty(v: unknown): boolean {
  if (v === undefined || v === null) return true;
  if (typeof v === 'string') return v.trim() === '';
  if (Array.isArray(v)) return v.length === 0;
  return false;
}

function stripEmptySecrets(
  config: Record<string, unknown>,
  manifest: PluginManifest | undefined,
  isEdit: boolean
): Record<string, unknown> {
  if (!manifest) return config;
  if (!isEdit) return config;
  const out: Record<string, unknown> = { ...config };
  for (const f of manifest.fields) {
    // null is the explicit "remove the stored value" (SchemaForm's
    // CLEARED_SECRET) and must reach the API; blank means keep.
    if (f.secret && out[f.key] !== CLEARED_SECRET && isEmpty(out[f.key])) {
      delete out[f.key];
    }
  }
  return out;
}
