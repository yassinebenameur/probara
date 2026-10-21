'use client';

import type { CSSProperties } from 'react';
import Link from 'next/link';
import { AlertTriangle, CheckCircle2, ChevronDown, MinusCircle, OctagonAlert, X } from 'lucide-react';
import type { DashboardGroup } from '@/lib/types';
import { formatUptime, pluralize } from '@/lib/format';
import {
  type GroupHealth,
  type MonitorStates,
  groupAttentionCount,
  groupMemberStatus,
  monitorsListHref,
} from '@/lib/dashboard-view-model';
import {
  MONITOR_STATUS_DOT,
  MONITOR_STATUS_LABEL,
  MONITOR_STATUS_TONE,
  TONE_TEXT,
  UPTIME_TONE_TEXT,
  uptimeTone,
} from '@/lib/status-tones';
import { FOCUS_RING, FOCUS_RING_INSET } from '@/components/ui/focus';
import IconButton from '@/components/ui/IconButton';
import GroupSparkline from './GroupSparkline';

export const groupLabel = (group: DashboardGroup) => group.tag ?? 'Ungrouped';

const HEALTH_LABEL: Record<GroupHealth, string> = {
  down: 'Down',
  degraded: 'Degraded',
  up: 'Healthy',
  unknown: 'No data',
  empty: 'No monitors',
};

// Down and degraded differ in shape as well as hue, so colour is never the
// only cue; the label is in the accessible name.
function HealthIcon({ health }: { health: GroupHealth }) {
  const cls = 'h-4 w-4 shrink-0';
  switch (health) {
    case 'down':
      return <OctagonAlert className={`${cls} text-rose-400`} strokeWidth={1.75} aria-hidden="true" />;
    case 'degraded':
      return <AlertTriangle className={`${cls} text-amber-400`} strokeWidth={1.75} aria-hidden="true" />;
    case 'up':
      return <CheckCircle2 className={`${cls} text-emerald-400`} strokeWidth={1.75} aria-hidden="true" />;
    default:
      return <MinusCircle className={`${cls} text-slate-500`} strokeWidth={1.75} aria-hidden="true" />;
  }
}

// Healthy uptime stays neutral; warn/bad take the shared tone, so a value has
// the same colour here as in the hero and on the monitors list. This is the
// only place history colours a tile: the icon says what is broken NOW.
export function uptimeClass(pct: number | null | undefined): string {
  const tone = uptimeTone(pct);
  return tone === 'good' ? 'text-slate-200' : UPTIME_TONE_TEXT[tone];
}

interface TileProps {
  group: DashboardGroup;
  health: GroupHealth;
  states?: MonitorStates;
  open: boolean;
  detailId: string;
  onToggle: () => void;
  className?: string;
  style?: CSSProperties;
}

/** One service group: current health, uptime over the range, members needing attention. */
export function GroupTile({ group, health, states, open, detailId, onToggle, className = '', style }: TileProps) {
  const label = groupLabel(group);
  const attention = groupAttentionCount(group, states);
  const empty = group.monitor_count === 0;
  const attentionTone = health === 'down' ? TONE_TEXT.danger : TONE_TEXT.warning;

  return (
    <button
      type="button"
      onClick={onToggle}
      aria-expanded={open}
      aria-controls={open ? detailId : undefined}
      style={style}
      className={`group glass-well relative flex min-w-0 flex-col gap-1 rounded-xl p-3 text-left transition-[background-color,box-shadow,transform] duration-fast ease-standard hover:bg-well-hover active:scale-[0.99] motion-reduce:active:scale-100 ${
        open ? 'bg-well-hover shadow-segment' : ''
      } ${FOCUS_RING} ${className}`.trim()}
    >
      <span className="flex min-w-0 items-center gap-2">
        <HealthIcon health={health} />
        <span className="sr-only">{HEALTH_LABEL[health]}: </span>
        <span className="min-w-0 flex-1 truncate text-sm font-medium text-white">{label}</span>
        <span className={`shrink-0 text-sm font-medium tabular-nums ${empty ? 'text-slate-500' : uptimeClass(group.uptime)}`}>
          <span className="sr-only">uptime </span>
          {empty ? '—' : formatUptime(group.uptime)}
        </span>
      </span>
      <span className="flex min-w-0 items-center gap-1.5 pl-6 text-xs text-slate-500">
        <span className="tabular-nums">{pluralize(group.monitor_count, 'monitor')}</span>
        {attention > 0 && (
          <>
            <span aria-hidden="true">·</span>
            <span className={`truncate tabular-nums ${attentionTone}`}>
              {attention} need{attention === 1 ? 's' : ''} attention
            </span>
          </>
        )}
        <ChevronDown
          className={`ml-auto h-3.5 w-3.5 shrink-0 text-slate-500 transition-transform duration-base ease-standard group-hover:text-slate-300 ${open ? 'rotate-180' : ''}`}
          strokeWidth={1.75}
          aria-hidden="true"
        />
      </span>
    </button>
  );
}

interface DetailProps {
  id: string;
  group: DashboardGroup;
  states?: MonitorStates;
  /** null while loading; [] when there is nothing to draw. */
  sparkline: (number | null)[] | null;
  onClose: () => void;
  className?: string;
}

/** The opened group, as a full-width row under its tile: members (worst first) and the uptime sparkline. */
export function GroupDetail({ id, group, states, sparkline, onClose, className = '' }: DetailProps) {
  const label = groupLabel(group);
  const href = group.tag ? monitorsListHref({ tags: [group.tag] }) : '/monitors';
  const hidden = group.monitor_count - group.members.length;

  return (
    <div
      id={id}
      role="region"
      aria-label={`${label} members`}
      className={`glass-well animate-fade-in rounded-xl px-4 pb-3 pt-2 ${className}`.trim()}
    >
      <div className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-2">
        <h3 className="min-w-0 truncate text-sm font-semibold text-white">{label}</h3>
        <div className="flex items-center gap-2">
          {sparkline === null ? (
            <span className="text-xs text-slate-500">Loading trend…</span>
          ) : sparkline.length > 0 ? (
            <GroupSparkline buckets={sparkline} />
          ) : (
            <span className="text-xs text-slate-500">No trend data</span>
          )}
        </div>
        <div className="ml-auto flex items-center gap-2">
          <Link
            href={href}
            className={`rounded text-xs text-cyan-300 transition-colors hover:text-cyan-200 hover:underline ${FOCUS_RING}`}
          >
            View all {group.monitor_count}
          </Link>
          <IconButton icon={<X strokeWidth={1.75} />} label={`Close ${label}`} onClick={onClose} size="sm" />
        </div>
      </div>

      {group.members.length === 0 ? (
        <p className="py-2 text-sm text-slate-500">No monitors in this group.</p>
      ) : (
        <ul className="mt-1 grid gap-x-6 [grid-template-columns:repeat(auto-fill,minmax(18rem,1fr))]">
          {group.members.map((m) => {
            const status = groupMemberStatus(m, states);
            return (
              <li key={m.monitor_id} className="min-w-0">
                <Link
                  href={`/monitors/${m.monitor_id}`}
                  className={`-mx-2 grid grid-cols-[auto_minmax(0,1fr)_4rem_4.5rem] items-center gap-2 rounded-md px-2 py-1.5 text-sm transition-colors hover:bg-well-hover ${FOCUS_RING_INSET}`}
                >
                  <span className={`h-2 w-2 rounded-full ${MONITOR_STATUS_DOT[status]}`} aria-hidden="true" />
                  <span className="truncate text-slate-200">{m.monitor_name}</span>
                  <span className={`text-right tabular-nums ${uptimeClass(m.uptime)}`}>{formatUptime(m.uptime)}</span>
                  <span className={`text-right text-xs ${TONE_TEXT[MONITOR_STATUS_TONE[status]]}`}>
                    {MONITOR_STATUS_LABEL[status]}
                  </span>
                </Link>
              </li>
            );
          })}
        </ul>
      )}
      {hidden > 0 && (
        <p className="mt-1 text-xs text-slate-500">
          Worst {group.members.length} of {group.monitor_count} shown.
        </p>
      )}
    </div>
  );
}
