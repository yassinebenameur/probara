'use client';

import { useCallback, useEffect, useState } from 'react';
import Link from 'next/link';
import BrandMark from '@/components/ui/BrandMark';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import {
  ACTION_TOKEN_HEADER,
  ALERT_ACTION_PATH,
  alertStatusLabel,
  describeAlertActionState,
  parseAlertActionResponse,
  readActionToken,
  type AlertActionResponse,
} from '@/lib/alert-action';
import { formatDateTime, formatRelativeTime } from '@/lib/format';

// Confirm page behind a notification's signed acknowledge link. Rendered
// outside the app shell (see Layout and AuthGuard): the token is the
// credential, so it works signed out. See lib/alert-action.ts.
export default function AlertActionPage() {
  // undefined until the hash has been read on the client.
  const [token, setToken] = useState<string | undefined>(undefined);
  const [data, setData] = useState<AlertActionResponse | null>(null);
  const [error, setError] = useState('');
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    setToken(readActionToken(window.location.hash));
  }, []);

  const request = useCallback(
    async (method: 'GET' | 'POST') => {
      const res = await fetch(ALERT_ACTION_PATH, {
        method,
        headers: { [ACTION_TOKEN_HEADER]: token ?? '' },
        credentials: 'omit',
        cache: 'no-store',
      });
      const body = await res.json().catch(() => null);
      const parsed = parseAlertActionResponse(res.status, body);
      if (!parsed) throw new Error('unexpected response');
      return parsed;
    },
    [token],
  );

  const load = useCallback(() => {
    setError('');
    request('GET')
      .then(setData)
      .catch(() => setError('Could not load this alert. Check your connection and try again.'));
  }, [request]);

  useEffect(() => {
    if (token === undefined) return;
    if (token === '') {
      setData({ state: 'invalid' });
      return;
    }
    load();
  }, [token, load]);

  const acknowledge = async () => {
    setSubmitting(true);
    setError('');
    try {
      const parsed = await request('POST');
      // A refusal may carry no alert; keep the one already shown for context.
      setData((prev) => ({ ...parsed, alert: parsed.alert ?? prev?.alert }));
    } catch {
      setError('Acknowledging failed. Try again.');
    } finally {
      setSubmitting(false);
    }
  };

  const copy = data ? describeAlertActionState(data.state) : null;
  const alert = data?.alert;

  return (
    <div className="flex min-h-screen items-center justify-center px-4 py-10">
      <div className="glass-overlay w-full max-w-md p-8">
        <div className="mb-6 flex justify-center">
          <BrandMark size={40} />
        </div>

        {!data && !error ? (
          <p className="py-8 text-center text-sm text-slate-400">Loading…</p>
        ) : (
          <div className="space-y-6">
            <div className="text-center">
              {alert && <p className="text-xs font-medium uppercase tracking-wide text-slate-400">{alert.label}</p>}
              <h1 className="mt-1 break-words text-title font-semibold tracking-tight text-white">
                {alert ? alert.subject : 'Acknowledge alert'}
              </h1>
              {alert && (
                <p className="mt-2 text-sm text-slate-400">
                  {alertStatusLabel(alert.status)} since {formatRelativeTime(alert.triggered_at)} (
                  {formatDateTime(alert.triggered_at)})
                </p>
              )}
            </div>

            {copy && (
              <Callout tone={copy.tone} title={copy.title} live="polite">
                {copy.text}
              </Callout>
            )}
            {error && (
              <Callout
                tone="danger"
                action={
                  !data ? (
                    <Button size="sm" onClick={load}>
                      Retry
                    </Button>
                  ) : undefined
                }
              >
                {error}
              </Callout>
            )}

            {data?.state === 'pending' && (
              <>
                <p className="text-sm text-slate-400">
                  Acknowledging tells your team someone is on it. It does not resolve the alert.
                </p>
                <Button
                  variant="accent"
                  size="sm"
                  className="!h-10 w-full justify-center !text-sm"
                  loading={submitting}
                  disabled={submitting}
                  onClick={acknowledge}
                >
                  Acknowledge alert
                </Button>
                {data.expires_at && (
                  <p className="text-center text-xs text-slate-500">
                    This link works until {formatDateTime(data.expires_at)}.
                  </p>
                )}
              </>
            )}

            <div className="text-center text-xs">
              <Link href="/alerts" className="text-slate-400 underline-offset-2 hover:text-slate-200 hover:underline">
                Open the Alerts page
              </Link>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
