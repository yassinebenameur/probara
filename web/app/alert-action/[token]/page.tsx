'use client';

import { useCallback, useEffect, useState } from 'react';
import Link from 'next/link';
import { useParams } from 'next/navigation';
import BrandMark from '@/components/ui/BrandMark';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import {
  alertActionPath,
  describeAlertActionState,
  parseAlertActionResponse,
  type AlertActionResponse,
} from '@/lib/alert-action';
import { formatDateTime, formatRelativeTime } from '@/lib/format';

// Confirm page behind a notification's signed acknowledge link. Rendered
// outside the app shell (see Layout/AuthGuard): the token is the credential,
// so it works signed out. Loading only reads (GET); acknowledging is the
// button's POST, because link scanners prefetch every URL in a message.
export default function AlertActionPage() {
  const params = useParams<{ token: string }>();
  const token = typeof params?.token === 'string' ? params.token : '';
  const [data, setData] = useState<AlertActionResponse | null>(null);
  const [error, setError] = useState('');
  const [submitting, setSubmitting] = useState(false);

  const request = useCallback(
    async (method: 'GET' | 'POST') => {
      const res = await fetch(alertActionPath(token), { method, credentials: 'omit', cache: 'no-store' });
      const body = await res.json().catch(() => null);
      const parsed = parseAlertActionResponse(res.status, body);
      if (!parsed) throw new Error('unexpected response');
      return parsed;
    },
    [token]
  );

  useEffect(() => {
    let ignore = false;
    request('GET')
      .then((parsed) => {
        if (!ignore) setData(parsed);
      })
      .catch(() => {
        if (!ignore) setError('Could not load this alert. Check your connection and reload the page.');
      });
    return () => {
      ignore = true;
    };
  }, [request]);

  const acknowledge = async () => {
    setSubmitting(true);
    setError('');
    try {
      const parsed = await request('POST');
      // A refusal keeps the alert summary from the GET for context.
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
              <h1 className="text-title font-semibold tracking-tight text-white">
                {alert ? alert.subject : 'Acknowledge alert'}
              </h1>
              {alert && (
                <p className="mt-2 text-sm text-slate-400">
                  Alert triggered {formatRelativeTime(alert.triggered_at)} ({formatDateTime(alert.triggered_at)})
                </p>
              )}
            </div>

            {copy && (
              <Callout tone={copy.tone} title={copy.title} live="polite">
                {copy.text}
              </Callout>
            )}
            {error && <Callout tone="danger">{error}</Callout>}

            {data?.state === 'pending' && (
              <>
                <p className="text-sm text-slate-400">
                  Acknowledging tells your team you are on it and, by default, stops the reminders and later
                  escalation tiers. It is recorded in the audit log as a magic-link acknowledgement.
                </p>
                <Button
                  variant="accent"
                  size="sm"
                  className="w-full justify-center"
                  loading={submitting}
                  disabled={submitting}
                  onClick={acknowledge}
                >
                  Acknowledge
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
