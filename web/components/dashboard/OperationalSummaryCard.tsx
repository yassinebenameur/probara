'use client';

import { Fragment } from 'react';
import Button from '@/components/ui/Button';
import { FOCUS_RING } from '@/components/ui/focus';
import Link from 'next/link';
import { OperationalSummary } from '@/lib/dashboard-view-model';

const SUMMARY_DOT: Record<OperationalSummary['tone'], string> = {
  critical: 'bg-rose-500',
  attention: 'bg-amber-500',
  stable: 'bg-emerald-500',
  clean: 'bg-emerald-500',
};

// Wraps between facts, never inside one ("3 / degraded").
function HeadlineFacts({ label }: { label: string }) {
  const facts = label.split(', ');
  return (
    <>
      {facts.map((fact, i) => (
        <Fragment key={i}>
          {i > 0 && ' '}
          <span className="whitespace-nowrap">
            {fact}
            {i < facts.length - 1 && ','}
          </span>
        </Fragment>
      ))}
    </>
  );
}

export type Metric = OperationalSummary['metrics'][number];

function MetricTile({ metric }: { metric: Metric }) {
  // Healthy values stay neutral so only a problem carries colour.
  const valueTone =
    metric.tone === 'critical'
      ? 'text-rose-300'
      : metric.tone === 'attention'
        ? 'text-amber-300'
        : 'text-white';
  const body = (
    <>
      <p className="truncate text-xs text-slate-400">{metric.label}</p>
      {/* Keyed on the value: a changed number rises in once, the true value
          at once (no count-up). */}
      <p key={metric.value} className={`mt-0.5 animate-value-in text-xl font-semibold tabular-nums sm:text-metric ${valueTone}`}>
        {metric.value}
      </p>
      {metric.detail && <p className="mt-0.5 hidden truncate text-xs text-slate-500 sm:block">{metric.detail}</p>}
    </>
  );
  const base = `glass-well min-w-0 px-3.5 py-3 ${metric.wideOnly ? 'hidden 3xl:block' : ''}`;
  return metric.href ? (
    <Link
      href={metric.href}
      title={metric.title}
      className={`${base} transition-colors duration-fast hover:bg-well-hover active:scale-[0.99] ${FOCUS_RING}`}
    >
      {body}
    </Link>
  ) : (
    <div className={base} title={metric.title}>
      {body}
    </div>
  );
}

export function OperationalSummaryCard({ summary }: { summary: OperationalSummary }) {
  const action = summary.primaryAction;

  return (
    <section className="glass grid gap-4 p-4 sm:p-5 lg:grid-cols-[minmax(18rem,26rem)_minmax(0,1fr)] lg:items-center lg:gap-6">
      <div className="min-w-0">
        <div className="flex items-start gap-2.5">
          <span className={`mt-2.5 h-2 w-2 shrink-0 rounded-full ${SUMMARY_DOT[summary.tone]}`} aria-hidden="true" />
          {/* On mobile the headline is the link; the button joins it from sm. */}
          <h2 className="text-balance text-lg font-semibold leading-7 text-white">
            {action ? (
              // Below sm the button is hidden and this is the only way in, so
              // it wears the link colour and underline that touch can't hover for.
              <Link
                href={action.href}
                className={`rounded text-cyan-300 underline decoration-cyan-500/40 underline-offset-4 transition-colors sm:text-white sm:no-underline sm:hover:text-cyan-300 sm:hover:underline ${FOCUS_RING}`}
              >
                <HeadlineFacts label={summary.label} />
              </Link>
            ) : (
              <HeadlineFacts label={summary.label} />
            )}
          </h2>
        </div>
        {summary.description && (
          <p className="mt-1 hidden max-w-xl pl-[1.125rem] text-sm text-slate-400 sm:block">{summary.description}</p>
        )}
        {action && (
          <div className="mt-3 hidden pl-[1.125rem] sm:block">
            <Button variant="ghost" size="sm" asChild>
              <Link href={action.href}>{action.label}</Link>
            </Button>
          </div>
        )}
      </div>

      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4 lg:grid-cols-2 xl:grid-cols-4 3xl:grid-cols-6">
        {summary.metrics.map((metric) => (
          <MetricTile key={metric.label} metric={metric} />
        ))}
      </div>
    </section>
  );
}
