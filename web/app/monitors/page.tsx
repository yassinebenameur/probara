'use client';

import PrometheusDetails from '@/components/monitors/PrometheusDetails';
import { useState, useEffect, useMemo, useRef } from 'react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { Plus, Download, Upload, Search, Tag, ChevronDown, ChevronRight, Activity, CheckSquare, FolderPlus, Move, Trash2, Bell, BellOff, Layers, X, Pencil, Copy, Pause, Play } from 'lucide-react';
import RowMenu, { RowMenuItem, RowMenuSeparator, useCloseRowMenu } from '@/components/ui/RowMenu';
import Button from '@/components/ui/Button';
import Pill from '@/components/ui/Pill';
import Callout from '@/components/ui/Callout';
import IconButton from '@/components/ui/IconButton';
import SharedSelectCheckbox from '@/components/ui/SelectCheckbox';
import FilterChip from '@/components/ui/FilterChip';
import PageHeader from '@/components/ui/PageHeader';
import SharedEmptyState from '@/components/ui/EmptyState';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import CopyableTarget from '@/components/ui/CopyableTarget';
import { useToast } from '@/components/ui/ToastProvider';
import { BulkAlertingModal } from '@/components/monitors/BulkAlertingModal';
import TagPill from '@/components/monitors/TagPill';
import { Monitor, CheckResult, AgentMetrics, HTTPDisplayValue } from '@/lib/types';
import {
  deleteMonitor,
  getMonitorResults,
  getGroupMembers,
  createMonitor,
  addMonitorsToGroup,
  removeMonitorsFromGroup,
  getSyntheticBrowserScreenshotUrl,
  toggleMonitorEnabled,
  exportMonitors,
  snoozeMonitor,
} from '@/lib/api';
import { getAllMonitors, sortMonitorsByName } from '@/lib/monitor-list';
import { getApiKey } from '@/lib/auth';
import { useCurrentUser } from '@/components/providers/CurrentUserProvider';
import {
  calculateUptime,
  countOperationalResults,
  displayValueLabel,
  formatInterval,
  formatTimeAgo,
  getEffectiveMonitorStatus,
  getHTTPDisplayValues,
  isPlatformResult,
  monitorTargetLabel,
  MonitorDisplayStatus,
  calculateLatencyStats,
} from '@/lib/monitor-utils';
import { monitorTypeLabel } from '@/lib/monitor-type-labels';

const DEBUG_INGEST_URL = process.env.NEXT_PUBLIC_DEBUG_INGEST_URL;
const NO_GROUP_VALUE = '__no_group__';

function groupContainsGroup(
  rootGroupID: string,
  candidateDescendantID: string,
  groupMembersMap: Record<string, Monitor[]>
): boolean {
  const visited = new Set<string>();
  const stack = [rootGroupID];

  while (stack.length > 0) {
    const currentGroupID = stack.pop();
    if (!currentGroupID) continue;
    if (currentGroupID === candidateDescendantID) return true;
    if (visited.has(currentGroupID)) continue;

    visited.add(currentGroupID);
    const members = groupMembersMap[currentGroupID] || [];
    members.forEach((member) => {
      if (member.type === 'group' && !visited.has(member.id)) {
        stack.push(member.id);
      }
    });
  }

  return false;
}

function debugIngest(payload: Record<string, unknown>) {
  if (!DEBUG_INGEST_URL) return;
  fetch(DEBUG_INGEST_URL, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  }).catch(() => {});
}

// Status dot component
function StatusDot({ status }: { status: MonitorDisplayStatus }) {
  const colors = {
    up: 'bg-emerald-500',
    down: 'bg-rose-500',
    degraded: 'bg-amber-500',
    paused: 'bg-slate-500',
    maintenance: 'bg-sky-500',
    unknown: 'bg-slate-500',
  };
  return <span className={`h-2 w-2 rounded-full ${colors[status]}`} />;
}

// Display names for monitor types. Types missing here (new features) still
// render via the fallback — they show up in the filter automatically.
// Type badge: one muted colour — status colours are reserved for state.
function TypeBadge({ type }: { type: string }) {
  return (
    <span className="text-[11px] font-medium text-slate-500">
      {monitorTypeLabel(type, 'short')}
    </span>
  );
}

// Mini uptime bars
function MiniUptimeBars({ results }: { results: CheckResult[] }) {
  const bars = results.slice(0, 20).reverse();
  return (
    <div className="flex items-center gap-px">
      {bars.map((result, i) => (
        <div
          key={i}
          className={`h-3 w-0.5 rounded-sm ${
            isPlatformResult(result)
              ? 'bg-slate-500'
              : result.status === 'success'
                ? 'bg-emerald-500'
                : 'bg-rose-500'
          }`}
        />
      ))}
      {[...Array(Math.max(0, 20 - bars.length))].map((_, i) => (
        <div key={`e-${i}`} className="h-3 w-0.5 rounded-sm bg-slate-500/25" />
      ))}
    </div>
  );
}

// Format bytes to human readable
function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B';
  const k = 1024;
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return `${(bytes / Math.pow(k, i)).toFixed(1)} ${sizes[i]}`;
}

// Agent metrics mini display
function AgentMetricsMini({ metrics }: { metrics: AgentMetrics }) {
  return (
    <div className="flex items-center gap-3 text-[11px] text-slate-500">
      <span>
        CPU <span className="font-medium tabular-nums text-slate-300">{metrics.cpu_percent.toFixed(0)}%</span>
      </span>
      <span>
        Mem{' '}
        <span className="font-medium tabular-nums text-slate-300">
          {((metrics.memory_used / metrics.memory_total) * 100).toFixed(0)}%
        </span>
      </span>
      <span>
        Disk{' '}
        <span className="font-medium tabular-nums text-slate-300">
          {((metrics.disk_used / metrics.disk_total) * 100).toFixed(0)}%
        </span>
      </span>
    </div>
  );
}

function isAgentMetrics(metrics: unknown): metrics is AgentMetrics {
  if (!metrics || typeof metrics !== 'object') return false;
  const candidate = metrics as AgentMetrics;
  return (
    typeof candidate.cpu_percent === 'number' &&
    typeof candidate.memory_used === 'number' &&
    typeof candidate.memory_total === 'number' &&
    typeof candidate.disk_used === 'number' &&
    typeof candidate.disk_total === 'number' &&
    typeof candidate.load_avg_1 === 'number'
  );
}

// Values the worker read from the latest JSON response (HTTP display_fields).
// Neutral and squared-off on purpose, so they never read as tags.
function DisplayValueChips({ values }: { values: HTTPDisplayValue[] }) {
  if (values.length === 0) return null;
  return (
    <div className="flex items-center gap-1 flex-wrap">
      {values.map((v) => (
        <span
          key={v.path}
          className="inline-flex max-w-[180px] items-center gap-1 rounded-md bg-well-hover px-1.5 py-0.5 font-mono text-[11px] leading-none text-slate-300"
          title={`${v.path} = ${v.value}`}
        >
          {displayValueLabel(v) && <span className="shrink-0 text-slate-500">{displayValueLabel(v)}</span>}
          <span className="truncate">{v.value}</span>
        </span>
      ))}
    </div>
  );
}

function SelectCheckbox({
  checked,
  onChange,
  visible = true,
  label,
}: {
  checked: boolean;
  onChange: () => void;
  visible?: boolean;
  label?: string;
}) {
  if (!visible) return null;
  return <SharedSelectCheckbox checked={checked} onChange={onChange} label={label} />;
}

function SnoozeOptions({ onSnooze }: { onSnooze: (minutes: number) => void }) {
  const close = useCloseRowMenu();
  return (
    <>
      <div className="px-2.5 pt-1 text-xs text-slate-500">Snooze alerts</div>
      <div className="flex gap-1 px-1.5 pb-1 pt-1.5">
        {[
          { label: '1h', minutes: 60 },
          { label: '4h', minutes: 240 },
          { label: '24h', minutes: 1440 },
        ].map(({ label, minutes }) => (
          <Button
            key={label}
            type="button"
            variant="ghost"
            size="xs"
            className="flex-1 !px-0"
            onClick={() => {
              close();
              onSnooze(minutes);
            }}
          >
            {label}
          </Button>
        ))}
      </div>
    </>
  );
}

function MonitorActionsMenu({
  monitor,
  onToggleEnabled,
  monitorId,
  onDelete,
  onSnooze,
}: {
  monitor: Monitor;
  onToggleEnabled: (monitor: Monitor) => void;
  monitorId: string;
  onDelete: () => void;
  onSnooze: (monitor: Monitor, durationMinutes: number) => void;
}) {
  return (
    <RowMenu label="Monitor actions">
      <RowMenuItem href={`/monitors/${monitorId}`} icon={<Pencil strokeWidth={1.75} />}>Edit</RowMenuItem>
      <RowMenuItem href={`/monitors/new?clone=${monitorId}`} icon={<Copy strokeWidth={1.75} />}>Clone</RowMenuItem>
      <RowMenuItem
        onSelect={() => onToggleEnabled(monitor)}
        icon={monitor.enabled ? <Pause strokeWidth={1.75} /> : <Play strokeWidth={1.75} />}
      >
        {monitor.enabled ? 'Pause' : 'Start'}
      </RowMenuItem>
      <RowMenuSeparator />
      <SnoozeOptions onSnooze={(minutes) => onSnooze(monitor, minutes)} />
      <RowMenuSeparator />
      <RowMenuItem onSelect={onDelete} icon={<Trash2 strokeWidth={1.75} />} danger>Delete</RowMenuItem>
    </RowMenu>
  );
}

// Row background: the previewed row carries an accent edge, checked rows a
// light accent tint.
function rowStateClass(isSelected: boolean, isChecked: boolean) {
  if (isSelected) return 'bg-well-hover shadow-[inset_2px_0_0_0_var(--accent)]';
  if (isChecked) return 'bg-cyan-500/[0.06]';
  return 'hover:bg-well';
}

// Compact monitor row component
function MonitorRow({
  monitor,
  results,
  isSelected,
  onClick,
  onDelete,
  onToggleEnabled,
  onSnooze,
  isChecked,
  onToggleSelect,
  selectionMode,
  isChild = false,
}: {
  monitor: Monitor;
  results: CheckResult[];
  isSelected: boolean;
  onClick: () => void;
  onDelete: () => void;
  onToggleEnabled: (monitor: Monitor) => void;
  onSnooze: (monitor: Monitor, durationMinutes: number) => void;
  isChecked: boolean;
  onToggleSelect: () => void;
  selectionMode: boolean;
  isChild?: boolean;
}) {
  const status = getEffectiveMonitorStatus(monitor, results);
  const uptime = calculateUptime(results);
  const operationalCount = countOperationalResults(results);
  const latencyStats = calculateLatencyStats(results);
  const latestResult = results[0];
  const agentMetrics = isAgentMetrics(latestResult?.metrics_data) ? latestResult.metrics_data : null;
  const displayValues = getHTTPDisplayValues(results);

  const getUrl = () => {
    if (monitor.config && 'url' in monitor.config) return monitor.config.url;
    if (monitor.config && 'base_url' in monitor.config) return monitor.config.base_url || null;
    if (monitor.config && 'start_url' in monitor.config) return monitor.config.start_url;
    if (monitor.type === 'grpc' && monitor.config && 'host' in monitor.config) {
      const cfg = monitor.config as { host?: string; port?: number; use_tls?: boolean };
      if (cfg.host) {
        const port = cfg.port || (cfg.use_tls === false ? 80 : 443);
        return `${cfg.host}:${port}`;
      }
    }
    if (monitor.type === 'tcp' && monitor.config && 'host' in monitor.config) {
      const cfg = monitor.config as { host?: string; port?: number };
      if (cfg.host) return cfg.port ? `${cfg.host}:${cfg.port}` : cfg.host;
    }
    if (monitor.url) return monitor.url;
    if (monitor.config && 'host' in monitor.config) return monitor.config.host;
    return null;
  };

  return (
    <div
      onClick={onClick}
      className={`group flex cursor-pointer items-center gap-3 px-4 transition-colors ${
        isChild ? 'py-2.5' : 'py-3'
      } ${rowStateClass(isSelected, isChecked)}`}
    >
      <SelectCheckbox
        checked={isChecked}
        onChange={onToggleSelect}
        visible={selectionMode}
        label={`Select ${monitor.name}`}
      />
      {/* Status */}
      <StatusDot status={status} />

      {/* Name & Type */}
      <div className="flex-1 min-w-0">
        <div className="flex items-center gap-2 flex-wrap">
          <span className="min-w-0 truncate text-sm font-medium text-white">{monitor.name}</span>
          <TypeBadge type={monitor.type} />
          {status === 'maintenance' && (
            <span
              className="inline-flex"
              title={
                monitor.maintenance_until
                  ? `Alerts suppressed until ${new Date(monitor.maintenance_until).toLocaleString()}`
                  : 'Alerts suppressed during maintenance'
              }
            >
              <Pill tone="info" size="xs">Maintenance</Pill>
            </span>
          )}
          <DisplayValueChips values={displayValues} />
          {/* Tags */}
          {monitor.tags && monitor.tags.length > 0 && (
            <div className="flex items-center gap-1 flex-wrap">
              {monitor.tags.slice(0, 3).map((tag) => (
                <TagPill key={tag} tag={tag} size="xs" />
              ))}
              {monitor.tags.length > 3 && (
                <span className="text-[11px] text-slate-500">+{monitor.tags.length - 3}</span>
              )}
            </div>
          )}
        </div>
        {getUrl() && (
          <div className="mt-0.5 flex">
            <CopyableTarget
              value={getUrl() as string}
              label={monitorTargetLabel(monitor.type)}
              size="sm"
              revealOnHover
              textClassName="text-slate-500"
            />
          </div>
        )}
        {/* Agent Metrics */}
        {monitor.type === 'agent' && agentMetrics && (
          <div className="mt-1">
            <AgentMetricsMini metrics={agentMetrics} />
          </div>
        )}
      </div>

      {/* Mini Uptime Bars */}
      <div className="hidden sm:block">
        <MiniUptimeBars results={results} />
      </div>

      {/* Stats */}
      <div className="hidden items-center gap-4 text-xs tabular-nums md:flex">
        <div className="w-14 text-right">
          {operationalCount > 0 ? (
            <span className={`font-medium ${uptime >= 99 ? 'text-emerald-300' : uptime >= 95 ? 'text-amber-300' : 'text-rose-300'}`}>
              {uptime.toFixed(1)}%
            </span>
          ) : (
            <span className="text-slate-500">—</span>
          )}
        </div>
        <div className="w-16 text-right text-slate-400">
          {latencyStats.median > 0 ? `${latencyStats.median}ms` : '—'}
        </div>
        <div className="w-16 text-right text-slate-500">
          {latestResult ? formatTimeAgo(latestResult.created_at) : 'Never'}
        </div>
      </div>

      {/* Actions */}
      <div className="flex items-center gap-1 opacity-100 transition-opacity focus-within:opacity-100 md:opacity-0 md:group-hover:opacity-100 md:focus-within:opacity-100">
        <MonitorActionsMenu
          monitor={monitor}
          monitorId={monitor.id}
          onToggleEnabled={onToggleEnabled}
          onSnooze={onSnooze}
          onDelete={onDelete}
        />
      </div>
    </div>
  );
}

// Group card with expandable children
function GroupCard({
  monitor,
  results,
  members,
  groupMembersMap,
  memberResults,
  isSelected,
  onClick,
  onDelete,
  onToggleEnabled,
  onSnooze,
  isChecked,
  onToggleSelect,
  onToggleMonitorSelection,
  onDeleteMonitor,
  selectionMode,
  onSelectMonitor,
  selectedMonitorId,
  selectedMonitorIds,
  visitedGroupIds = new Set<string>(),
  autoExpand = false,
  memberMatcher,
  groupHasMatch,
}: {
  monitor: Monitor;
  results: CheckResult[];
  members: Monitor[];
  groupMembersMap: Record<string, Monitor[]>;
  memberResults: Record<string, CheckResult[]>;
  isSelected: boolean;
  onClick: () => void;
  onDelete: () => void;
  onToggleEnabled: (monitor: Monitor) => void;
  onSnooze: (monitor: Monitor, durationMinutes: number) => void;
  isChecked: boolean;
  onToggleSelect: () => void;
  onToggleMonitorSelection: (id: string) => void;
  onDeleteMonitor: (id: string) => void;
  selectionMode: boolean;
  onSelectMonitor: (id: string) => void;
  selectedMonitorId: string | null;
  selectedMonitorIds: Set<string>;
  visitedGroupIds?: Set<string>;
  autoExpand?: boolean;
  memberMatcher?: (monitor: Monitor) => boolean;
  groupHasMatch?: (groupId: string) => boolean;
}) {
  const [expanded, setExpanded] = useState(false); // Collapsed by default

  // Expand automatically while a filter matches members inside this group,
  // and collapse again when that filter is cleared (unless the user expanded it).
  const wasAutoExpanded = useRef(false);
  useEffect(() => {
    if (autoExpand) {
      setExpanded((prev) => {
        if (!prev) wasAutoExpanded.current = true;
        return true;
      });
    } else if (wasAutoExpanded.current) {
      wasAutoExpanded.current = false;
      setExpanded(false);
    }
  }, [autoExpand]);
  const status = getEffectiveMonitorStatus(monitor, results);
  const uptime = calculateUptime(results);
  const operationalCount = countOperationalResults(results);
  const nextVisitedGroupIDs = new Set(visitedGroupIds);
  nextVisitedGroupIDs.add(monitor.id);

  // Calculate aggregate stats from members
  const memberStats = members.map(m => ({
    status: getEffectiveMonitorStatus(m, memberResults[m.id] || []),
    uptime: calculateUptime(memberResults[m.id] || [])
  }));
  const healthyCount = memberStats.filter(s => s.status === 'up').length;
  const downCount = memberStats.filter(s => s.status === 'down' || s.status === 'degraded').length;
  const healthTone = downCount > 0
    ? 'text-rose-300'
    : healthyCount === members.length && members.length > 0
      ? 'text-emerald-300'
      : 'text-slate-500';

  // While filtering, only show the members that match (fall back to all if
  // the group itself matched but none of its members did).
  const matchingMembers = memberMatcher
    ? members.filter(
        (member) =>
          memberMatcher(member) ||
          (member.type === 'group' && groupHasMatch?.(member.id))
      )
    : members;
  const visibleMembers = matchingMembers.length > 0 ? matchingMembers : members;
  const hiddenMemberCount = members.length - visibleMembers.length;

  return (
    <div>
      {/* Group Header */}
      <div
        onClick={onClick}
        className={`group flex cursor-pointer items-center gap-3 px-4 py-3 transition-colors ${rowStateClass(isSelected, isChecked)}`}
      >
        <SelectCheckbox
          checked={isChecked}
          onChange={onToggleSelect}
          visible={selectionMode}
          label={`Select ${monitor.name}`}
        />
        {/* Expand Button */}
        <button
          onClick={(e) => { e.stopPropagation(); setExpanded(!expanded); }}
          className="-mx-1 flex h-6 w-6 flex-shrink-0 items-center justify-center rounded-md text-slate-400 transition-colors hover:bg-well-hover hover:text-white"
        >
          <ChevronRight
            className={`h-4 w-4 transition-transform ${expanded ? 'rotate-90' : ''}`}
            strokeWidth={1.75}
          />
        </button>

        <StatusDot status={status} />

        {/* Name */}
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2 flex-wrap">
            <span className="min-w-0 truncate text-sm font-medium text-white">{monitor.name}</span>
            <TypeBadge type="group" />
            {/* Tags */}
            {monitor.tags && monitor.tags.length > 0 && (
              <div className="flex items-center gap-1">
                {monitor.tags.slice(0, 2).map((tag) => (
                  <TagPill key={tag} tag={tag} size="xs" />
                ))}
                {monitor.tags.length > 2 && (
                  <span className="text-[11px] text-slate-500">+{monitor.tags.length - 2}</span>
                )}
              </div>
            )}
          </div>
          <p className="mt-0.5 text-xs text-slate-500">
            <span className={healthTone}>{healthyCount}/{members.length} healthy</span>
            {' · '}{operationalCount > 0 ? `${uptime.toFixed(1)}%` : 'N/A'} uptime
          </p>
        </div>

        {/* Member Status Dots */}
        <div className="hidden sm:flex items-center gap-1">
          {members.slice(0, 8).map((m) => {
            const memberStatus = getEffectiveMonitorStatus(m, memberResults[m.id] || []);
            const dotColor =
              memberStatus === 'up'
                ? 'bg-emerald-500'
                : memberStatus === 'paused' || memberStatus === 'unknown'
                  ? 'bg-slate-500'
                  : memberStatus === 'maintenance'
                    ? 'bg-sky-500'
                    : 'bg-rose-500';
            return <div key={m.id} className={`h-2 w-2 rounded-full ${dotColor}`} title={m.name} />;
          })}
          {members.length > 8 && (
            <span className="text-[11px] text-slate-500">+{members.length - 8}</span>
          )}
        </div>
        {/* Keeps the member dots over the check-bars column, clear of the stat columns. */}
        <div className="hidden w-[216px] flex-shrink-0 md:block" />

        {/* Actions */}
        <div className="flex items-center gap-1 opacity-100 transition-opacity focus-within:opacity-100 md:opacity-0 md:group-hover:opacity-100 md:focus-within:opacity-100">
          <MonitorActionsMenu
            monitor={monitor}
            monitorId={monitor.id}
            onToggleEnabled={onToggleEnabled}
            onSnooze={onSnooze}
            onDelete={onDelete}
          />
        </div>
      </div>

      {/* Expanded Children */}
      {expanded && visibleMembers.length > 0 && (
        <div className="dashboard-scroll max-h-[60vh] overflow-y-auto overscroll-contain border-t border-divider bg-well pl-7 ![scrollbar-gutter:auto]">
          {hiddenMemberCount > 0 && (
            <p className="px-4 pb-1 pt-2.5 text-xs text-slate-500">
              Showing {visibleMembers.length} matching of {members.length} members
            </p>
          )}
          <div className="divide-y divide-divider">
          {visibleMembers.map((member) => {
            const memberKey = `${monitor.id}:${member.id}`;
            if (member.type === 'group') {
              const cycleDetected = nextVisitedGroupIDs.has(member.id);
              return (
                <div key={memberKey}>
                  {cycleDetected ? (
                    <>
                      <MonitorRow
                        monitor={member}
                        results={memberResults[member.id] || []}
                        isSelected={member.id === selectedMonitorId}
                        onClick={() => onSelectMonitor(member.id)}
                        onDelete={() => onDeleteMonitor(member.id)}
                        onToggleEnabled={onToggleEnabled}
                        onSnooze={onSnooze}
                        isChecked={selectedMonitorIds.has(member.id)}
                        onToggleSelect={() => onToggleMonitorSelection(member.id)}
                        selectionMode={selectionMode}
                        isChild
                      />
                      <p className="px-4 pb-2.5 text-xs text-rose-300">
                        Cycle detected in group nesting. Expansion stopped.
                      </p>
                    </>
                  ) : (
                    <GroupCard
                      monitor={member}
                      results={memberResults[member.id] || []}
                      members={groupMembersMap[member.id] || []}
                      groupMembersMap={groupMembersMap}
                      memberResults={memberResults}
                      isSelected={member.id === selectedMonitorId}
                      onClick={() => onSelectMonitor(member.id)}
                      onDelete={() => onDeleteMonitor(member.id)}
                      onToggleEnabled={onToggleEnabled}
                      onSnooze={onSnooze}
                      isChecked={selectedMonitorIds.has(member.id)}
                      onToggleSelect={() => onToggleMonitorSelection(member.id)}
                      onToggleMonitorSelection={onToggleMonitorSelection}
                      onDeleteMonitor={onDeleteMonitor}
                      selectionMode={selectionMode}
                      onSelectMonitor={onSelectMonitor}
                      selectedMonitorId={selectedMonitorId}
                      selectedMonitorIds={selectedMonitorIds}
                      visitedGroupIds={nextVisitedGroupIDs}
                      autoExpand={Boolean(memberMatcher) && groupHasMatch?.(member.id)}
                      memberMatcher={memberMatcher}
                      groupHasMatch={groupHasMatch}
                    />
                  )}
                </div>
              );
            }

            return (
              <MonitorRow
                key={memberKey}
                monitor={member}
                results={memberResults[member.id] || []}
                isSelected={member.id === selectedMonitorId}
                onClick={() => onSelectMonitor(member.id)}
                onDelete={() => onDeleteMonitor(member.id)}
                onToggleEnabled={onToggleEnabled}
                onSnooze={onSnooze}
                isChecked={selectedMonitorIds.has(member.id)}
                onToggleSelect={() => onToggleMonitorSelection(member.id)}
                selectionMode={selectionMode}
                isChild
              />
            );
          })}
          </div>
        </div>
      )}
    </div>
  );
}

// Detail Panel Component
function DetailPanel({
  monitor,
  results,
  members = [],
  memberResults = {},
  onSelectMember,
  onClose,
}: {
  monitor: Monitor | null;
  results: CheckResult[];
  members?: Monitor[];
  memberResults?: Record<string, CheckResult[]>;
  onSelectMember?: (id: string) => void;
  onClose?: () => void;
}) {
  const [screenshotBlobURL, setScreenshotBlobURL] = useState<string | null>(null);
  const [screenshotLoading, setScreenshotLoading] = useState(false);

  const getSyntheticBrowserScreenshotPath = (result?: CheckResult): string | null => {
    const md = result?.metrics_data as any;
    return md?.synthetic_browser?.artifacts?.screenshot_path || null;
  };

  const latestSyntheticBrowserResult = monitor?.type === 'synthetic_browser'
    ? results.find((result) => Boolean(getSyntheticBrowserScreenshotPath(result)))
    : undefined;
  const latestSyntheticBrowserScreenshotPath = getSyntheticBrowserScreenshotPath(latestSyntheticBrowserResult);
  const syntheticBrowserScreenshotURL = monitor && latestSyntheticBrowserScreenshotPath
    ? getSyntheticBrowserScreenshotUrl(monitor.id, monitor.tenant_id, latestSyntheticBrowserScreenshotPath)
    : null;

  useEffect(() => {
    let cancelled = false;
    let objectURL: string | null = null;

    if (!syntheticBrowserScreenshotURL) {
      setScreenshotBlobURL((prev) => {
        if (prev) URL.revokeObjectURL(prev);
        return null;
      });
      setScreenshotLoading(false);
      return;
    }

    const loadScreenshot = async () => {
      setScreenshotLoading(true);
      try {
        const headers: HeadersInit = {};
        const apiKey = getApiKey();
        if (apiKey) {
          headers.Authorization = `Bearer ${apiKey}`;
        }

        const response = await fetch(syntheticBrowserScreenshotURL, {
          method: 'GET',
          headers,
          credentials: 'include',
        });
        if (!response.ok) {
          throw new Error(`screenshot request failed: ${response.status}`);
        }

        const blob = await response.blob();
        objectURL = URL.createObjectURL(blob);
        if (!cancelled) {
          setScreenshotBlobURL((prev) => {
            if (prev) URL.revokeObjectURL(prev);
            return objectURL;
          });
        }
      } catch (err) {
        console.error('Failed to load synthetic browser screenshot:', err);
        if (!cancelled) {
          setScreenshotBlobURL((prev) => {
            if (prev) URL.revokeObjectURL(prev);
            return null;
          });
        }
      } finally {
        if (!cancelled) {
          setScreenshotLoading(false);
        }
      }
    };

    loadScreenshot();

    return () => {
      cancelled = true;
      if (objectURL) {
        URL.revokeObjectURL(objectURL);
      }
    };
  }, [syntheticBrowserScreenshotURL]);

  if (!monitor) {
    return (
      <div className="glass flex h-64 items-center justify-center p-6">
        <p className="text-sm text-slate-500">Select a monitor</p>
      </div>
    );
  }

  const status = getEffectiveMonitorStatus(monitor, results);
  const uptime = calculateUptime(results);
  const operationalCount = countOperationalResults(results);
  const latencyStats = calculateLatencyStats(results);
  const latestResult = results[0];
  const agentMetrics = isAgentMetrics(latestResult?.metrics_data) ? latestResult.metrics_data : null;
  const displayValues = getHTTPDisplayValues(results);

  const memberStatuses = monitor.type === 'group'
    ? members.map((m) => getEffectiveMonitorStatus(m, memberResults[m.id] || []))
    : [];
  const memberUp = memberStatuses.filter((s) => s === 'up').length;
  const memberDown = memberStatuses.filter((s) => s === 'down' || s === 'degraded').length;
  const memberPaused = memberStatuses.filter((s) => s === 'paused').length;
  const memberMaintenance = memberStatuses.filter((s) => s === 'maintenance').length;
  const memberOther = memberStatuses.length - memberUp - memberDown - memberPaused - memberMaintenance;

  const getUrl = () => {
    if (monitor.config && 'url' in monitor.config) return monitor.config.url;
    if (monitor.config && 'base_url' in monitor.config) return monitor.config.base_url || null;
    if (monitor.config && 'start_url' in monitor.config) return monitor.config.start_url;
    if (monitor.url) return monitor.url;
    if (monitor.config && 'host' in monitor.config) return monitor.config.host;
    return null;
  };

  const sectionTitle = 'mb-2 text-xs font-medium text-slate-400';
  const meterTrack = 'mt-2 h-1 overflow-hidden rounded-full bg-well-hover';

  return (
    <div className="glass">
      {/* Header */}
      <div className="flex items-start justify-between gap-3 px-5 pt-5">
        <div className="min-w-0">
          <h3 className="text-[15px] font-semibold text-white">{monitor.name}</h3>
          {getUrl() ? (
            <div className="mt-0.5 flex max-w-full">
              <CopyableTarget
                value={getUrl() as string}
                label={monitorTargetLabel(monitor.type)}
                size="sm"
                textClassName="text-slate-500"
              />
            </div>
          ) : (
            <p className="mt-0.5 text-xs text-slate-500">{monitorTypeLabel(monitor.type)}</p>
          )}
        </div>
        <div className="flex flex-shrink-0 items-center gap-2">
          <StatusDot status={status} />
          {onClose && (
            <IconButton icon={<X strokeWidth={1.75} />} label="Close preview" onClick={onClose} />
          )}
        </div>
      </div>

      <div className="space-y-5 p-5">
        {/* Stats */}
        <div className="grid grid-cols-3 gap-2">
          <div className="glass-well px-3 py-2.5">
            <p className="text-xs text-slate-400">Uptime</p>
            <p className="mt-1 text-lg font-semibold tabular-nums text-white">{operationalCount > 0 ? `${uptime.toFixed(1)}%` : '—'}</p>
          </div>
          <div className="glass-well px-3 py-2.5">
            <p className="text-xs text-slate-400">Median</p>
            <p className="mt-1 text-lg font-semibold tabular-nums text-white">{latencyStats.median ? `${latencyStats.median}ms` : '—'}</p>
          </div>
          <div className="glass-well px-3 py-2.5">
            <p className="text-xs text-slate-400">P95</p>
            <p className="mt-1 text-lg font-semibold tabular-nums text-white">{latencyStats.p95 ? `${latencyStats.p95}ms` : '—'}</p>
          </div>
        </div>

        {/* Response values (HTTP display_fields), shown in full here */}
        {displayValues.length > 0 && (
          <div>
            <h4 className={sectionTitle}>Response values</h4>
            <dl className="space-y-1.5 text-xs">
              {displayValues.map((v) => (
                <div key={v.path} className="flex items-baseline gap-2">
                  <dt className="shrink-0 text-slate-500" title={v.path}>{displayValueLabel(v) || v.path}</dt>
                  <dd className="min-w-0 break-all font-mono text-slate-300">{v.value}</dd>
                </div>
              ))}
            </dl>
          </div>
        )}

        {/* Group Members */}
        {monitor.type === 'group' && (
          <div>
            <h4 className={sectionTitle}>Members ({members.length})</h4>
            {members.length > 0 && (
              <>
                <div className="mb-1.5 flex h-1.5 overflow-hidden rounded-full bg-well-hover">
                  {memberUp > 0 && (
                    <div className="bg-emerald-500" style={{ width: `${(memberUp / members.length) * 100}%` }} />
                  )}
                  {memberDown > 0 && (
                    <div className="bg-rose-500" style={{ width: `${(memberDown / members.length) * 100}%` }} />
                  )}
                  {memberPaused > 0 && (
                    <div className="bg-slate-500" style={{ width: `${(memberPaused / members.length) * 100}%` }} />
                  )}
                  {memberMaintenance > 0 && (
                    <div className="bg-sky-500" style={{ width: `${(memberMaintenance / members.length) * 100}%` }} />
                  )}
                  {memberOther > 0 && (
                    <div className="bg-slate-500/50" style={{ width: `${(memberOther / members.length) * 100}%` }} />
                  )}
                </div>
                <p className="mb-2 text-xs text-slate-500">
                  {[
                    `${memberUp} up`,
                    memberDown ? `${memberDown} down` : null,
                    memberPaused ? `${memberPaused} paused` : null,
                    memberMaintenance ? `${memberMaintenance} maintenance` : null,
                    memberOther ? `${memberOther} unknown` : null,
                  ]
                    .filter(Boolean)
                    .join(' · ')}
                </p>
              </>
            )}
            {members.length > 0 ? (
              <div className="dashboard-scroll -mx-2 max-h-56 space-y-0.5 overflow-y-auto overscroll-contain ![scrollbar-gutter:auto]">
                {members.map((member) => {
                  const memberStatus = getEffectiveMonitorStatus(member, memberResults[member.id] || []);
                  const memberOperational = countOperationalResults(memberResults[member.id] || []);
                  const memberUptime = calculateUptime(memberResults[member.id] || []);
                  return (
                    <button
                      key={member.id}
                      onClick={() => onSelectMember?.(member.id)}
                      className="flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left transition-colors hover:bg-well"
                    >
                      <StatusDot status={memberStatus} />
                      <span className="min-w-0 flex-1 truncate text-xs text-slate-300">{member.name}</span>
                      <span className="text-[11px] tabular-nums text-slate-500">
                        {memberOperational > 0 ? `${memberUptime.toFixed(1)}%` : '—'}
                      </span>
                    </button>
                  );
                })}
              </div>
            ) : (
              <p className="text-xs text-slate-500">No members yet</p>
            )}
          </div>
        )}

        {/* Agent Metrics Section */}
        {monitor.type === 'agent' && agentMetrics && (
          <div>
            <h4 className={sectionTitle}>System metrics</h4>
            <div className="grid grid-cols-2 gap-2">
              <div className="glass-well p-3">
                <div className="flex items-center justify-between">
                  <span className="text-xs text-slate-400">CPU</span>
                  <span className="text-xs font-medium tabular-nums text-white">{agentMetrics.cpu_percent.toFixed(1)}%</span>
                </div>
                <div className={meterTrack}>
                  <div className="h-full rounded-full bg-sky-500" style={{ width: `${agentMetrics.cpu_percent}%` }} />
                </div>
              </div>
              <div className="glass-well p-3">
                <div className="flex items-center justify-between">
                  <span className="text-xs text-slate-400">Memory</span>
                  <span className="text-xs font-medium tabular-nums text-white">
                    {((agentMetrics.memory_used / agentMetrics.memory_total) * 100).toFixed(1)}%
                  </span>
                </div>
                <div className={meterTrack}>
                  <div className="h-full rounded-full bg-sky-500" style={{ width: `${(agentMetrics.memory_used / agentMetrics.memory_total) * 100}%` }} />
                </div>
              </div>
              <div className="glass-well p-3">
                <div className="flex items-center justify-between">
                  <span className="text-xs text-slate-400">Disk</span>
                  <span className="text-xs font-medium tabular-nums text-white">
                    {((agentMetrics.disk_used / agentMetrics.disk_total) * 100).toFixed(1)}%
                  </span>
                </div>
                <div className={meterTrack}>
                  <div className="h-full rounded-full bg-sky-500" style={{ width: `${(agentMetrics.disk_used / agentMetrics.disk_total) * 100}%` }} />
                </div>
              </div>
              <div className="glass-well p-3">
                <div className="flex items-center justify-between">
                  <span className="text-xs text-slate-400">Load avg</span>
                  <span className="text-xs font-medium tabular-nums text-white">{agentMetrics.load_avg_1.toFixed(2)}</span>
                </div>
                <p className="mt-1.5 text-[11px] text-slate-500">{agentMetrics.process_count} processes</p>
              </div>
            </div>
            <div className="mt-3 grid grid-cols-2 gap-x-4 gap-y-2 text-xs">
              <div className="flex justify-between text-slate-500">
                <span>Network in</span>
                <span className="tabular-nums text-slate-300">{formatBytes(agentMetrics.network_bytes_in)}</span>
              </div>
              <div className="flex justify-between text-slate-500">
                <span>Network out</span>
                <span className="tabular-nums text-slate-300">{formatBytes(agentMetrics.network_bytes_out)}</span>
              </div>
            </div>
          </div>
        )}

        {/* Tags */}
        {monitor.tags && monitor.tags.length > 0 && (
          <div>
            <h4 className={sectionTitle}>Tags</h4>
            <div className="flex flex-wrap gap-1.5">
              {monitor.tags.map((tag) => (
                <TagPill key={tag} tag={tag} size="sm" />
              ))}
            </div>
          </div>
        )}

        {/* Config */}
        <div>
          <h4 className={sectionTitle}>Config</h4>
          <div className="space-y-1.5 text-xs">
            <div className="flex justify-between">
              <span className="text-slate-500">Interval</span>
              <span className="text-slate-300">{formatInterval(monitor.interval_seconds)}</span>
            </div>
            <div className="flex justify-between">
              <span className="text-slate-500">Timeout</span>
              <span className="tabular-nums text-slate-300">{monitor.timeout_seconds}s</span>
            </div>
            <div className="flex justify-between">
              <span className="text-slate-500">Status</span>
              <span className={monitor.enabled ? 'text-emerald-300' : 'text-slate-500'}>
                {monitor.enabled ? 'Active' : 'Paused'}
              </span>
            </div>
          </div>
        </div>

        {monitor.type === 'prometheus' && (
          <div>
            <h4 className={sectionTitle}>Prometheus query</h4>
            <PrometheusDetails monitor={monitor} result={results[0]} size="compact" />
          </div>
        )}

        {/* Synthetic Browser Screenshot */}
        {monitor.type === 'synthetic_browser' && (
          <div>
            <h4 className={sectionTitle}>Latest failure screenshot</h4>
            {screenshotLoading ? (
              <p className="text-xs text-slate-500">Loading screenshot...</p>
            ) : screenshotBlobURL ? (
              <div className="glass-well overflow-hidden p-2">
                {/* A blob: URL from an authenticated fetch, which next/image cannot load. */}
                {/* eslint-disable-next-line @next/next/no-img-element */}
                <img
                  src={screenshotBlobURL}
                  alt="Latest synthetic browser failure screenshot"
                  className="h-auto max-h-[220px] w-full rounded-lg object-contain"
                  loading="lazy"
                  decoding="async"
                />
                <div className="mt-2 text-[11px] text-slate-500">
                  {latestSyntheticBrowserResult ? formatTimeAgo(latestSyntheticBrowserResult.created_at) : 'Latest run'}
                </div>
              </div>
            ) : (
              <p className="text-xs text-slate-500">No screenshot available yet.</p>
            )}
          </div>
        )}

        {/* Recent Results */}
        <div>
          <h4 className={sectionTitle}>Recent</h4>
          {results.length > 0 ? (
            <div className="space-y-1">
              {results.slice(0, 5).map((r, i) => (
                <div key={i} className="flex items-center justify-between py-1 text-xs">
                  <div className="flex items-center gap-2">
                    <span
                      className={`h-2 w-2 rounded-full ${
                        isPlatformResult(r) ? 'bg-slate-500' : r.status === 'success' ? 'bg-emerald-500' : 'bg-rose-500'
                      }`}
                    />
                    <span className="text-slate-500">{formatTimeAgo(r.created_at)}</span>
                  </div>
                  <span className="tabular-nums text-slate-400">{r.latency_ms || 0}ms</span>
                </div>
              ))}
            </div>
          ) : (
            <p className="py-2 text-center text-xs text-slate-500">No results</p>
          )}
        </div>

        {/* View Details */}
        <Button variant="ghost" size="sm" className="w-full" asChild>
          <Link href={`/monitors/${monitor.id}`}>View details</Link>
        </Button>
      </div>
    </div>
  );
}

// Column labels, laid out on the same flex track as MonitorRow so they sit
// over its stat columns. Only shown where those columns are.
function ListHeader({ selectionMode }: { selectionMode: boolean }) {
  return (
    <div className="hidden items-center gap-3 border-b border-divider px-4 py-2.5 text-xs font-medium text-slate-500 md:flex">
      {selectionMode && <span className="w-4 flex-shrink-0" />}
      <span className="w-2 flex-shrink-0" />
      <span className="min-w-0 flex-1">Name</span>
      <span className="w-[59px]" />
      <div className="flex items-center gap-4">
        <span className="w-14 text-right">Uptime</span>
        <span className="w-16 text-right">Median</span>
        <span className="w-16 text-right">Last check</span>
      </div>
      <span className="w-8 flex-shrink-0" />
    </div>
  );
}

// Loading skeleton
function LoadingSkeleton() {
  return (
    <div className="glass divide-y divide-divider overflow-hidden">
      {[...Array(5)].map((_, i) => (
        <div key={i} className="flex items-center gap-3 px-4 py-4">
          <span className="h-2 w-2 rounded-full bg-well-hover" />
          <div className="h-3.5 w-1/3 animate-pulse rounded bg-well-hover" />
        </div>
      ))}
    </div>
  );
}

// Empty state
function EmptyState() {
  return (
    <SharedEmptyState
      icon={<Activity strokeWidth={1.5} />}
      title="No monitors yet"
      description="Create your first monitor to start tracking."
      action={
        <Button variant="ghost" size="sm" icon={<Plus strokeWidth={1.75} />} asChild>
          <Link href="/monitors/new">Add monitor</Link>
        </Button>
      }
    />
  );
}

export default function MonitorsPage() {
  const { canWrite } = useCurrentUser();
  const [monitors, setMonitors] = useState<Monitor[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>('');
  const [searchTerm, setSearchTerm] = useState('');
  const [selectedTypes, setSelectedTypes] = useState<Set<string>>(new Set());
  const [showTypeFilter, setShowTypeFilter] = useState(false);
  const [statusFilter, setStatusFilter] = useState<string>('all');
  // Deep-linked from the dashboard's unrouted warning (?routing=unrouted).
  const [unroutedOnly, setUnroutedOnly] = useState(false);

  useEffect(() => {
    if (typeof window === 'undefined') return;
    if (new URLSearchParams(window.location.search).get('routing') === 'unrouted') {
      setUnroutedOnly(true);
    }
  }, []);
  const [selectedTags, setSelectedTags] = useState<Set<string>>(new Set());
  const [selectedMonitorId, setSelectedMonitorId] = useState<string | null>(null);
  const [checkResultsMap, setCheckResultsMap] = useState<Record<string, CheckResult[]>>({});
  const [groupMembersMap, setGroupMembersMap] = useState<Record<string, Monitor[]>>({});
  const { showToast } = useToast();
  const [pendingDelete, setPendingDelete] = useState<Monitor | null>(null);
  const [confirmBulkDelete, setConfirmBulkDelete] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [showTagFilter, setShowTagFilter] = useState(false);
  const [selectionMode, setSelectionMode] = useState(false);
  const [selectedMonitorIds, setSelectedMonitorIds] = useState<Set<string>>(new Set());
  const [groupNameInput, setGroupNameInput] = useState('');
  const [targetGroupId, setTargetGroupId] = useState('');
  const [exporting, setExporting] = useState(false);
  const [showBulkAlerting, setShowBulkAlerting] = useState(false);
  const router = useRouter();

  // Below lg the detail panel is not rendered, so selecting a row would do
  // nothing visible — open the monitor's page instead.
  const selectMonitor = (id: string) => {
    if (selectionMode) {
      setSelectedMonitorId(id);
      return;
    }
    if (typeof window !== 'undefined' && !window.matchMedia('(min-width: 1024px)').matches) {
      router.push(`/monitors/${id}`);
      return;
    }
    // The preview panel is opt-in: clicking the previewed row again closes it.
    setSelectedMonitorId((current) => (current === id ? null : id));
  };

  useEffect(() => {
    loadMonitors();
  }, []);

  const loadMonitors = async () => {
    // #region agent log
    const loadStartTime = Date.now();
    debugIngest({
      location: 'page.tsx:loadMonitors:start',
      message: 'loadMonitors started (progressive)',
      data: {},
      timestamp: Date.now(),
      sessionId: 'debug-session',
      runId: 'progressive',
      hypothesisId: 'A',
    });
    // #endregion
    try {
      setLoading(true);
      setError('');
      // #region agent log
      const getMonitorsStart = Date.now();
      // #endregion
      const monitorsList = await getAllMonitors();
      // #region agent log
      debugIngest({
        location: 'page.tsx:loadMonitors:getMonitors',
        message: 'getMonitors API call completed',
        data: {
          durationMs: Date.now() - getMonitorsStart,
          monitorCount: monitorsList.length,
        },
        timestamp: Date.now(),
        sessionId: 'debug-session',
        runId: 'progressive',
        hypothesisId: 'A',
      });
      // #endregion
      setMonitors(monitorsList);

      // FIX: Progressive loading - show UI immediately, load results in background
      // Stop showing loading spinner now - monitors list is ready
      setLoading(false);

      // Fetch check results and group members progressively in chunks
      const resultsMap: Record<string, CheckResult[]> = {};
      const membersMap: Record<string, Monitor[]> = {};

      // #region agent log
      const resultsStartTime = Date.now();
      let apiCallCount = 0;
      // #endregion

      // FIX: Process in chunks of 6 (browser connection limit) to avoid queuing
      const CHUNK_SIZE = 6;
      for (let i = 0; i < monitorsList.length; i += CHUNK_SIZE) {
        const chunk = monitorsList.slice(i, i + CHUNK_SIZE);
        await Promise.all(
          chunk.map(async (monitor) => {
            try {
              // #region agent log
              apiCallCount++;
              const resultStart = Date.now();
              // #endregion
              const results = await getMonitorResults(monitor.id, { limit: 20 });
              // #region agent log
              debugIngest({
                location: 'page.tsx:loadMonitors:getMonitorResults',
                message: 'getMonitorResults for single monitor',
                data: {
                  monitorId: monitor.id,
                  durationMs: Date.now() - resultStart,
                  resultsCount: results?.results?.length || 0,
                },
                timestamp: Date.now(),
                sessionId: 'debug-session',
                runId: 'progressive',
                hypothesisId: 'A,B',
              });
              // #endregion
              resultsMap[monitor.id] = results.results || [];

              if (monitor.type === 'group') {
                try {
                  // #region agent log
                  apiCallCount++;
                  const membersStart = Date.now();
                  // #endregion
                  const members = sortMonitorsByName(await getGroupMembers(monitor.id));
                  // #region agent log
                  debugIngest({
                    location: 'page.tsx:loadMonitors:getGroupMembers',
                    message: 'getGroupMembers call',
                    data: {
                      groupId: monitor.id,
                      durationMs: Date.now() - membersStart,
                      memberCount: members?.length || 0,
                    },
                    timestamp: Date.now(),
                    sessionId: 'debug-session',
                    runId: 'progressive',
                    hypothesisId: 'C',
                  });
                  // #endregion
                  membersMap[monitor.id] = members;
                  members.forEach((m) => {
                    if (!resultsMap[m.id]) {
                      resultsMap[m.id] = [];
                    }
                  });
                } catch { membersMap[monitor.id] = []; }
              }
            } catch {
              resultsMap[monitor.id] = [];
            }
          })
        );
        // Update state after each chunk so UI updates progressively
        setCheckResultsMap({ ...resultsMap });
        setGroupMembersMap({ ...membersMap });
      }

      // #region agent log
      debugIngest({
        location: 'page.tsx:loadMonitors:allResultsFetched',
        message: 'All results and members fetched (progressive)',
        data: {
          totalApiCalls: apiCallCount,
          totalResultsFetchDurationMs: Date.now() - resultsStartTime,
        },
        timestamp: Date.now(),
        sessionId: 'debug-session',
        runId: 'progressive',
        hypothesisId: 'A,D',
      });
      // #endregion
    } catch (err: any) {
      setError(err.message || 'Failed to load monitors');
      setMonitors([]);
      setLoading(false);
    }
    // #region agent log
    debugIngest({
      location: 'page.tsx:loadMonitors:end',
      message: 'loadMonitors completed (progressive)',
      data: { totalDurationMs: Date.now() - loadStartTime },
      timestamp: Date.now(),
      sessionId: 'debug-session',
      runId: 'progressive',
      hypothesisId: 'A',
    });
    // #endregion
  };

  const handleExport = async () => {
    try {
      setExporting(true);
      const { blob, filename } = await exportMonitors();
      const url = window.URL.createObjectURL(blob);
      const link = document.createElement('a');
      link.href = url;
      link.download = filename;
      document.body.appendChild(link);
      link.click();
      link.remove();
      window.URL.revokeObjectURL(url);
      showToast('Monitor export downloaded', 'success');
    } catch (err: any) {
      showToast(err.message || 'Failed to export monitors', 'error');
    } finally {
      setExporting(false);
    }
  };

  const requestDeleteById = (id: string) => {
    const all = [...monitors, ...Object.values(groupMembersMap).flat()];
    const target = all.find((m) => m.id === id);
    if (target) setPendingDelete(target);
  };

  const handleDelete = async () => {
    if (!pendingDelete) return;
    const id = pendingDelete.id;
    setDeleting(true);
    try {
      await deleteMonitor(id);
      setMonitors(monitors.filter((m) => m.id !== id));
      if (selectedMonitorId === id) {
        setSelectedMonitorId(null);
      }
      showToast('Monitor deleted', 'success');
      setPendingDelete(null);
    } catch (err: any) {
      showToast(err.message || 'Failed to delete', 'error');
    } finally {
      setDeleting(false);
    }
  };

  const handleToggleEnabled = async (monitor: Monitor) => {
    try {
      const updated = await toggleMonitorEnabled(monitor.id, !monitor.enabled);
      setMonitors((prev) => prev.map((item) => (item.id === updated.id ? updated : item)));
      setGroupMembersMap((prev) => {
        const nextEntries = Object.entries(prev).map(([groupId, members]) => [
          groupId,
          members.map((member) => (member.id === updated.id ? updated : member)),
        ] as const);
        return Object.fromEntries(nextEntries);
      });
      showToast(updated.enabled ? 'Monitor resumed' : 'Monitor paused', 'success');
    } catch (err: any) {
      showToast(err.message || 'Failed to update monitor', 'error');
    }
  };

  const handleSnooze = async (monitor: Monitor, durationMinutes: number) => {
    try {
      const window = await snoozeMonitor(monitor.id, { duration_minutes: durationMinutes });
      const updated: Monitor = { ...monitor, in_maintenance: true, maintenance_until: window.ends_at };
      setMonitors((prev) => prev.map((item) => (item.id === updated.id ? updated : item)));
      setGroupMembersMap((prev) => {
        const nextEntries = Object.entries(prev).map(([groupId, members]) => [
          groupId,
          members.map((member) => (member.id === updated.id ? updated : member)),
        ] as const);
        return Object.fromEntries(nextEntries);
      });
      showToast(`Alerts snoozed until ${new Date(window.ends_at).toLocaleTimeString()}`, 'success');
    } catch (err: any) {
      showToast(err.message || 'Failed to snooze monitor', 'error');
    }
  };

  const toggleMonitorSelection = (id: string) => {
    setSelectedMonitorIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
      }
      return next;
    });
  };

  const clearSelection = () => {
    setSelectedMonitorIds(new Set());
    setSelectionMode(false);
    setGroupNameInput('');
    setTargetGroupId('');
  };

  // Collect all unique tags from monitors
  const allTags = useMemo(() => {
    const tagSet = new Set<string>();
    monitors.forEach((monitor) => {
      if (monitor.tags) {
        monitor.tags.forEach((tag) => tagSet.add(tag));
      }
    });
    return Array.from(tagSet).sort();
  }, [monitors]);

  // Types present in the workspace, with counts, most common first.
  // Derived from data so new monitor types appear here with no UI changes.
  const typeCounts = useMemo(() => {
    const counts = new Map<string, number>();
    monitors.forEach((monitor) => {
      counts.set(monitor.type, (counts.get(monitor.type) || 0) + 1);
    });
    return Array.from(counts.entries()).sort(
      (a, b) => b[1] - a[1] || a[0].localeCompare(b[0])
    );
  }, [monitors]);

  // Monitors whose alerts would notify nobody (server-computed per monitor in
  // shared/alertrouting). Drives the filter chip and, with it, the dashboard's
  // deep link.
  const unroutedCount = useMemo(
    () => monitors.filter((monitor) => monitor.enabled && monitor.alert_routing?.reachable === false).length,
    [monitors],
  );

  const toggleType = (type: string) => {
    const newSelected = new Set(selectedTypes);
    if (newSelected.has(type)) {
      newSelected.delete(type);
    } else {
      newSelected.add(type);
    }
    setSelectedTypes(newSelected);
  };

  // Toggle tag selection
  const toggleTag = (tag: string) => {
    const newSelected = new Set(selectedTags);
    if (newSelected.has(tag)) {
      newSelected.delete(tag);
    } else {
      newSelected.add(tag);
    }
    setSelectedTags(newSelected);
  };

  // Clear all tag filters
  const clearTagFilters = () => {
    setSelectedTags(new Set());
  };

  // Collect all monitor IDs that are members of a group
  const groupMemberIds = useMemo(() => {
    const memberIds = new Set<string>();
    Object.values(groupMembersMap).forEach((members) => {
      members.forEach((m) => memberIds.add(m.id));
    });
    return memberIds;
  }, [groupMembersMap]);

  const monitorMatchesFilters = (monitor: Monitor) => {
    const matchesSearch = monitor.name.toLowerCase().includes(searchTerm.toLowerCase());
    const matchesType = selectedTypes.size === 0 || selectedTypes.has(monitor.type);
    const status = getEffectiveMonitorStatus(monitor, checkResultsMap[monitor.id] || []);
    const matchesStatus = statusFilter === 'all' ||
      (statusFilter === 'up' && status === 'up') ||
      (statusFilter === 'down' && (status === 'down' || status === 'degraded')) ||
      (statusFilter === 'paused' && status === 'paused') ||
      (statusFilter === 'maintenance' && status === 'maintenance');
    const matchesTags = selectedTags.size === 0 ||
      Boolean(monitor.tags && monitor.tags.some((tag) => selectedTags.has(tag)));
    const matchesRouting =
      !unroutedOnly || (monitor.enabled && monitor.alert_routing?.reachable === false);
    return matchesSearch && matchesType && matchesStatus && matchesTags && matchesRouting;
  };

  // A group also matches when any of its (nested) members match, so searching
  // for a member surfaces the group it lives in.
  const groupHasMatchingMember = (groupId: string, visited: Set<string> = new Set()): boolean => {
    if (visited.has(groupId)) return false;
    visited.add(groupId);
    return (groupMembersMap[groupId] || []).some(
      (member) =>
        monitorMatchesFilters(member) ||
        (member.type === 'group' && groupHasMatchingMember(member.id, visited))
    );
  };

  const hasActiveFilters =
    searchTerm.trim() !== '' || selectedTypes.size > 0 || statusFilter !== 'all' || selectedTags.size > 0 ||
    unroutedOnly;

  // Filter monitors (exclude monitors that are members of a group - they only show under their group),
  // then show groups ahead of ungrouped monitors.
  const filteredMonitors = monitors
    .filter((monitor) => {
      if (groupMemberIds.has(monitor.id)) return false;
      if (monitorMatchesFilters(monitor)) return true;
      return monitor.type === 'group' && groupHasMatchingMember(monitor.id);
    })
    .sort((a, b) => Number(b.type === 'group') - Number(a.type === 'group'));

  const selectedMonitor = monitors.find((m) => m.id === selectedMonitorId) || null;
  const selectedMonitors = monitors.filter((m) => selectedMonitorIds.has(m.id));
  const availableGroups = monitors
    .filter((m) => m.type === 'group' && !selectedMonitorIds.has(m.id))
    .sort((a, b) => a.name.localeCompare(b.name));
  const allFilteredSelected = filteredMonitors.length > 0 && filteredMonitors.every((m) => selectedMonitorIds.has(m.id));
  const canDeleteSelected = selectedMonitorIds.size > 0;
  const canCreateGroup = selectedMonitorIds.size > 0 && groupNameInput.trim().length > 0;
  const canMoveToGroup = targetGroupId.length > 0 && selectedMonitors.length > 0;

  useEffect(() => {
    if (!targetGroupId) return;
    if (targetGroupId === NO_GROUP_VALUE) return;
    if (!availableGroups.some((group) => group.id === targetGroupId)) {
      setTargetGroupId('');
    }
  }, [targetGroupId, availableGroups]);

  const toggleSelectAllFiltered = () => {
    if (allFilteredSelected) {
      setSelectedMonitorIds((prev) => {
        const next = new Set(prev);
        filteredMonitors.forEach((m) => next.delete(m.id));
        return next;
      });
      return;
    }
    setSelectedMonitorIds((prev) => {
      const next = new Set(prev);
      filteredMonitors.forEach((m) => next.add(m.id));
      return next;
    });
  };

  const handleBulkDelete = async () => {
    if (selectedMonitors.length === 0) return;
    setDeleting(true);
    try {
      await Promise.all(selectedMonitors.map((m) => deleteMonitor(m.id)));
      setMonitors(monitors.filter((m) => !selectedMonitorIds.has(m.id)));
      setSelectedMonitorIds(new Set());
      showToast('Selected monitors deleted', 'success');
      setConfirmBulkDelete(false);
    } catch (err: any) {
      showToast(err.message || 'Failed to delete selected monitors', 'error');
    } finally {
      setDeleting(false);
    }
  };

  const handleCreateGroup = async () => {
    const trimmedName = groupNameInput.trim();
    if (!trimmedName) {
      showToast('Group name is required', 'error');
      return;
    }
    if (selectedMonitors.length < 1) {
      showToast('Select at least one item to create a group', 'error');
      return;
    }

    try {
      await createMonitor({
        name: trimmedName,
        type: 'group',
        config: { monitor_ids: selectedMonitors.map((m) => m.id) },
        interval_seconds: 60,
        timeout_seconds: 30,
        enabled: true,
      });
      setGroupNameInput('');
      setSelectedMonitorIds(new Set());
      await loadMonitors();
      showToast('Group created', 'success');
    } catch (err: any) {
      showToast(err.message || 'Failed to create group', 'error');
    }
  };

  const handleMoveToGroup = async () => {
    if (!targetGroupId) {
      showToast('Select a destination group first', 'error');
      return;
    }
    if (selectedMonitors.length === 0) {
      showToast('Select at least one monitor or group to move', 'error');
      return;
    }

    const isNoGroupDestination = targetGroupId === NO_GROUP_VALUE;
    const targetGroup = isNoGroupDestination
      ? null
      : monitors.find((m) => m.id === targetGroupId && m.type === 'group');
    if (!isNoGroupDestination && !targetGroup) {
      showToast('Destination group not found', 'error');
      return;
    }

    const selectedIds = selectedMonitors.map((m) => m.id);
    const selectedGroupIDs = selectedMonitors
      .filter((monitor) => monitor.type === 'group')
      .map((monitor) => monitor.id);

    if (!isNoGroupDestination) {
      if (selectedGroupIDs.includes(targetGroupId)) {
        showToast('Cannot move a group into itself', 'error');
        return;
      }

      const wouldCreateCycle = selectedGroupIDs.some((groupID) =>
        groupContainsGroup(groupID, targetGroupId, groupMembersMap)
      );
      if (wouldCreateCycle) {
        showToast('Cannot move a group into one of its descendants', 'error');
        return;
      }
    }

    const removalsByGroup: Record<string, string[]> = {};

    for (const [groupId, members] of Object.entries(groupMembersMap)) {
      if (!isNoGroupDestination && groupId === targetGroupId) continue;
      const overlappingIds = members
        .filter((member) => selectedIds.includes(member.id))
        .map((member) => member.id);
      if (overlappingIds.length > 0) {
        removalsByGroup[groupId] = overlappingIds;
      }
    }

    const hasRemovals = Object.keys(removalsByGroup).length > 0;

    if (isNoGroupDestination && !hasRemovals) {
      showToast('Selected items are not in any group', 'error');
      return;
    }

    const targetMemberIds = new Set(
      isNoGroupDestination ? [] : (groupMembersMap[targetGroupId] || []).map((m) => m.id)
    );
    const idsToAdd = isNoGroupDestination
      ? []
      : selectedIds.filter((id) => !targetMemberIds.has(id));

    if (!isNoGroupDestination && !hasRemovals && idsToAdd.length === 0) {
      showToast('Selected items are already in the target group', 'error');
      return;
    }

    try {
      if (!isNoGroupDestination && idsToAdd.length > 0) {
        await addMonitorsToGroup(targetGroupId, idsToAdd);
      }

      if (hasRemovals) {
        await Promise.all(
          Object.entries(removalsByGroup).map(([groupId, monitorIds]) =>
            removeMonitorsFromGroup(groupId, monitorIds)
          )
        );
      }

      setSelectedMonitorIds(new Set());
      await loadMonitors();
      showToast(isNoGroupDestination
          ? `Removed ${selectedMonitors.length} item(s) from all groups`
          : `Moved ${selectedMonitors.length} item(s) to ${targetGroup?.name}`, 'success');
    } catch (err: any) {
      showToast(err.message || 'Failed to move monitors', 'error');
    }
  };

  // Stats
  const totalUp = monitors.filter((m) => getEffectiveMonitorStatus(m, checkResultsMap[m.id] || []) === 'up').length;
  const totalDown = monitors.filter((m) => {
    const status = getEffectiveMonitorStatus(m, checkResultsMap[m.id] || []);
    return status === 'down' || status === 'degraded';
  }).length;
  const totalPaused = monitors.filter((m) => getEffectiveMonitorStatus(m, checkResultsMap[m.id] || []) === 'paused').length;
  const totalMaintenance = monitors.filter(
    (m) => getEffectiveMonitorStatus(m, checkResultsMap[m.id] || []) === 'maintenance'
  ).length;

  return (
    <div className="space-y-6">
      {/* Header */}
      <PageHeader
        title="Monitors"
        subtitle={`${monitors.length} monitors · ${totalUp} up · ${totalDown} down · ${totalPaused} paused${totalMaintenance ? ` · ${totalMaintenance} maintenance` : ''}`}
        action={
          <div className="flex items-center gap-2">
            <Button
              variant="ghost"
              size="sm"
              icon={<Download strokeWidth={1.75} />}
              onClick={handleExport}
              disabled={exporting}
              loading={exporting}
            >
              {exporting ? 'Exporting…' : 'Export'}
            </Button>
            {canWrite && (
              <Button variant="ghost" size="sm" icon={<Upload strokeWidth={1.75} />} asChild>
                <Link href="/monitors/import">Import</Link>
              </Button>
            )}
            {canWrite && (
              <Button variant="accent" size="sm" icon={<Plus strokeWidth={1.75} />} asChild>
                <Link href="/monitors/new">Add monitor</Link>
              </Button>
            )}
          </div>
        }
      />

      {/* Filters */}
      <div className="space-y-3">
        <div className="flex flex-wrap items-center gap-3">
          <div className="relative flex-1 max-w-xs">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-slate-500" strokeWidth={1.75} />
            <input
              type="text"
              placeholder="Search…"
              value={searchTerm}
              onChange={(e) => setSearchTerm(e.target.value)}
              className="input input-sm h-7 py-0 !pl-8"
            />
          </div>

          {/* Type Filter Toggle */}
          <FilterChip
            selected={selectedTypes.size > 0}
            count={selectedTypes.size > 0 ? selectedTypes.size : undefined}
            icon={<Layers strokeWidth={1.75} />}
            onClick={() => setShowTypeFilter(!showTypeFilter)}
            aria-expanded={showTypeFilter}
          >
            <span className="inline-flex items-center gap-1">
              Type
              <ChevronDown
                className={`h-3 w-3 transition-transform ${showTypeFilter ? 'rotate-180' : ''}`}
                strokeWidth={1.75}
              />
            </span>
          </FilterChip>

          <div className="flex flex-wrap items-center gap-1.5">
            {['all', 'up', 'down', 'paused', 'maintenance'].map((status) => (
              <FilterChip
                key={status}
                selected={statusFilter === status}
                onClick={() => setStatusFilter(status)}
              >
                {status === 'all'
                  ? 'All'
                  : status === 'up'
                  ? 'Up'
                  : status === 'down'
                  ? 'Down'
                  : status === 'paused'
                  ? 'Paused'
                  : 'Maintenance'}
              </FilterChip>
            ))}
          </div>

          {(unroutedCount > 0 || unroutedOnly) && (
            <FilterChip
              selected={unroutedOnly}
              count={unroutedCount > 0 ? unroutedCount : undefined}
              icon={<BellOff strokeWidth={1.75} />}
              onClick={() => setUnroutedOnly((v) => !v)}
              title="Monitors whose alerts would notify nobody"
            >
              No alert route
            </FilterChip>
          )}

          {/* Tag Filter Toggle */}
          {allTags.length > 0 && (
            <FilterChip
              selected={selectedTags.size > 0}
              count={selectedTags.size > 0 ? selectedTags.size : undefined}
              icon={<Tag strokeWidth={1.75} />}
              onClick={() => setShowTagFilter(!showTagFilter)}
              aria-expanded={showTagFilter}
            >
              <span className="inline-flex items-center gap-1">
                Tags
                <ChevronDown
                  className={`h-3 w-3 transition-transform ${showTagFilter ? 'rotate-180' : ''}`}
                  strokeWidth={1.75}
                />
              </span>
            </FilterChip>
          )}

          {!selectionMode && filteredMonitors.length > 0 && (
            <FilterChip
              className="ml-auto"
              icon={<CheckSquare strokeWidth={1.75} />}
              onClick={() => setSelectionMode(true)}
            >
              Select
            </FilterChip>
          )}
        </div>

        {/* Type Filter Dropdown */}
        {showTypeFilter && typeCounts.length > 0 && (
          <div className="glass p-4">
            <div className="mb-3 flex h-7 items-center justify-between">
              <span className="text-[13px] font-medium text-slate-300">Filter by type</span>
              {selectedTypes.size > 0 && (
                <Button variant="subtle" size="xs" onClick={() => setSelectedTypes(new Set())}>
                  Clear all
                </Button>
              )}
            </div>
            <div className="flex flex-wrap gap-1.5">
              {typeCounts.map(([type, count]) => (
                <FilterChip
                  key={type}
                  selected={selectedTypes.has(type)}
                  count={count}
                  onClick={() => toggleType(type)}
                >
                  {monitorTypeLabel(type)}
                </FilterChip>
              ))}
            </div>
          </div>
        )}

        {/* Tag Filter Dropdown */}
        {showTagFilter && allTags.length > 0 && (
          <div className="glass p-4">
            <div className="mb-3 flex h-7 items-center justify-between">
              <span className="text-[13px] font-medium text-slate-300">Filter by tags</span>
              {selectedTags.size > 0 && (
                <Button variant="subtle" size="xs" onClick={clearTagFilters}>
                  Clear all
                </Button>
              )}
            </div>
            <div className="flex flex-wrap gap-1.5">
              {allTags.map((tag) => (
                <TagPill
                  key={tag}
                  tag={tag}
                  size="sm"
                  selected={selectedTags.has(tag)}
                  onClick={() => toggleTag(tag)}
                />
              ))}
            </div>
          </div>
        )}
      </div>

      {/* Error */}
      {error && (
        <Callout
          tone="danger"
          action={<Button variant="danger" size="xs" onClick={loadMonitors}>Retry</Button>}
        >
          {error}
        </Callout>
      )}

      {/* Content */}
      {loading ? (
        <LoadingSkeleton />
      ) : monitors.length === 0 ? (
        <EmptyState />
      ) : (
        <div className={`grid gap-6 ${selectedMonitor ? 'lg:grid-cols-[minmax(0,1fr)_320px]' : ''}`}>
          {/* Monitor List */}
          <div className="glass min-w-0 self-start overflow-hidden">
            {/* Bulk actions bar (selection mode is entered from the filter row) */}
            {selectionMode && filteredMonitors.length > 0 && (
              <div className="flex flex-wrap items-center gap-2 border-b border-divider px-4 py-3">
                {/* Selection mode toggle */}
                <Button
                  variant="ghost"
                  size="xs"
                  icon={<X strokeWidth={1.75} />}
                  onClick={() => {
                    if (selectionMode) {
                      clearSelection();
                    } else {
                      setSelectionMode(true);
                    }
                  }}
                >
                  {selectionMode ? 'Exit select' : 'Select'}
                </Button>

                {/* Selection actions - only show when in selection mode */}
                {selectionMode && (
                  <>
                    <Button variant="ghost" size="xs" onClick={toggleSelectAllFiltered}>
                      {allFilteredSelected ? 'Deselect all' : 'Select all'}
                    </Button>

                    {selectedMonitorIds.size > 0 && (
                      <>
                        <span className="px-1 text-xs font-medium tabular-nums text-slate-300">
                          {selectedMonitorIds.size} selected
                        </span>

                        <div className="h-4 w-px bg-divider" />

                        {/* Group creation */}
                        <div className="flex items-center gap-1.5">
                          <input
                            type="text"
                            placeholder="New group name..."
                            value={groupNameInput}
                            onChange={(e) => setGroupNameInput(e.target.value)}
                            className="input input-xs h-7 !w-32"
                          />
                          <Button
                            variant="ghost"
                            size="xs"
                            icon={<FolderPlus strokeWidth={1.75} />}
                            onClick={handleCreateGroup}
                            disabled={!canCreateGroup}
                          >
                            Group
                          </Button>
                        </div>

                        {/* Move to existing group */}
                        <div className="flex items-center gap-1.5">
                          <select
                            value={targetGroupId}
                            onChange={(e) => setTargetGroupId(e.target.value)}
                            className="input input-xs h-7 !w-36"
                          >
                            <option value="">Move to group...</option>
                            <option value={NO_GROUP_VALUE}>No group</option>
                            {availableGroups.map((group) => (
                              <option key={group.id} value={group.id}>
                                {group.name}
                              </option>
                            ))}
                          </select>
                          <Button
                            variant="ghost"
                            size="xs"
                            icon={<Move strokeWidth={1.75} />}
                            onClick={handleMoveToGroup}
                            disabled={!canMoveToGroup}
                          >
                            Move
                          </Button>
                        </div>

                        <div className="h-4 w-px bg-divider" />

                        <Button
                          variant="ghost"
                          size="xs"
                          icon={<Bell strokeWidth={1.75} />}
                          onClick={() => setShowBulkAlerting(true)}
                        >
                          Edit alerting…
                        </Button>

                        <Button
                          variant="danger"
                          size="xs"
                          icon={<Trash2 strokeWidth={1.75} />}
                          onClick={() => setConfirmBulkDelete(true)}
                        >
                          Delete
                        </Button>
                      </>
                    )}
                  </>
                )}
              </div>
            )}
            
            {filteredMonitors.length > 0 && <ListHeader selectionMode={selectionMode} />}

            {filteredMonitors.length === 0 ? (
              <p className="px-5 py-10 text-center text-sm text-slate-500">No monitors match filters</p>
            ) : (
              <div className="divide-y divide-divider">
                {filteredMonitors.map((monitor) => (
                  monitor.type === 'group' ? (
                    <GroupCard
                      key={monitor.id}
                      monitor={monitor}
                      results={checkResultsMap[monitor.id] || []}
                      members={groupMembersMap[monitor.id] || []}
                      groupMembersMap={groupMembersMap}
                      memberResults={checkResultsMap}
                      isSelected={monitor.id === selectedMonitorId}
                      isChecked={selectedMonitorIds.has(monitor.id)}
                      onToggleSelect={() => toggleMonitorSelection(monitor.id)}
                      onToggleMonitorSelection={toggleMonitorSelection}
                      onDeleteMonitor={requestDeleteById}
                      onToggleEnabled={handleToggleEnabled}
                      onSnooze={handleSnooze}
                      selectionMode={selectionMode}
                      onClick={() => setSelectedMonitorId(monitor.id)}
                      onDelete={() => requestDeleteById(monitor.id)}
                      onSelectMonitor={selectMonitor}
                      selectedMonitorId={selectedMonitorId}
                      selectedMonitorIds={selectedMonitorIds}
                      autoExpand={hasActiveFilters && groupHasMatchingMember(monitor.id)}
                      memberMatcher={hasActiveFilters ? monitorMatchesFilters : undefined}
                      groupHasMatch={(groupId) => groupHasMatchingMember(groupId)}
                    />
                  ) : (
                    <MonitorRow
                      key={monitor.id}
                      monitor={monitor}
                      results={checkResultsMap[monitor.id] || []}
                      isSelected={monitor.id === selectedMonitorId}
                      isChecked={selectedMonitorIds.has(monitor.id)}
                      onToggleSelect={() => toggleMonitorSelection(monitor.id)}
                      selectionMode={selectionMode}
                      onClick={() => selectMonitor(monitor.id)}
                      onDelete={() => requestDeleteById(monitor.id)}
                      onToggleEnabled={handleToggleEnabled}
                      onSnooze={handleSnooze}
                    />
                  )
                ))}
              </div>
            )}
          </div>

          {/* Detail Panel */}
          {selectedMonitor && <div className="hidden lg:block">
            <div className="dashboard-scroll sticky top-6 max-h-[calc(100vh-3rem)] overflow-y-auto overscroll-contain rounded-2xl ![scrollbar-gutter:auto]">
              <DetailPanel
                monitor={selectedMonitor}
                results={selectedMonitor ? checkResultsMap[selectedMonitor.id] || [] : []}
                members={selectedMonitor ? groupMembersMap[selectedMonitor.id] || [] : []}
                memberResults={checkResultsMap}
                onSelectMember={setSelectedMonitorId}
                onClose={() => setSelectedMonitorId(null)}
              />
            </div>
          </div>}
        </div>
      )}

      {showBulkAlerting && (
        <BulkAlertingModal
          monitorIds={Array.from(selectedMonitorIds)}
          onDone={() => {
            setShowBulkAlerting(false);
            setSelectedMonitorIds(new Set());
            setSelectionMode(false);
            showToast('Alerting settings updated', 'success');
            loadMonitors();
          }}
          onCancel={() => setShowBulkAlerting(false)}
        />
      )}

      <ConfirmDialog
        open={pendingDelete !== null}
        title={pendingDelete?.type === 'group' ? 'Delete group' : 'Delete monitor'}
        description={
          pendingDelete
            ? `“${pendingDelete.name}” will be removed along with its check history. This cannot be undone.`
            : undefined
        }
        confirmLabel="Delete"
        loading={deleting}
        onConfirm={handleDelete}
        onCancel={() => !deleting && setPendingDelete(null)}
      />

      <ConfirmDialog
        open={confirmBulkDelete}
        title={`Delete ${selectedMonitors.length} selected monitor${selectedMonitors.length === 1 ? '' : 's'}`}
        description="The selected monitors and their check history will be removed. This cannot be undone."
        confirmLabel="Delete selected"
        loading={deleting}
        onConfirm={handleBulkDelete}
        onCancel={() => !deleting && setConfirmBulkDelete(false)}
      />
    </div>
  );
}
