'use client';

import Button from '@/components/ui/Button';

export function SectionCard({
  title,
  action,
  children,
  className,
}: {
  title: string;
  action?: React.ReactNode;
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <section className={`glass flex min-w-0 flex-col overflow-hidden xl:h-full ${className ?? ''}`}>
      <div className="flex min-w-0 shrink-0 flex-wrap items-center justify-between gap-2 px-5 py-4">
        <h2 className="min-w-0 text-title font-semibold text-white">{title}</h2>
        {action && <div className="min-w-0 text-ui text-slate-500">{action}</div>}
      </div>
      <div className="flex min-h-0 min-w-0 flex-1 flex-col">{children}</div>
    </section>
  );
}

// A section whose data failed to load says so; an empty state here would
// claim an all-clear the page never checked.
export function SectionLoadError({ onRetry }: { onRetry: () => void }) {
  return (
    <div className="flex items-center justify-between gap-3 px-5 py-4 text-sm text-slate-400">
      <span>Couldn&apos;t load this section.</span>
      <Button variant="ghost" size="xs" onClick={onRetry}>
        Retry
      </Button>
    </div>
  );
}
