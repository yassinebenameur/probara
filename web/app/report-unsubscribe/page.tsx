'use client';

import { useCallback, useEffect, useState } from 'react';
import BrandMark from '@/components/ui/BrandMark';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import {
  ACTION_TOKEN_HEADER,
  REPORT_UNSUBSCRIBE_PATH,
  describeReportUnsubscribeState,
  parseReportUnsubscribeResponse,
  takeActionToken,
  type ReportUnsubscribeResponse,
} from '@/lib/report-unsubscribe';

// Confirm page behind the unsubscribe link of an emailed SLA report.
// Rendered outside the app shell (see Layout and AuthGuard): the token is the
// credential, so it works signed out. See lib/report-unsubscribe.ts.
export default function ReportUnsubscribePage() {
  // undefined until the hash has been read on the client.
  const [token, setToken] = useState<string | undefined>(undefined);
  const [data, setData] = useState<ReportUnsubscribeResponse | null>(null);
  const [error, setError] = useState('');
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    setToken(takeActionToken());
  }, []);

  const request = useCallback(
    async (method: 'GET' | 'POST') => {
      const res = await fetch(REPORT_UNSUBSCRIBE_PATH, {
        method,
        headers: { [ACTION_TOKEN_HEADER]: token ?? '' },
        credentials: 'omit',
        cache: 'no-store',
      });
      const body = await res.json().catch(() => null);
      const parsed = parseReportUnsubscribeResponse(res.status, body);
      if (!parsed) throw new Error('unexpected response');
      return parsed;
    },
    [token],
  );

  const load = useCallback(() => {
    setError('');
    request('GET')
      .then(setData)
      .catch(() => setError('Could not load this link. Check your connection and try again.'));
  }, [request]);

  useEffect(() => {
    if (token === undefined) return;
    if (token === '') {
      setData({ state: 'invalid' });
      return;
    }
    load();
  }, [token, load]);

  const unsubscribe = async () => {
    setSubmitting(true);
    setError('');
    try {
      const parsed = await request('POST');
      setData((prev) => ({ ...parsed, sla_name: parsed.sla_name ?? prev?.sla_name }));
    } catch {
      setError('Unsubscribing failed. Try again.');
    } finally {
      setSubmitting(false);
    }
  };

  const copy = data ? describeReportUnsubscribeState(data.state) : null;

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
              <p className="text-xs font-medium uppercase tracking-wide text-slate-400">SLA report emails</p>
              <h1 className="mt-1 break-words text-title font-semibold tracking-tight text-white">
                {data?.sla_name ?? 'Unsubscribe'}
              </h1>
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

            {data?.state === 'subscribed' && (
              <>
                <p className="text-sm text-slate-400">
                  You receive this SLA&apos;s availability report by email when each reporting period closes. Unsubscribing
                  stops those emails for this address only.
                </p>
                <Button
                  variant="accent"
                  size="sm"
                  className="!h-10 w-full justify-center !text-sm"
                  loading={submitting}
                  disabled={submitting}
                  onClick={unsubscribe}
                >
                  Unsubscribe
                </Button>
              </>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
