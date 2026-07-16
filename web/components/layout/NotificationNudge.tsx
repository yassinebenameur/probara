'use client';

import { useEffect, useState } from 'react';
import Link from 'next/link';
import { getNotificationSettings } from '@/lib/api';

const DISMISS_KEY = 'notification-nudge-dismissed';

export function NotificationNudge() {
  const [show, setShow] = useState(false);

  useEffect(() => {
    if (typeof window === 'undefined' || sessionStorage.getItem(DISMISS_KEY)) return;
    getNotificationSettings()
      .then((s) => setShow(s.default_channels.length === 0))
      .catch(() => setShow(false));
  }, []);

  if (!show) return null;
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
        onClick={() => { sessionStorage.setItem(DISMISS_KEY, '1'); setShow(false); }}
      >
        Dismiss
      </button>
    </div>
  );
}
