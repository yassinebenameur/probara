'use client';

import { useState } from 'react';
import { ChevronRight } from 'lucide-react';
import { FOCUS_RING } from '@/components/ui/focus';
import { getMonitorResult } from '@/lib/api';
import { errorMessage } from '@/lib/errors';
import { failureResponseHeaderLines, failureResponseOf } from '@/lib/http-capture';
import type { CheckResult, HTTPFailureResponse } from '@/lib/types';

/**
 * The "Response" disclosure of a failed HTTP check: status line, headers and
 * the start of the body as the worker stored them, already redacted. List
 * reads leave the capture out (it can be 16 KiB per row), so it is fetched
 * with the result alone the first time the disclosure opens.
 */
export default function FailureResponse({ monitorId, result }: { monitorId: string; result: CheckResult }) {
  const [capture, setCapture] = useState<HTTPFailureResponse | null>(() => failureResponseOf(result));
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const load = async () => {
    if (capture || loading) return;
    setLoading(true);
    setError(null);
    try {
      const whole = await getMonitorResult(monitorId, result.id);
      const fetched = failureResponseOf(whole);
      if (fetched) setCapture(fetched);
      else setError('The response is no longer stored.');
    } catch (err) {
      setError(errorMessage(err, 'Could not load the response'));
    } finally {
      setLoading(false);
    }
  };

  return (
    <details
      className="group/resp min-w-0 basis-full"
      onToggle={(e) => {
        if ((e.currentTarget as HTMLDetailsElement).open) void load();
      }}
    >
      <summary
        className={`inline-flex cursor-pointer select-none items-center gap-1 rounded text-xs text-slate-400 hover:text-slate-200 [&::-webkit-details-marker]:hidden ${FOCUS_RING}`}
      >
        <ChevronRight className="h-3.5 w-3.5 transition-transform duration-fast group-open/resp:rotate-90" strokeWidth={1.75} aria-hidden="true" />
        Response
      </summary>
      <div className="glass-well mt-2 space-y-2 p-3 font-mono text-xs text-slate-300" aria-busy={loading || undefined}>
        {capture ? (
          <CaptureBody capture={capture} />
        ) : error ? (
          <p className="font-sans text-rose-300">{error}</p>
        ) : (
          <p className="font-sans text-slate-500">Loading response…</p>
        )}
      </div>
    </details>
  );
}

function CaptureBody({ capture }: { capture: HTTPFailureResponse }) {
  const headers = failureResponseHeaderLines(capture);
  return (
    <>
      <p className="text-slate-200">{capture.status_line}</p>
      {headers.length > 0 && (
        <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-all text-slate-400">
          {headers.join('\n')}
          {capture.headers_truncated ? '\n…' : ''}
        </pre>
      )}
      {capture.body_note ? (
        <p className="text-slate-500">{capture.body_note}</p>
      ) : capture.body_excerpt ? (
        <pre className="max-h-72 overflow-auto whitespace-pre-wrap break-all">
          {capture.body_excerpt}
          {capture.body_truncated ? '…' : ''}
        </pre>
      ) : (
        <p className="text-slate-500">Empty body</p>
      )}
      <p className="font-sans text-2xs text-slate-500">
        Cookies, credential headers, headers this monitor sends, its secret values and credential-like body fields are
        redacted.{capture.body_truncated ? ' The body is cut to its first 8 KiB.' : ''}
      </p>
    </>
  );
}
