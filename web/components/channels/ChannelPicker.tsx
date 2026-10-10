'use client';

import { useEffect, useState } from 'react';
import { Plus, X } from 'lucide-react';
import {
  AlertChannel,
  ChannelAssignment,
  CreateAlertChannelRequest,
  UpdateAlertChannelRequest,
} from '@/lib/types';
import { createAlertChannel, getAlertChannels } from '@/lib/api';
import AlertChannelForm from '@/components/alert-channels/AlertChannelForm';

const DELAY_PRESETS = [
  { label: 'Immediately', value: 0 },
  { label: 'If still down 5 min', value: 300 },
  { label: 'If still down 10 min', value: 600 },
  { label: 'If still down 30 min', value: 1800 },
] as const;

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
}

export default function ChannelPicker({
  value,
  onChange,
  defaultChannelIds = [],
  showDelays = true,
  onChannelsLoaded,
}: ChannelPickerProps) {
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
      {/* Channel list */}
      <div className="rounded-lg border border-white/[0.06] bg-slate-900/40 overflow-hidden">
        {loadingChannels ? (
          <div className="px-4 py-3 text-xs text-slate-500">Loading channels…</div>
        ) : loadError ? (
          <div className="px-4 py-3 text-xs text-rose-400">{loadError}</div>
        ) : channels.length === 0 ? (
          <div className="px-4 py-3 text-xs text-slate-500">
            No channels configured yet. Create one below.
          </div>
        ) : (
          <ul className="divide-y divide-white/[0.04]">
            {channels.map((channel) => {
              const isSelected = selectedIds.has(channel.id);
              const assignment = value.find((a) => a.channel_id === channel.id);
              const isDefault = defaultChannelIds.includes(channel.id);

              return (
                <li
                  key={channel.id}
                  className={`flex items-center gap-3 px-4 py-2.5 transition-colors ${
                    isSelected ? 'bg-cyan-500/5' : 'hover:bg-slate-800/40'
                  }`}
                >
                  {/* Checkbox */}
                  <input
                    type="checkbox"
                    id={`channel-${channel.id}`}
                    checked={isSelected}
                    onChange={() => handleToggle(channel)}
                    className="h-3.5 w-3.5 rounded border-slate-600 bg-slate-800 text-cyan-500 accent-cyan-500 cursor-pointer flex-shrink-0"
                  />

                  {/* Name + type */}
                  <label
                    htmlFor={`channel-${channel.id}`}
                    className="flex flex-1 items-center gap-2 cursor-pointer min-w-0"
                  >
                    <span className="text-sm text-slate-200 truncate">{channel.name}</span>
                    <span className="text-xs text-slate-500 uppercase tracking-wide flex-shrink-0">
                      {channel.type}
                    </span>
                    {isDefault && (
                      <span className="text-xs text-cyan-400/70 flex-shrink-0">(default)</span>
                    )}
                    {channel.is_active === false && (
                      <span
                        className="flex-shrink-0 rounded border border-amber-500/30 bg-amber-500/10 px-1 text-[10px] uppercase tracking-wide text-amber-300"
                        title="This channel is disabled — selecting it delivers nothing"
                      >
                        Disabled
                      </span>
                    )}
                  </label>

                  {/* Delay dropdown */}
                  {showDelays && isSelected && assignment && (
                    <select
                      value={assignment.delay_seconds}
                      onChange={(e) =>
                        handleDelayChange(channel.id, Number(e.target.value))
                      }
                      className="ml-auto flex-shrink-0 rounded border border-white/[0.08] bg-slate-800 px-2 py-1 text-xs text-slate-300 focus:border-cyan-500/50 focus:outline-none cursor-pointer"
                      aria-label={`Delay for ${channel.name}`}
                    >
                      {DELAY_PRESETS.map((preset) => (
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

      {/* Inline create panel */}
      {showCreate ? (
        <div className="rounded-lg border border-white/[0.08] bg-slate-900/60 p-4 space-y-4">
          <div className="flex items-center justify-between">
            <span className="text-xs font-medium text-slate-300">New channel</span>
            <button
              type="button"
              onClick={() => {
                setShowCreate(false);
                setCreateError(null);
              }}
              className="text-slate-500 hover:text-slate-300 transition-colors"
              aria-label="Close new channel panel"
            >
              <X className="h-4 w-4" />
            </button>
          </div>

          {createError && (
            <p className="text-xs text-rose-400">{createError}</p>
          )}

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
          className="flex items-center gap-1.5 text-xs text-cyan-400 hover:text-cyan-300 transition-colors"
        >
          <Plus className="h-3.5 w-3.5" />
          New channel
        </button>
      )}
    </div>
  );
}
