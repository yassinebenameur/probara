'use client';

import type { ReactNode } from 'react';
import { DetailRow } from '@/components/monitors/MonitorFactsCard';
import CopyableTarget from '@/components/ui/CopyableTarget';
import InfoTip from '@/components/ui/InfoTip';
import { serverFacts } from '@/lib/monitor-server-facts';
import { latestSIPMetrics, sipFacts, sipMediaOutdatedWorkersHint } from '@/lib/sip-metrics';
import type { CheckResult, Monitor, SIPMonitorConfig } from '@/lib/types';

// Type-specific facts for the sidebar's Server panel: what the database or
// broker reported about itself, the TCP TLS version, what the SIP server
// answered. The database and TCP facts are lib/monitor-server-facts.ts, the
// SIP ones lib/sip-metrics.ts. Interval and timeout
// live in the page header. Empty when the type has nothing to add, so the
// panel is not rendered at all.
export function serverFactRows(monitor: Monitor, results: CheckResult[]): ReactNode[] {
  const rows: ReactNode[] = [];
  for (const fact of serverFacts(monitor, results)) {
    rows.push(
      <DetailRow key={fact.key} label={fact.label} tone={fact.tone}>
        {fact.value}
      </DetailRow>
    );
  }
  if (monitor.type === 'sip') {
    const sip = latestSIPMetrics(results);
    const config = monitor.config as SIPMonitorConfig;
    for (const fact of sip ? sipFacts(sip, config.tls_min_days_valid, config.call?.media) : []) {
      rows.push(
        <DetailRow key={fact.key} label={fact.label} tone={fact.tone}>
          {fact.copyable ? (
            <CopyableTarget value={fact.value} label={fact.label} href={null} size="xs" className="min-w-0 justify-end" />
          ) : fact.tip ? (
            // Only the text truncates: the row's value cell clips, and the
            // tip at the end would be the first thing cut off.
            <span className="inline-flex max-w-full items-center gap-1">
              <span className="min-w-0 truncate">{fact.value}</span>
              <InfoTip inLabel ariaLabel={`About ${fact.label.toLowerCase()}`}>
                {fact.tip}
              </InfoTip>
            </span>
          ) : (
            fact.value
          )}
        </DetailRow>
      );
    }
    // A worker that predates call quality refuses the check before it gets
    // far enough to write a SIP envelope, so this reads the error.
    const outdated = config.call?.media ? sipMediaOutdatedWorkersHint(results) : '';
    if (outdated) {
      rows.push(
        <DetailRow key="media-outdated" label="Workers" tone="warning">
          {outdated}
        </DetailRow>
      );
    }
  }
  return rows;
}
