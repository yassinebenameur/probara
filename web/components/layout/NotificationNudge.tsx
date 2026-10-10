'use client';

import { useEffect, useState } from 'react';
import Link from 'next/link';
import { getNotificationSettings } from '@/lib/api';

const DISMISS_KEY = 'notification-nudge-dismissed';

/**
 * Warns when alerts have nowhere to go.
 *
 * Two distinct failures, in order of urgency:
 *
 *  1. `unroutedMonitors > 0` — monitors exist whose alerts the alerter would
 *     deliver to nobody (server-computed, shared/alertrouting). This is a live
 *     fleet defect, so it is not dismissible: it disappears when routing is
 *     fixed, not when the operator looks away.
 *  2. No workspace default channel at all — onboarding, dismissible for the
 *     session, and only shown when nothing is actually unrouted yet (a fresh
 *     workspace with no monitors).
 */
export function NotificationNudge({ unroutedMonitors = 0 }: { unroutedMonitors?: number }) {
  const [noDefaultChannels, setNoDefaultChannels] = useState(false);
  const [dismissed, setDismissed] = useState(true);

  useEffect(() => {
    if (typeof window === 'undefined') return;
    setDismissed(Boolean(sessionStorage.getItem(DISMISS_KEY)));
    getNotificationSettings()
      .then((s) => setNoDefaultChannels(s.default_channels.length === 0))
      .catch(() => setNoDefaultChannels(false));
  }, []);

  if (unroutedMonitors > 0) {
    return (
      <div className="mb-4 flex items-center justify-between gap-4 rounded-xl border border-amber-600/40 bg-amber-950/40 px-4 py-3">
        <p className="text-sm text-amber-200">
          ⚠{' '}
          <strong className="font-medium">
            {unroutedMonitors} monitor{unroutedMonitors === 1 ? "'s" : "s'"} alerts reach nobody
          </strong>{' '}
          — no active channel routes {unroutedMonitors === 1 ? 'it' : 'them'}, so an outage would go
          unnoticed.{' '}
          <Link href="/monitors?routing=unrouted" className="text-cyan-400 hover:text-cyan-300">
            Review routing
          </Link>
          {noDefaultChannels && (
            <>
              {' '}or{' '}
              <Link href="/settings" className="text-cyan-400 hover:text-cyan-300">
                set a workspace default
              </Link>
            </>
          )}
          .
        </p>
      </div>
    );
  }

  if (!noDefaultChannels || dismissed) return null;
  return (
    <div className="mb-4 flex items-center justify-between gap-4 rounded-xl border border-amber-600/40 bg-amber-950/40 px-4 py-3">
      <p className="text-sm text-amber-200">
        ⚠ Alerts have nowhere to go yet —{' '}
        <Link href="/settings" className="text-cyan-400 hover:text-cyan-300">connect a channel</Link>{' '}
        (MS Teams, Slack, email…) to set your workspace default.
      </p>
      <button
        type="button"
        className="shrink-0 text-xs text-amber-400/70 hover:text-amber-300"
        onClick={() => { sessionStorage.setItem(DISMISS_KEY, '1'); setDismissed(true); }}
      >
        Dismiss
      </button>
    </div>
  );
}
