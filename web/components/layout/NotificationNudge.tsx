'use client';

import { useEffect, useState } from 'react';
import Link from 'next/link';
import { getNotificationSettings } from '@/lib/api';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';

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
      <Callout
        tone="warning"
        className="mb-6"
        title={`${unroutedMonitors} monitor${unroutedMonitors === 1 ? "'s" : "s'"} alerts reach nobody`}
      >
        No active channel routes {unroutedMonitors === 1 ? 'it' : 'them'}, so an outage would go unnoticed.{' '}
        <Link href="/monitors?routing=unrouted" className="font-medium text-cyan-300 hover:text-cyan-200">
          Review routing
        </Link>
        {noDefaultChannels && (
          <>
            {' '}or{' '}
            <Link href="/settings" className="font-medium text-cyan-300 hover:text-cyan-200">
              set a workspace default
            </Link>
          </>
        )}
        .
      </Callout>
    );
  }

  if (!noDefaultChannels || dismissed) return null;
  return (
    <Callout
      tone="warning"
      className="mb-6"
      title="Alerts have nowhere to go yet"
      action={
        <Button
          variant="subtle"
          size="xs"
          onClick={() => {
            sessionStorage.setItem(DISMISS_KEY, '1');
            setDismissed(true);
          }}
        >
          Dismiss
        </Button>
      }
    >
      <Link href="/settings" className="font-medium text-cyan-300 hover:text-cyan-200">
        Connect a channel
      </Link>{' '}
      (MS Teams, Slack, email…) to set your workspace default.
    </Callout>
  );
}
