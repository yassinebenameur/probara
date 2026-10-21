'use client';

import { RefreshCw } from 'lucide-react';
import { useOptionalAlertEvents } from '@/components/alerts/AlertStreamProvider';
import IconButton from './IconButton';
import { RelativeTime } from './RelativeTime';

interface LiveStatusProps {
  /** Epoch ms of the last successful load (useLiveRefresh().updatedAt). */
  updatedAt: number | null;
  /** A load is in flight: spins the refresh icon. */
  refreshing?: boolean;
  /** The last load failed: reads "Update failed" until one succeeds. */
  failed?: boolean;
  /** Manual refresh; omit to hide the button. */
  onRefresh?: () => void;
  /** Accessible name of the refresh button. */
  refreshLabel?: string;
  className?: string;
}

/**
 * "Live · updated 12s ago" for the header of a page that refreshes itself
 * (useLiveRefresh). The dot is the alert stream's state: green and pinging
 * briefly after each update while the stream is open, amber "Reconnecting…"
 * while it is down, grey "Offline" once retries ran out. The text says the
 * same thing (the dot is decoration) and is not a live region: it ticks.
 */
export default function LiveStatus({
  updatedAt,
  refreshing = false,
  failed = false,
  onRefresh,
  refreshLabel = 'Refresh',
  className = '',
}: LiveStatusProps) {
  const stream = useOptionalAlertEvents();
  const connection = stream?.connection ?? 'live';

  let tone: 'live' | 'warn' | 'off';
  let state: string;
  if (failed) {
    tone = 'warn';
    state = 'Update failed';
  } else if (connection === 'reconnecting' || connection === 'connecting') {
    tone = 'warn';
    state = connection === 'connecting' ? 'Connecting…' : 'Reconnecting…';
  } else if (connection === 'offline') {
    tone = 'off';
    state = 'Offline';
  } else {
    tone = 'live';
    state = 'Live';
  }

  const dot = tone === 'live' ? 'bg-emerald-500' : tone === 'warn' ? 'bg-amber-500' : 'bg-slate-500';

  return (
    <div className={`inline-flex items-center gap-2 text-2xs text-slate-500 ${className}`.trim()}>
      <span className="inline-flex items-center gap-1.5 tabular-nums" aria-live="off">
        {/* Keyed on the update so the ping ring replays (three pulses) after
            each refresh, then rests: "just updated", not an endless pulse. */}
        <span
          key={tone === 'live' ? updatedAt ?? 'idle' : tone}
          aria-hidden="true"
          className={`inline-flex h-1.5 w-1.5 rounded-full ${dot} ${tone === 'live' && updatedAt !== null ? 'animate-live-ping' : ''}`}
          style={{ animationIterationCount: 3 }}
        />
        <span>
          {state}
          {updatedAt !== null && (
            <>
              {' · '}
              <RelativeTime value={updatedAt} prefix="updated " />
            </>
          )}
        </span>
      </span>
      {onRefresh && (
        <IconButton
          icon={<RefreshCw strokeWidth={1.75} />}
          label={refreshLabel}
          onClick={onRefresh}
          loading={refreshing}
          spinIcon
          size="sm"
        />
      )}
    </div>
  );
}
