'use client';

import { useEffect, useState } from 'react';
import Link from 'next/link';
import { X } from 'lucide-react';
import { getNotificationSettings } from '@/lib/api';
import { getSelectedTenantId } from '@/lib/tenant';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import IconButton from '@/components/ui/IconButton';

const DISMISS_KEY = 'notification-nudge-dismissed';
const UNROUTED_DISMISS_KEY = 'unrouted-nudge-dismissed';

// Session-scoped, per tenant; storage can be unavailable (private mode).
function readUnroutedDismissal(): number | null {
  try {
    const raw = sessionStorage.getItem(`${UNROUTED_DISMISS_KEY}:${getSelectedTenantId() ?? ''}`);
    return raw === null ? null : Number(raw);
  } catch {
    return null;
  }
}

function writeUnroutedDismissal(count: number) {
  try {
    sessionStorage.setItem(`${UNROUTED_DISMISS_KEY}:${getSelectedTenantId() ?? ''}`, String(count));
  } catch {
    // Dismissal then lasts until the next mount only.
  }
}

// Links inside a sentence need more than hue to stand apart from the text
// around them (WCAG 1.4.1): an underline that firms up on hover.
const INLINE_LINK =
  'font-medium text-cyan-300 underline decoration-cyan-300/40 underline-offset-2 transition-colors hover:text-cyan-200 hover:decoration-cyan-300';

/**
 * Warns when alerts have nowhere to go.
 *
 * Two distinct failures, in order of urgency:
 *
 *  1. `unroutedMonitors > 0` — monitors exist whose alerts the alerter would
 *     deliver to nobody (server-computed, shared/alertrouting). This is a live
 *     fleet defect, so dismissing it only snoozes it: for this session and
 *     this tenant, and only at the count it was dismissed at — one more
 *     unrouted monitor brings it back. The dashboard's Unrouted tile and the
 *     monitors list's "No alert route" filter keep reporting it meanwhile.
 *  2. No workspace default channel at all — onboarding, dismissible for the
 *     session, and only shown when nothing is actually unrouted yet (a fresh
 *     workspace with no monitors).
 */
export function NotificationNudge({ unroutedMonitors = 0 }: { unroutedMonitors?: number }) {
  const [noDefaultChannels, setNoDefaultChannels] = useState(false);
  const [dismissed, setDismissed] = useState(true);
  // The unrouted count the operator dismissed at (null: not dismissed).
  const [unroutedDismissedAt, setUnroutedDismissedAt] = useState<number | null>(null);

  useEffect(() => {
    if (typeof window === 'undefined') return;
    setDismissed(Boolean(sessionStorage.getItem(DISMISS_KEY)));
    setUnroutedDismissedAt(readUnroutedDismissal());
    getNotificationSettings()
      .then((s) => setNoDefaultChannels(s.default_channels.length === 0))
      .catch(() => setNoDefaultChannels(false));
  }, []);

  if (unroutedMonitors > 0) {
    if (unroutedDismissedAt !== null && unroutedMonitors <= unroutedDismissedAt) return null;
    return (
      <Callout
        tone="warning"
        className="mb-6"
        title={`${unroutedMonitors} monitor${unroutedMonitors === 1 ? ' alerts' : 's alert'} no one`}
        action={
          <IconButton
            label="Dismiss for this session"
            icon={<X strokeWidth={1.75} />}
            onClick={() => {
              writeUnroutedDismissal(unroutedMonitors);
              setUnroutedDismissedAt(unroutedMonitors);
            }}
          />
        }
      >
        No active channel routes {unroutedMonitors === 1 ? 'it' : 'them'}, so an outage would go unnoticed.{' '}
        <Link href="/monitors?routing=unrouted" className={INLINE_LINK}>
          Review routing
        </Link>
        {noDefaultChannels && (
          <>
            {' '}or{' '}
            <Link href="/settings" className={INLINE_LINK}>
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
      <Link href="/settings" className={INLINE_LINK}>
        Connect a channel
      </Link>{' '}
      (MS Teams, Slack, email…) to set your workspace default.
    </Callout>
  );
}
