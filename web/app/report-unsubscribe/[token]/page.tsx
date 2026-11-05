'use client';

import { useCallback, useEffect, useState } from 'react';
import { useParams } from 'next/navigation';
import BrandMark from '@/components/ui/BrandMark';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import {
  parseReportUnsubscribeResponse,
  reportUnsubscribePath,
  type ReportUnsubscribeResponse,
} from '@/lib/report-unsubscribe';

// Confirm page behind a scheduled SLA report's unsubscribe link. Rendered
// outside the app shell (see Layout/AuthGuard): recipients are often not
// Probara users. Loading only reads (GET); unsubscribing is the button's POST.
export default function ReportUnsubscribePage() {
  const params = useParams<{ token: string }>();
  const token = typeof params?.token === 'string' ? params.token : '';
  const [data, setData] = useState<ReportUnsubscribeResponse | null>(null);
  const [error, setError] = useState('');
  const [submitting, setSubmitting] = useState(false);

  const request = useCallback(
    async (method: 'GET' | 'POST') => {
      const res = await fetch(reportUnsubscribePath(token), { method, credentials: 'omit', cache: 'no-store' });
      const body = await res.json().catch(() => null);
      const parsed = parseReportUnsubscribeResponse(res.status, body);
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
        if (!ignore) setError('Could not load this link. Check your connection and reload the page.');
      });
    return () => {
      ignore = true;
    };
  }, [request]);

  const unsubscribe = async () => {
    setSubmitting(true);
    setError('');
    try {
      setData(await request('POST'));
    } catch {
      setError('Unsubscribing failed. Try again.');
    } finally {
      setSubmitting(false);
    }
  };

  const what = data?.sla_name ? `the ${data.sla_name} SLA reports` : 'these SLA reports';

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
              <h1 className="text-title font-semibold tracking-tight text-white">Unsubscribe from SLA reports</h1>
              {data?.recipient && <p className="mt-2 text-sm text-slate-400">{data.recipient}</p>}
            </div>

            {data?.state === 'unsubscribed' && (
              <Callout tone="success" title="Unsubscribed" live="polite">
                You will not receive {what} from this schedule again.
              </Callout>
            )}
            {data?.state === 'invalid' && (
              <Callout tone="danger" title="Link not valid">
                This link is malformed, or its report schedule no longer exists or no longer mails this address.
              </Callout>
            )}
            {error && <Callout tone="danger">{error}</Callout>}

            {data?.state === 'subscribed' && (
              <>
                <p className="text-sm text-slate-400">
                  Stop receiving {what} at this address. Other recipients of the schedule are not affected.
                </p>
                <Button
                  variant="accent"
                  size="sm"
                  className="w-full justify-center"
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
