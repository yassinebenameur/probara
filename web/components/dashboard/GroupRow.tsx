'use client';

import { useState } from 'react';
import Link from 'next/link';
import { ChevronDown, AlertTriangle, CheckCircle2, MinusCircle } from 'lucide-react';
import type { DashboardGroup, DashboardRange } from '@/lib/types';
import { getDashboardGroupSparkline } from '@/lib/api';
import GroupSparkline from './GroupSparkline';

interface Props {
  group: DashboardGroup;
  range: DashboardRange;
  filterTags: string[];
}

function statusIcon(group: DashboardGroup) {
  if (group.monitor_count === 0) {
    return <MinusCircle className="h-4 w-4 text-slate-500" strokeWidth={1.8} />;
  }
  if (group.attention_count > 0) {
    return <AlertTriangle className="h-4 w-4 text-amber-300" strokeWidth={1.8} />;
  }
  return <CheckCircle2 className="h-4 w-4 text-emerald-300" strokeWidth={1.8} />;
}

function statusColor(status: string | null | undefined): string {
  if (status === 'success') return 'text-emerald-300';
  if (status === 'failure') return 'text-rose-300';
  if (status === 'error') return 'text-amber-300';
  return 'text-slate-400';
}

export default function GroupRow({ group, range, filterTags }: Props) {
  const [open, setOpen] = useState(false);
  const [sparkline, setSparkline] = useState<number[] | null>(null);
  const [loading, setLoading] = useState(false);

  const label = group.tag ?? 'Ungrouped';
  const linkHref = group.tag ? `/monitors?tag=${encodeURIComponent(group.tag)}` : '/monitors';

  async function toggle() {
    const next = !open;
    setOpen(next);
    if (next && sparkline === null && !loading) {
      setLoading(true);
      try {
        const resp = await getDashboardGroupSparkline({
          group: group.tag,
          range,
          tags: filterTags,
        });
        setSparkline(resp.buckets);
      } catch {
        setSparkline([]);
      } finally {
        setLoading(false);
      }
    }
  }

  return (
    <div>
      <button
        type="button"
        onClick={toggle}
        aria-expanded={open}
        className="grid w-full grid-cols-[auto_1fr_auto_auto_auto] items-center gap-4 px-5 py-3 text-left transition-colors hover:bg-white/[0.02]"
      >
        {statusIcon(group)}
        <span className="min-w-0 truncate text-sm font-medium text-white">{label}</span>
        <span className="text-xs tabular-nums text-slate-400">
          {group.monitor_count} monitor{group.monitor_count === 1 ? '' : 's'}
        </span>
        <span className={`text-sm font-medium tabular-nums ${group.attention_count > 0 ? 'text-amber-300' : 'text-emerald-300'}`}>
          {group.monitor_count === 0 ? '—' : `${group.uptime.toFixed(2)}%`}
        </span>
        <ChevronDown className={`h-4 w-4 text-slate-500 transition-transform ${open ? 'rotate-180' : ''}`} strokeWidth={2} />
      </button>

      {open && (
        <div className="bg-slate-950/35 px-5 pb-4">
          {group.members.length === 0 ? (
            <p className="py-2 text-sm text-slate-500">No monitors in this group.</p>
          ) : (
            <ul className="space-y-1.5 py-2">
              {group.members.map((m) => (
                <li key={m.monitor_id} className="grid grid-cols-[1fr_auto_auto] items-center gap-3 text-sm">
                  <Link
                    href={`/monitors/${m.monitor_id}`}
                    className="truncate text-slate-200 transition-colors hover:text-cyan-300"
                  >
                    {m.monitor_name}
                  </Link>
                  <span className="tabular-nums text-slate-400">{m.uptime.toFixed(2)}%</span>
                  <span className={`text-xs ${statusColor(m.current_status)}`}>{m.current_status ?? 'paused'}</span>
                </li>
              ))}
            </ul>
          )}

          <div className="mt-3 flex items-center justify-between">
            {loading ? (
              <span className="text-xs text-slate-500">Loading sparkline…</span>
            ) : sparkline && sparkline.length > 0 ? (
              <GroupSparkline buckets={sparkline} />
            ) : (
              <span className="text-xs text-slate-600">No sparkline data</span>
            )}
            <Link href={linkHref} className="text-xs text-cyan-400 transition-colors hover:text-cyan-300">
              View all {group.monitor_count} →
            </Link>
          </div>
        </div>
      )}
    </div>
  );
}
