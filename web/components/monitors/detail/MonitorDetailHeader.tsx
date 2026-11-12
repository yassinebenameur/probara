'use client';

import { Eraser, Pause, Pencil, Play, RefreshCw, Trash2 } from 'lucide-react';
import { Monitor } from '@/lib/types';
import PageHeader from '@/components/ui/PageHeader';
import Pill from '@/components/ui/Pill';
import RowMenu, { RowMenuItem, RowMenuSeparator } from '@/components/ui/RowMenu';
import TypeBadge from '@/components/ui/TypeBadge';
import Button from '@/components/ui/Button';
import CopyableTarget from '@/components/ui/CopyableTarget';
import { RelativeTime } from '@/components/ui/RelativeTime';
import { monitorTargetLabel, type MonitorDisplayStatus } from '@/lib/monitor-utils';
import { MONITOR_STATUS_LABEL, MONITOR_STATUS_TONE } from '@/lib/status-tones';
import { formatDuration } from '@/lib/format';

interface MonitorDetailHeaderProps {
  monitor: Monitor;
  status: MonitorDisplayStatus;
  target: string | null;
  /** The monitor never runs a check of its own (group, agent, push). */
  isPassive: boolean;
  isCall: boolean;
  isGroup: boolean;
  lastCheckAt?: string;
  canWrite: boolean;
  /** May run, pause and resume: writers, tenant admins only for a SIP test call. */
  canOperate: boolean;
  runningCheck: boolean;
  togglingEnabled: boolean;
  /** Runs the check, or asks first for a SIP test call. */
  onRunCheck: () => void;
  onToggleEnabled: () => void;
  onEditSettings: () => void;
  onClearHistory: () => void;
  onDelete: () => void;
}

/** The monitor page's header: name, status, target and cadence, and the actions. */
export default function MonitorDetailHeader({
  monitor,
  status,
  target,
  isPassive,
  isCall,
  isGroup,
  lastCheckAt,
  canWrite,
  canOperate,
  runningCheck,
  togglingEnabled,
  onRunCheck,
  onToggleEnabled,
  onEditSettings,
  onClearHistory,
  onDelete,
}: MonitorDetailHeaderProps) {
  return (
    <PageHeader
      breadcrumb={[{ label: 'Monitors', href: '/monitors' }, { label: monitor.name }]}
      title={monitor.name}
      titleAside={
        <Pill tone={MONITOR_STATUS_TONE[status]} dot>
          {MONITOR_STATUS_LABEL[status]}
        </Pill>
      }
      // One line of facts on desktop: the target truncates (full value in
      // its tooltip, copy/open stay visible) so "checked …" never wraps
      // alone. Phones put the target on its own line above the facts.
      subtitle={
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 tabular-nums sm:flex-nowrap">
          <span className="inline-flex min-w-0 items-center gap-2 max-sm:basis-full">
            <TypeBadge type={monitor.type} config={monitor.config} />
            {target && (
              <CopyableTarget
                value={target}
                label={monitorTargetLabel(monitor.type)}
                size="sm"
                className="min-w-0"
                textClassName="text-slate-400"
              />
            )}
          </span>
          <span className="shrink-0 whitespace-nowrap">every {formatDuration(monitor.interval_seconds)}</span>
          {!isPassive && monitor.timeout_seconds > 0 && (
            <span className="shrink-0 whitespace-nowrap">timeout {formatDuration(monitor.timeout_seconds)}</span>
          )}
          {/* Agent results are scheduler-written freshness verdicts, not
              reports; the agent view shows the real last report itself. */}
          {lastCheckAt && monitor.type !== 'agent' && (
            <RelativeTime value={lastCheckAt} prefix="checked " className="shrink-0 whitespace-nowrap" />
          )}
        </div>
      }
      action={
        canWrite ? (
          <>
            {!isPassive && canOperate && (
              <Button
                variant="ghost"
                size="sm"
                icon={<RefreshCw className={`h-4 w-4 ${runningCheck ? 'animate-spin' : ''}`} strokeWidth={1.75} />}
                onClick={onRunCheck}
                disabled={runningCheck}
                title={
                  isCall
                    ? 'Place the test call now from every location of this monitor'
                    : 'Run the check now on every location of this monitor'
                }
              >
                {runningCheck ? 'Running…' : 'Run check'}
              </Button>
            )}
            {canOperate && (
              <Button
                variant="ghost"
                size="sm"
                icon={
                  monitor.enabled ? (
                    <Pause className="h-4 w-4" strokeWidth={1.75} />
                  ) : (
                    <Play className="h-4 w-4" strokeWidth={1.75} />
                  )
                }
                onClick={onToggleEnabled}
                loading={togglingEnabled}
              >
                {monitor.enabled ? 'Pause' : 'Resume'}
              </Button>
            )}
            <RowMenu label="More actions">
              <RowMenuItem
                icon={<Pencil className="h-3.5 w-3.5" strokeWidth={1.75} />}
                onSelect={onEditSettings}
              >
                Edit settings
              </RowMenuItem>
              <RowMenuItem
                icon={<Eraser className="h-3.5 w-3.5" strokeWidth={1.75} />}
                onSelect={onClearHistory}
              >
                {isGroup ? 'Clear group history…' : 'Clear history…'}
              </RowMenuItem>
              <RowMenuSeparator />
              <RowMenuItem
                danger
                icon={<Trash2 className="h-3.5 w-3.5" strokeWidth={1.75} />}
                onSelect={onDelete}
              >
                Delete…
              </RowMenuItem>
            </RowMenu>
          </>
        ) : undefined
      }
    />
  );
}
