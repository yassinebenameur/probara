'use client';

import { Monitor, MonitorAnalyticsResponse } from '@/lib/types';
import { formatDateTimeFull, RANGES } from '@/lib/format';
import { useState } from 'react';
import { Check, Copy } from 'lucide-react';
import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';

interface MonitorDetailJsonProps {
  monitor: Monitor;
  /** The Overview's current analytics, whose provenance is listed here. */
  analytics?: MonitorAnalyticsResponse | null;
}

export default function MonitorDetailJson({ monitor, analytics }: MonitorDetailJsonProps) {
  const [copied, setCopied] = useState(false);

  const handleCopy = async () => {
    const jsonString = JSON.stringify(monitor, null, 2);
    try {
      await navigator.clipboard.writeText(jsonString);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch (err) {
      console.error('Failed to copy JSON:', err);
    }
  };

  const jsonString = JSON.stringify(monitor, null, 2);

  // Where the Overview's numbers come from: pipeline detail that has no
  // place next to the headline but matters when a number looks wrong.
  const provenance = analytics
    ? [
        { label: 'Range', value: RANGES[analytics.range]?.long ?? analytics.range },
        {
          label: 'Method',
          value: analytics.summary.method === 'interval' ? 'Interval (state timeline)' : 'Sampled (check counts)',
        },
        { label: 'Source', value: analytics.source === 'rollup' ? 'Rollups and raw tail' : 'Raw results' },
        { label: 'Coverage', value: analytics.is_partial ? 'Partial' : 'Complete' },
        { label: 'Coverage start', value: formatDateTimeFull(analytics.coverage_start) },
        { label: 'Generated', value: formatDateTimeFull(analytics.generated_at) },
      ]
    : [];

  return (
    <div className="space-y-6">
      <Panel
        title="Raw JSON"
        subtitle="Monitor configuration payload"
        actions={
          <Button
            variant="ghost"
            size="xs"
            onClick={handleCopy}
            icon={
              copied ? (
                <Check className="h-3.5 w-3.5 text-emerald-400" strokeWidth={1.75} />
              ) : (
                <Copy className="h-3.5 w-3.5" strokeWidth={1.75} />
              )
            }
          >
            {copied ? 'Copied!' : 'Copy'}
          </Button>
        }
      >
        <pre className="glass-well overflow-x-auto whitespace-pre p-4 font-mono text-xs text-slate-300">{jsonString}</pre>
      </Panel>
      {provenance.length > 0 && (
        <Panel title="Analytics source" subtitle="How the Overview's figures were computed">
          <dl className="grid gap-x-6 gap-y-2.5 text-ui sm:grid-cols-2 lg:grid-cols-3">
            {provenance.map((row) => (
              <div key={row.label} className="flex justify-between gap-3 sm:block">
                <dt className="text-slate-400">{row.label}</dt>
                <dd className="tabular-nums text-slate-200 sm:mt-0.5">{row.value}</dd>
              </div>
            ))}
          </dl>
        </Panel>
      )}
    </div>
  );
}
