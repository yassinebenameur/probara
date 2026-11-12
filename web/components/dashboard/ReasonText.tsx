'use client';

import { shortFailureReason } from '@/lib/monitor-utils';

// The short cause ("connection refused", "HTTP 503"); the raw worker message
// stays one hover away in the title.
export function ReasonText({ message, className = '' }: { message: string | null | undefined; className?: string }) {
  const short = shortFailureReason(message);
  if (!short) return null;
  return (
    <span className={`min-w-0 truncate text-slate-400 ${className}`} title={message ?? undefined}>
      {short}
    </span>
  );
}
