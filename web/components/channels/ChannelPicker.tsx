'use client';

import { ReactNode, useEffect, useState } from 'react';
import { Plus, X } from 'lucide-react';
import {
  AlertChannel,
  ChannelAssignment,
  CreateAlertChannelRequest,
  UpdateAlertChannelRequest,
} from '@/lib/types';
import { createAlertChannel, getAlertChannels } from '@/lib/api';
import AlertChannelForm from '@/components/alert-channels/AlertChannelForm';
import Callout from '@/components/ui/Callout';
import Checkbox from '@/components/ui/Checkbox';
import IconButton from '@/components/ui/IconButton';
import Pill from '@/components/ui/Pill';
import { usePluginNames } from '@/components/alert-channels/usePluginNames';
import { formatDuration } from '@/lib/format';

const DELAY_PRESETS = [
  { label: 'Immediately', value: 0 },
  { label: 'If still down 5 min', value: 300 },
  { label: 'If still down 10 min', value: 600 },
  { label: 'If still down 30 min', value: 1800 },
] as const;

// A delay saved outside the presets (API, import, an older UI) stays a
// selectable option of its own; without it the select would show the first
// preset while the stored delay stays in place.
function delayText(seconds: number): string {
  // Whole minutes under an hour read like the presets ("15 min").
  return seconds % 60 === 0 && seconds < 3600 ? `${seconds / 60} min` : formatDuration(seconds);
}

function delayOptions(current: number): { label: string; value: number }[] {
  if (DELAY_PRESETS.some((p) => p.value === current)) return [...DELAY_PRESETS];
  return [...DELAY_PRESETS, { label: `If still down ${delayText(current)}`, value: current }].sort(
    (a, b) => a.value - b.value
  );
}

export interface ChannelPickerProps {
  value: ChannelAssignment[];
  onChange: (next: ChannelAssignment[]) => void;
  defaultChannelIds?: string[];
  showDelays?: boolean;
  /**
   * Called whenever the picker's channel list changes, so a parent can reason
   * about the selection (e.g. warn that every selected channel is disabled)
   * without fetching the channels a second time.
   */
  onChannelsLoaded?: (channels: AlertChannel[]) => void;
  /** Read-only: selection and delays are shown but cannot change. */
  disabled?: boolean;
  /** Id of the visible label naming the channel list. */
  ariaLabelledBy?: string;
  /**
   * Offer the inline "New channel" form (default on). Off where the page
   * already has its own channel management (Settings → Notifications).
   */
  allowCreate?: boolean;
  /** Replaces the "No channels yet" line, e.g. with a pointer to where channels are made. */
  emptyState?: ReactNode;
}

export default function ChannelPicker({
  value,
  onChange,
  defaultChannelIds = [],
  showDelays = true,
  onChannelsLoaded,
  disabled = false,
  ariaLabelledBy,
  allowCreate = true,
  emptyState,
}: ChannelPickerProps) {
  const pluginName = usePluginNames();
  const [channels, setChannels] = useState<AlertChannel[]>([]);
  const [loadingChannels, setLoadingChannels] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);

  // Inline create state
  const [showCreate, setShowCreate] = useState(false);
  const [creating, setCreating] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    setLoadingChannels(true);
    getAlertChannels({ page_size: 200 })
      .then((res) => {
        if (!cancelled) setChannels(res?.items ?? []);
      })
      .catch((err: Error) => {
        if (!cancelled) setLoadError(err.message || 'Failed to load channels');
      })
      .finally(() => {
        if (!cancelled) setLoadingChannels(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    onChannelsLoaded?.(channels);
    // onChannelsLoaded is a render-stable callback in practice; keying the
    // effect on it too would re-fire on every parent render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [channels]);

  const selectedIds = new Set(value.map((a) => a.channel_id));

  const handleToggle = (channel: AlertChannel) => {
    if (selectedIds.has(channel.id)) {
      onChange(value.filter((a) => a.channel_id !== channel.id));
    } else {
      onChange([
        ...value,
        {
          channel_id: channel.id,
          channel_name: channel.name,
          channel_type: channel.type,
          delay_seconds: 0,
        },
      ]);
    }
  };

  const handleDelayChange = (channelId: string, delay: number) => {
    onChange(
      value.map((a) =>
        a.channel_id === channelId ? { ...a, delay_seconds: delay } : a
      )
    );
  };

  const handleCreateSubmit = async (
    data: CreateAlertChannelRequest | UpdateAlertChannelRequest
  ) => {
    if (!('type' in data) || !data.type) {
      setCreateError('Channel type is required');
      return;
    }
    try {
      setCreating(true);
      setCreateError(null);
      const created = await createAlertChannel(data as CreateAlertChannelRequest);
      // Add to local list
      setChannels((prev) => [...prev, created]);
      // Auto-select with delay 0
      onChange([
        ...value,
        {
          channel_id: created.id,
          channel_name: created.name,
          channel_type: created.type,
          delay_seconds: 0,
        },
      ]);
      setShowCreate(false);
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : 'Failed to create channel';
      setCreateError(message);
    } finally {
      setCreating(false);
    }
  };

  return (
    <div className="space-y-2">
      <div className="glass-well overflow-hidden">
        {loadingChannels ? (
          <div role="status" className="px-4 py-3 text-ui text-slate-500">Loading channels…</div>
        ) : loadError ? (
          <div role="alert" className="px-4 py-3 text-ui text-rose-300">{loadError}</div>
        ) : channels.length === 0 ? (
          <div className="px-4 py-3 text-ui text-slate-500">
            {emptyState ??
              (disabled || !allowCreate ? 'No channels yet.' : 'No channels yet. Add one below to start receiving alerts.')}
          </div>
        ) : (
          <ul className="divide-y divide-divider" aria-labelledby={ariaLabelledBy}>
            {channels.map((channel) => {
              const isSelected = selectedIds.has(channel.id);
              const assignment = value.find((a) => a.channel_id === channel.id);
              const isDefault = defaultChannelIds.includes(channel.id);

              return (
                // Wraps instead of squeezing: once the name column would drop
                // below 12rem the delay select moves to its own line.
                <li
                  key={channel.id}
                  className={`flex flex-wrap items-center gap-x-3 gap-y-2 px-4 py-2.5 transition-colors ${
                    isSelected ? 'bg-well-strong' : 'hover:bg-well-hover'
                  }`}
                >
                  <Checkbox
                    id={`channel-${channel.id}`}
                    checked={isSelected}
                    onChange={() => handleToggle(channel)}
                    disabled={disabled}
                  />

                  {/* Name over a muted meta line (plugin, default, status), so
                      the name keeps the width beside a delay select. */}
                  <label
                    htmlFor={`channel-${channel.id}`}
                    className={`min-w-0 grow basis-48 ${disabled ? 'cursor-not-allowed' : 'cursor-pointer'}`}
                  >
                    <span className={`block truncate text-sm ${channel.is_active === false ? 'text-slate-400' : 'text-slate-200'}`}>
                      {channel.name}
                    </span>
                    <span className="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-slate-500">
                      <span>{pluginName(channel.type)}</span>
                      {isDefault && <span className="text-slate-400">(default)</span>}
                      {channel.is_active === false && (
                        // Same wording and tone as the channel list's Status column.
                        <span title="This channel is inactive — selecting it delivers nothing">
                          <Pill tone="neutral" size="xs" dot>Inactive</Pill>
                        </span>
                      )}
                    </span>
                  </label>

                  {showDelays && isSelected && assignment && (
                    <select
                      value={assignment.delay_seconds}
                      onChange={(e) =>
                        handleDelayChange(channel.id, Number(e.target.value))
                      }
                      className="input input-sm ml-auto !w-44 flex-shrink-0 cursor-pointer"
                      aria-label={`Delay for ${channel.name}`}
                      disabled={disabled}
                    >
                      {delayOptions(assignment.delay_seconds).map((preset) => (
                        <option key={preset.value} value={preset.value}>
                          {preset.label}
                        </option>
                      ))}
                    </select>
                  )}
                </li>
              );
            })}
          </ul>
        )}
      </div>

      {disabled || !allowCreate ? null : showCreate ? (
        <div className="glass-well space-y-4 p-4">
          <div className="flex items-center justify-between">
            <span className="text-title font-semibold text-white">New channel</span>
            <IconButton
              icon={<X strokeWidth={1.75} />}
              label="Close new channel panel"
              onClick={() => {
                setShowCreate(false);
                setCreateError(null);
              }}
            />
          </div>

          {createError && <Callout tone="danger">{createError}</Callout>}

          <AlertChannelForm
            onSubmit={handleCreateSubmit}
            onCancel={() => {
              setShowCreate(false);
              setCreateError(null);
            }}
            loading={creating}
          />
        </div>
      ) : (
        <button
          type="button"
          onClick={() => setShowCreate(true)}
          className="inline-flex items-center gap-1.5 text-ui font-medium text-cyan-300 transition-colors hover:text-cyan-200"
        >
          <Plus className="h-4 w-4" strokeWidth={1.75} />
          New channel
        </button>
      )}
    </div>
  );
}
