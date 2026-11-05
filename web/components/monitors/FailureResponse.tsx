'use client';

import { ChevronRight } from 'lucide-react';
import type { HTTPFailureResponse } from '@/lib/types';
import { failureResponseHeaderLines } from '@/lib/http-capture';

/**
 * The "Response" expander of a failed HTTP check that captured its response
 * (capture_failure_response): status line, redacted headers and the start
 * of the body, as the worker stored them.
 */
export default function FailureResponse({ capture }: { capture: HTTPFailureResponse }) {
  const headers = failureResponseHeaderLines(capture);
  return (
    <details className="group/resp min-w-0 basis-full">
      <summary className="inline-flex cursor-pointer select-none items-center gap-1 rounded text-xs text-slate-400 hover:text-slate-200 [&::-webkit-details-marker]:hidden">
        <ChevronRight className="h-3.5 w-3.5 transition-transform group-open/resp:rotate-90" strokeWidth={1.75} aria-hidden="true" />
        Response
      </summary>
      <div className="glass-well mt-2 space-y-2 p-3 font-mono text-xs text-slate-300">
        <p className="text-slate-200">{capture.status_line}</p>
        {headers.length > 0 && (
          <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-all text-slate-400">
            {headers.join('\n')}
            {capture.headers_truncated ? '\n…' : ''}
          </pre>
        )}
        {capture.body_excerpt ? (
          <pre className="max-h-72 overflow-auto whitespace-pre-wrap break-all">
            {capture.body_excerpt}
            {capture.body_truncated ? '…' : ''}
          </pre>
        ) : (
          <p className="text-slate-500">Empty body</p>
        )}
        <p className="font-sans text-2xs text-slate-500">
          Credential-like headers, headers this monitor sends and its secret values are redacted.
          {capture.body_truncated ? ' The body is cut at the monitor’s capture size.' : ''}
        </p>
      </div>
    </details>
  );
}
