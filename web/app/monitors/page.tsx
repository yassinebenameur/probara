'use client';

import { useState, useEffect, useMemo, useRef } from 'react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { Plus, Download, Upload, Search, Tag, ChevronDown, Activity, CheckSquare, FolderPlus, Move, Trash2, Bell, Layers } from 'lucide-react';
import Button from '@/components/ui/Button';
import Pill from '@/components/ui/Pill';
import FilterChip from '@/components/ui/FilterChip';
import PageHeader from '@/components/ui/PageHeader';
import SharedEmptyState from '@/components/ui/EmptyState';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import { useToast } from '@/components/ui/ToastProvider';
import { BulkAlertingModal } from '@/components/monitors/BulkAlertingModal';
import { Monitor, CheckResult, AgentMetrics } from '@/lib/types';
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
import {
  calculateUptime,
  countOperationalResults,
  formatInterval,
  formatTimeAgo,
  getEffectiveMonitorStatus,
  isPlatformResult,
  MonitorDisplayStatus,
  calculateLatencyStats,
} from '@/lib/monitor-utils';

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

// Tag color palette - vibrant colors that work on dark backgrounds
const TAG_COLORS = [
  { bg: 'bg-rose-500/20', text: 'text-rose-400', border: 'border-rose-500/30' },
  { bg: 'bg-amber-500/20', text: 'text-amber-400', border: 'border-amber-500/30' },
  { bg: 'bg-lime-500/20', text: 'text-lime-400', border: 'border-lime-500/30' },
  { bg: 'bg-emerald-500/20', text: 'text-emerald-400', border: 'border-emerald-500/30' },
  { bg: 'bg-teal-500/20', text: 'text-teal-400', border: 'border-teal-500/30' },
  { bg: 'bg-cyan-500/20', text: 'text-cyan-400', border: 'border-cyan-500/30' },
  { bg: 'bg-sky-500/20', text: 'text-sky-400', border: 'border-sky-500/30' },
  { bg: 'bg-blue-500/20', text: 'text-blue-400', border: 'border-blue-500/30' },
  { bg: 'bg-indigo-500/20', text: 'text-indigo-400', border: 'border-indigo-500/30' },
  { bg: 'bg-violet-500/20', text: 'text-violet-400', border: 'border-violet-500/30' },
  { bg: 'bg-purple-500/20', text: 'text-purple-400', border: 'border-purple-500/30' },
  { bg: 'bg-fuchsia-500/20', text: 'text-fuchsia-400', border: 'border-fuchsia-500/30' },
  { bg: 'bg-pink-500/20', text: 'text-pink-400', border: 'border-pink-500/30' },
  { bg: 'bg-orange-500/20', text: 'text-orange-400', border: 'border-orange-500/30' },
];

// Hash function to get consistent color for a tag
function getTagColor(tag: string) {
  let hash = 0;
  for (let i = 0; i < tag.length; i++) {
    hash = tag.charCodeAt(i) + ((hash << 5) - hash);
  }
  return TAG_COLORS[Math.abs(hash) % TAG_COLORS.length];
}

// Tag pill — renders a colored Pill using the per-tag color palette. When onClick
// is provided, the wrapping span becomes a focusable button with a selected ring.
function TagPill({ tag, size = 'sm', onClick, selected = false }: {
  tag: string;
  size?: 'xs' | 'sm';
  onClick?: () => void;
  selected?: boolean;
}) {
  const color = getTagColor(tag);
  const pill = (
    <Pill tone="tag" size={size} color={color}>
      {tag}
    </Pill>
  );
  if (!onClick) return pill;
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={selected}
      className={`inline-flex rounded-full transition-all focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-500/40 ${
        selected ? 'ring-1 ring-offset-1 ring-offset-slate-900 ring-white/20' : 'hover:brightness-125'
      }`}
    >
      {pill}
    </button>
  );
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
const TYPE_LABELS: Record<string, { label: string; short: string }> = {
  http: { label: 'HTTP', short: 'HTTP' },
  ping: { label: 'Ping', short: 'PING' },
  dns: { label: 'DNS', short: 'DNS' },
  grpc: { label: 'gRPC', short: 'GRPC' },
  agent: { label: 'Agent', short: 'AGENT' },
  group: { label: 'Group', short: 'GROUP' },
  push: { label: 'Push', short: 'PUSH' },
  sip: { label: 'SIP', short: 'SIP' },
  synthetic_api: { label: 'Synthetic API', short: 'SYN API' },
  synthetic_browser: { label: 'Synthetic Browser', short: 'SYN BROWSER' },
  redis: { label: 'Redis', short: 'REDIS' },
  postgres: { label: 'PostgreSQL', short: 'POSTGRES' },
  mongodb: { label: 'MongoDB', short: 'MONGO' },
  rabbitmq: { label: 'RabbitMQ', short: 'RABBITMQ' },
};

function monitorTypeLabel(type: string, variant: 'label' | 'short' = 'label') {
  return TYPE_LABELS[type]?.[variant] ?? type.replace(/_/g, ' ');
}

// Type badge component
function TypeBadge({ type }: { type: string }) {
  const colors: Record<string, string> = {
    http: 'text-cyan-400',
    ping: 'text-violet-400',
    dns: 'text-sky-400',
    grpc: 'text-teal-400',
    agent: 'text-amber-400',
    group: 'text-indigo-400',
    push: 'text-emerald-400',
    sip: 'text-orange-400',
    synthetic_api: 'text-fuchsia-400',
    synthetic_browser: 'text-pink-400',
    redis: 'text-red-400',
    postgres: 'text-blue-400',
    mongodb: 'text-green-400',
    rabbitmq: 'text-orange-300',
  };
  return (
    <span className={`text-[10px] font-medium uppercase ${colors[type] || 'text-slate-400'}`}>
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
        <div key={`e-${i}`} className="h-3 w-0.5 rounded-sm bg-slate-700/50" />
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
    <div className="flex items-center gap-3 text-[10px]">
      <div className="flex items-center gap-1">
        <svg className="h-3 w-3 text-cyan-400" fill="none" viewBox="0 0 24 24" stroke="currentColor">
          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 3v2m6-2v2M9 19v2m6-2v2M5 9H3m2 6H3m18-6h-2m2 6h-2M7 19h10a2 2 0 002-2V7a2 2 0 00-2-2H7a2 2 0 00-2 2v10a2 2 0 002 2z" />
        </svg>
        <span className="text-slate-400">CPU</span>
        <span className="text-white font-medium">{metrics.cpu_percent.toFixed(0)}%</span>
      </div>
      <div className="flex items-center gap-1">
        <svg className="h-3 w-3 text-violet-400" fill="none" viewBox="0 0 24 24" stroke="currentColor">
          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 11H5m14 0a2 2 0 012 2v6a2 2 0 01-2 2H5a2 2 0 01-2-2v-6a2 2 0 012-2m14 0V9a2 2 0 00-2-2M5 11V9a2 2 0 012-2m0 0V5a2 2 0 012-2h6a2 2 0 012 2v2M7 7h10" />
        </svg>
        <span className="text-slate-400">MEM</span>
        <span className="text-white font-medium">{((metrics.memory_used / metrics.memory_total) * 100).toFixed(0)}%</span>
      </div>
      <div className="flex items-center gap-1">
        <svg className="h-3 w-3 text-emerald-400" fill="none" viewBox="0 0 24 24" stroke="currentColor">
          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4 7v10c0 2.21 3.582 4 8 4s8-1.79 8-4V7M4 7c0 2.21 3.582 4 8 4s8-1.79 8-4M4 7c0-2.21 3.582-4 8-4s8 1.79 8 4" />
        </svg>
        <span className="text-slate-400">DISK</span>
        <span className="text-white font-medium">{((metrics.disk_used / metrics.disk_total) * 100).toFixed(0)}%</span>
      </div>
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

// Custom checkbox component matching the dark theme
function SelectCheckbox({ 
  checked, 
  onChange, 
  visible = true,
  label 
}: { 
  checked: boolean; 
  onChange: () => void; 
  visible?: boolean;
  label?: string;
}) {
  if (!visible) return null;
  
  return (
    <button
      onClick={(e) => { e.stopPropagation(); onChange(); }}
      className={`flex h-4 w-4 flex-shrink-0 items-center justify-center rounded border transition-all ${
        checked
          ? 'border-cyan-500 bg-cyan-500'
          : 'border-white/20 bg-slate-900/80 hover:border-white/40'
      }`}
      aria-label={label}
    >
      {checked && (
        <svg className="h-2.5 w-2.5 text-white" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={3}>
          <path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" />
        </svg>
      )}
    </button>
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
  const [open, setOpen] = useState(false);
  const [pos, setPos] = useState<{ top: number; left: number } | null>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);

  // The menu renders position:fixed so it can escape the group children's
  // overflow-y-auto container; anchor it to the trigger on open.
  const MENU_WIDTH = 144;
  const MENU_HEIGHT = 196;

  const toggle = (e: React.MouseEvent) => {
    e.stopPropagation();
    if (open) {
      setOpen(false);
      return;
    }
    const rect = triggerRef.current?.getBoundingClientRect();
    if (!rect) return;
    const openUp = rect.bottom + 4 + MENU_HEIGHT > window.innerHeight;
    setPos({
      top: openUp ? rect.top - 4 - MENU_HEIGHT : rect.bottom + 4,
      left: Math.max(8, rect.right - MENU_WIDTH),
    });
    setOpen(true);
  };

  useEffect(() => {
    if (!open) return;
    const close = () => setOpen(false);
    window.addEventListener('click', close);
    window.addEventListener('scroll', close, true);
    window.addEventListener('resize', close);
    return () => {
      window.removeEventListener('click', close);
      window.removeEventListener('scroll', close, true);
      window.removeEventListener('resize', close);
    };
  }, [open]);

  const itemClass =
    'block w-full rounded px-2.5 py-1.5 text-left text-xs text-slate-300 transition-colors hover:bg-white/[0.06] hover:text-white';

  return (
    <>
      <button
        ref={triggerRef}
        type="button"
        onClick={toggle}
        aria-label="Monitor actions"
        aria-expanded={open}
        className="rounded p-1.5 text-slate-400 hover:bg-slate-700 hover:text-white cursor-pointer"
      >
        <svg className="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 6h.01M12 12h.01M12 18h.01" />
        </svg>
      </button>
      {open && pos && (
        <div
          className="fixed z-50 w-36 rounded-lg border border-white/[0.08] bg-slate-900/95 p-1 shadow-lg backdrop-blur-sm"
          style={{ top: pos.top, left: pos.left }}
          onClick={(e) => e.stopPropagation()}
        >
          <Link
            href={`/monitors/${monitorId}`}
            onClick={(e) => {
              e.stopPropagation();
              setOpen(false);
            }}
            className={itemClass}
          >
            Edit
          </Link>
          <Link
            href={`/monitors/new?clone=${monitorId}`}
            onClick={(e) => {
              e.stopPropagation();
              setOpen(false);
            }}
            className={itemClass}
          >
            Clone
          </Link>
          <button
            onClick={(e) => {
              e.stopPropagation();
              setOpen(false);
              onToggleEnabled(monitor);
            }}
            className={itemClass}
          >
            {monitor.enabled ? 'Pause' : 'Start'}
          </button>
          <div className="my-1 border-t border-white/[0.06] px-2.5 pt-1 text-[9px] uppercase tracking-wider text-slate-500">
            Snooze alerts
          </div>
          <div className="flex gap-1 px-2.5 pb-1">
            {[
              { label: '1h', minutes: 60 },
              { label: '4h', minutes: 240 },
              { label: '24h', minutes: 1440 },
            ].map(({ label, minutes }) => (
              <button
                key={label}
                onClick={(e) => {
                  e.stopPropagation();
                  setOpen(false);
                  onSnooze(monitor, minutes);
                }}
                className="flex-1 rounded border border-white/[0.08] px-1.5 py-1 text-[10px] text-slate-300 transition-colors hover:border-sky-500/40 hover:bg-sky-500/10 hover:text-sky-300"
              >
                {label}
              </button>
            ))}
          </div>
          <button
            onClick={(e) => {
              e.stopPropagation();
              setOpen(false);
              onDelete();
            }}
            className="block w-full rounded px-2.5 py-1.5 text-left text-xs text-rose-300 transition-colors hover:bg-rose-500/10 hover:text-rose-200"
          >
            Delete
          </button>
        </div>
      )}
    </>
  );
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
    if (monitor.url) return monitor.url;
    if (monitor.config && 'host' in monitor.config) return monitor.config.host;
    return null;
  };

  return (
    <div
      onClick={onClick}
      className={`group flex items-center gap-3 rounded-lg border px-3 py-2.5 transition-all cursor-pointer ${
        isSelected
          ? 'border-cyan-500/40 bg-cyan-500/5'
          : isChecked
          ? 'border-cyan-500/20 bg-cyan-500/5'
          : 'border-transparent hover:border-white/[0.06] hover:bg-slate-800/30'
      } ${isChild ? 'bg-slate-800/20' : ''}`}
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
              className="rounded-full border border-sky-500/30 bg-sky-500/10 px-1.5 py-0.5 text-[9px] font-medium uppercase tracking-wide text-sky-300"
              title={
                monitor.maintenance_until
                  ? `Alerts suppressed until ${new Date(monitor.maintenance_until).toLocaleString()}`
                  : 'Alerts suppressed during maintenance'
              }
            >
              Maintenance
            </span>
          )}
          {/* Tags */}
          {monitor.tags && monitor.tags.length > 0 && (
            <div className="flex items-center gap-1 flex-wrap">
              {monitor.tags.slice(0, 3).map((tag) => (
                <TagPill key={tag} tag={tag} size="xs" />
              ))}
              {monitor.tags.length > 3 && (
                <span className="text-[9px] text-slate-500">+{monitor.tags.length - 3}</span>
              )}
            </div>
          )}
        </div>
        {getUrl() && (
          <p className="text-[10px] text-slate-500 truncate mt-0.5">{getUrl()}</p>
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
      <div className="hidden md:flex items-center gap-4 text-xs">
        <div className="w-14 text-right">
          <span className={`font-medium ${uptime >= 99 ? 'text-emerald-400' : uptime >= 95 ? 'text-amber-400' : 'text-rose-400'}`}>
            {operationalCount > 0 ? `${uptime.toFixed(1)}%` : '—'}
          </span>
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
    ? 'text-rose-400'
    : healthyCount === members.length && members.length > 0
      ? 'text-emerald-400/90'
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
    <div className={`rounded-xl border transition-all ${
      isSelected ? 'border-indigo-500/40 bg-indigo-500/5' : isChecked ? 'border-cyan-500/20 bg-cyan-500/5' : 'border-white/[0.06] bg-slate-900/30'
    }`}>
      {/* Group Header */}
      <div
        onClick={onClick}
        className="flex items-center gap-3 px-3 py-2.5 cursor-pointer group"
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
          className="rounded p-1 hover:bg-slate-700 transition-colors"
        >
          <svg 
            className={`h-4 w-4 text-indigo-400 transition-transform ${expanded ? 'rotate-90' : ''}`}
            fill="none" viewBox="0 0 24 24" stroke="currentColor"
          >
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 5l7 7-7 7" />
          </svg>
        </button>

        {/* Group Icon */}
        <div className="flex h-7 w-7 items-center justify-center rounded-md bg-indigo-500/20">
          <svg className="h-4 w-4 text-indigo-400" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 11H5m14 0a2 2 0 012 2v6a2 2 0 01-2 2H5a2 2 0 01-2-2v-6a2 2 0 012-2m14 0V9a2 2 0 00-2-2M5 11V9a2 2 0 012-2m0 0V5a2 2 0 012-2h6a2 2 0 012 2v2M7 7h10" />
          </svg>
        </div>

        {/* Name */}
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2 flex-wrap">
            <span className="min-w-0 truncate text-sm font-medium text-white">{monitor.name}</span>
            <span className="text-[10px] font-medium uppercase text-indigo-400">GROUP</span>
            {/* Tags */}
            {monitor.tags && monitor.tags.length > 0 && (
              <div className="flex items-center gap-1">
                {monitor.tags.slice(0, 2).map((tag) => (
                  <TagPill key={tag} tag={tag} size="xs" />
                ))}
                {monitor.tags.length > 2 && (
                  <span className="text-[9px] text-slate-500">+{monitor.tags.length - 2}</span>
                )}
              </div>
            )}
          </div>
          <p className="text-[10px] text-slate-500 mt-0.5">
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
            <span className="text-[10px] text-slate-500">+{members.length - 8}</span>
          )}
        </div>

        {/* Status */}
        <StatusDot status={status} />

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
        <div className="dashboard-scroll max-h-[60vh] overflow-y-auto overscroll-contain border-t border-white/[0.04] px-3 py-3">
          {hiddenMemberCount > 0 && (
            <p className="mb-2 ml-5 pl-2.5 text-[10px] text-slate-500">
              Showing {visibleMembers.length} matching of {members.length} members
            </p>
          )}
          <div className="ml-5 space-y-1.5 border-l border-white/[0.08] pl-2.5">
          {visibleMembers.map((member) => {
            const memberKey = `${monitor.id}:${member.id}`;
            if (member.type === 'group') {
              const cycleDetected = nextVisitedGroupIDs.has(member.id);
              return (
                <div key={memberKey} className="space-y-1">
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
                      <p className="ml-6 text-[10px] text-rose-400">
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
}: {
  monitor: Monitor | null;
  results: CheckResult[];
  members?: Monitor[];
  memberResults?: Record<string, CheckResult[]>;
  onSelectMember?: (id: string) => void;
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
      <div className="flex h-64 items-center justify-center rounded-xl border border-white/[0.06] bg-slate-900/40 p-6">
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

  return (
    <div className="rounded-xl border border-white/[0.06] bg-slate-900/40 overflow-hidden">
      {/* Header */}
      <div className="border-b border-white/[0.06] p-4">
        <div className="flex items-start justify-between">
          <div>
            <h3 className="font-medium text-white">{monitor.name}</h3>
            <p className="text-xs text-slate-500 mt-0.5">{getUrl() || monitor.type}</p>
          </div>
          <StatusDot status={status} />
        </div>
      </div>

      {/* Stats */}
      <div className="grid grid-cols-3 divide-x divide-white/[0.06] border-b border-white/[0.06]">
        <div className="p-3 text-center">
          <p className="text-lg font-semibold text-white">{operationalCount > 0 ? `${uptime.toFixed(1)}%` : '—'}</p>
          <p className="text-[10px] text-slate-500">Uptime</p>
        </div>
        <div className="p-3 text-center">
          <p className="text-lg font-semibold text-white">{latencyStats.median || 0}ms</p>
          <p className="text-[10px] text-slate-500">Median</p>
        </div>
        <div className="p-3 text-center">
          <p className="text-lg font-semibold text-white">{latencyStats.p95 || 0}ms</p>
          <p className="text-[10px] text-slate-500">P95</p>
        </div>
      </div>

      {/* Group Members */}
      {monitor.type === 'group' && (
        <div className="border-b border-white/[0.06] p-4">
          <h4 className="text-[10px] font-medium uppercase tracking-wider text-slate-500 mb-2">
            Members ({members.length})
          </h4>
          {members.length > 0 && (
            <>
              <div className="mb-1.5 flex h-1.5 overflow-hidden rounded-full bg-slate-800">
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
                  <div className="bg-slate-600" style={{ width: `${(memberOther / members.length) * 100}%` }} />
                )}
              </div>
              <p className="mb-2 text-[10px] text-slate-500">
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
            <div className="dashboard-scroll max-h-56 space-y-0.5 overflow-y-auto overscroll-contain">
              {members.map((member) => {
                const memberStatus = getEffectiveMonitorStatus(member, memberResults[member.id] || []);
                const memberOperational = countOperationalResults(memberResults[member.id] || []);
                const memberUptime = calculateUptime(memberResults[member.id] || []);
                return (
                  <button
                    key={member.id}
                    onClick={() => onSelectMember?.(member.id)}
                    className="flex w-full items-center gap-2 rounded-md px-1.5 py-1 text-left transition-colors hover:bg-white/[0.04]"
                  >
                    <StatusDot status={memberStatus} />
                    <span className="min-w-0 flex-1 truncate text-xs text-slate-300">{member.name}</span>
                    <span className="text-[10px] text-slate-500">
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
        <div className="border-b border-white/[0.06] p-4">
          <h4 className="text-[10px] font-medium uppercase tracking-wider text-slate-500 mb-3">System Metrics</h4>
          <div className="grid grid-cols-2 gap-3">
            <div className="rounded-lg bg-slate-800/50 p-2.5">
              <div className="flex items-center justify-between">
                <span className="text-[10px] text-slate-500">CPU</span>
                <span className="text-xs font-medium text-cyan-400">{agentMetrics.cpu_percent.toFixed(1)}%</span>
              </div>
              <div className="mt-1.5 h-1 rounded-full bg-slate-700 overflow-hidden">
                <div className="h-full bg-cyan-500 rounded-full" style={{ width: `${agentMetrics.cpu_percent}%` }} />
              </div>
            </div>
            <div className="rounded-lg bg-slate-800/50 p-2.5">
              <div className="flex items-center justify-between">
                <span className="text-[10px] text-slate-500">Memory</span>
                <span className="text-xs font-medium text-violet-400">
                  {((agentMetrics.memory_used / agentMetrics.memory_total) * 100).toFixed(1)}%
                </span>
              </div>
              <div className="mt-1.5 h-1 rounded-full bg-slate-700 overflow-hidden">
                <div className="h-full bg-violet-500 rounded-full" style={{ width: `${(agentMetrics.memory_used / agentMetrics.memory_total) * 100}%` }} />
              </div>
            </div>
            <div className="rounded-lg bg-slate-800/50 p-2.5">
              <div className="flex items-center justify-between">
                <span className="text-[10px] text-slate-500">Disk</span>
                <span className="text-xs font-medium text-emerald-400">
                  {((agentMetrics.disk_used / agentMetrics.disk_total) * 100).toFixed(1)}%
                </span>
              </div>
              <div className="mt-1.5 h-1 rounded-full bg-slate-700 overflow-hidden">
                <div className="h-full bg-emerald-500 rounded-full" style={{ width: `${(agentMetrics.disk_used / agentMetrics.disk_total) * 100}%` }} />
              </div>
            </div>
            <div className="rounded-lg bg-slate-800/50 p-2.5">
              <div className="flex items-center justify-between">
                <span className="text-[10px] text-slate-500">Load Avg</span>
                <span className="text-xs font-medium text-amber-400">{agentMetrics.load_avg_1.toFixed(2)}</span>
              </div>
              <p className="text-[10px] text-slate-600 mt-1">{agentMetrics.process_count} processes</p>
            </div>
          </div>
          <div className="mt-3 grid grid-cols-2 gap-2 text-[10px]">
            <div className="flex justify-between text-slate-500">
              <span>Network In</span>
              <span className="text-slate-400">{formatBytes(agentMetrics.network_bytes_in)}</span>
            </div>
            <div className="flex justify-between text-slate-500">
              <span>Network Out</span>
              <span className="text-slate-400">{formatBytes(agentMetrics.network_bytes_out)}</span>
            </div>
          </div>
        </div>
      )}

      {/* Tags */}
      {monitor.tags && monitor.tags.length > 0 && (
        <div className="p-4 border-b border-white/[0.06]">
          <h4 className="text-[10px] font-medium uppercase tracking-wider text-slate-500 mb-2">Tags</h4>
          <div className="flex flex-wrap gap-1.5">
            {monitor.tags.map((tag) => (
              <TagPill key={tag} tag={tag} size="sm" />
            ))}
          </div>
        </div>
      )}

      {/* Config */}
      <div className="p-4 border-b border-white/[0.06]">
        <h4 className="text-[10px] font-medium uppercase tracking-wider text-slate-500 mb-2">Config</h4>
        <div className="space-y-1.5 text-xs">
          <div className="flex justify-between">
            <span className="text-slate-500">Interval</span>
            <span className="text-slate-300">{formatInterval(monitor.interval_seconds)}</span>
          </div>
          <div className="flex justify-between">
            <span className="text-slate-500">Timeout</span>
            <span className="text-slate-300">{monitor.timeout_seconds}s</span>
          </div>
          <div className="flex justify-between">
            <span className="text-slate-500">Status</span>
            <span className={monitor.enabled ? 'text-emerald-400' : 'text-slate-500'}>
              {monitor.enabled ? 'Active' : 'Paused'}
            </span>
          </div>
        </div>
      </div>

      {/* Synthetic Browser Screenshot */}
      {monitor.type === 'synthetic_browser' && (
        <div className="p-4 border-b border-white/[0.06]">
          <h4 className="text-[10px] font-medium uppercase tracking-wider text-slate-500 mb-2">Latest failure screenshot</h4>
          {screenshotLoading ? (
            <p className="text-xs text-slate-500">Loading screenshot...</p>
          ) : screenshotBlobURL ? (
            <div className="overflow-hidden rounded-lg border border-white/[0.08] bg-slate-800/40 p-2">
              <img
                src={screenshotBlobURL}
                alt="Latest synthetic browser failure screenshot"
                className="h-auto max-h-[220px] w-full rounded-md object-contain"
                loading="lazy"
                decoding="async"
              />
              <div className="mt-2 text-[10px] text-slate-500">
                {latestSyntheticBrowserResult ? formatTimeAgo(latestSyntheticBrowserResult.created_at) : 'Latest run'}
              </div>
            </div>
          ) : (
            <p className="text-xs text-slate-500">No screenshot available yet.</p>
          )}
        </div>
      )}

      {/* Recent Results */}
      <div className="p-4">
        <h4 className="text-[10px] font-medium uppercase tracking-wider text-slate-500 mb-2">Recent</h4>
        {results.length > 0 ? (
          <div className="space-y-1">
            {results.slice(0, 5).map((r, i) => (
              <div key={i} className="flex items-center justify-between text-xs py-1">
                <div className="flex items-center gap-2">
                  <span
                    className={`h-1.5 w-1.5 rounded-full ${
                      isPlatformResult(r) ? 'bg-slate-500' : r.status === 'success' ? 'bg-emerald-500' : 'bg-rose-500'
                    }`}
                  />
                  <span className="text-slate-500">{formatTimeAgo(r.created_at)}</span>
                </div>
                <span className="text-slate-400">{r.latency_ms || 0}ms</span>
              </div>
            ))}
          </div>
        ) : (
          <p className="text-xs text-slate-500 text-center py-2">No results</p>
        )}
      </div>

      {/* View Details */}
      <div className="p-3 border-t border-white/[0.06]">
        <Button variant="ghost" size="sm" className="w-full" asChild>
          <Link href={`/monitors/${monitor.id}`}>View details</Link>
        </Button>
      </div>
    </div>
  );
}

// Loading skeleton
function LoadingSkeleton() {
  return (
    <div className="space-y-2">
      {[...Array(5)].map((_, i) => (
        <div key={i} className="h-14 animate-pulse rounded-lg bg-slate-800/50" />
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
  const [monitors, setMonitors] = useState<Monitor[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>('');
  const [searchTerm, setSearchTerm] = useState('');
  const [selectedTypes, setSelectedTypes] = useState<Set<string>>(new Set());
  const [showTypeFilter, setShowTypeFilter] = useState(false);
  const [statusFilter, setStatusFilter] = useState<string>('all');
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
    setSelectedMonitorId(id);
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

      if (monitorsList.length > 0 && !selectedMonitorId) {
        setSelectedMonitorId(monitorsList[0].id);
      }

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
        setSelectedMonitorId(monitors.find((m) => m.id !== id)?.id || null);
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
    return matchesSearch && matchesType && matchesStatus && matchesTags;
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
    searchTerm.trim() !== '' || selectedTypes.size > 0 || statusFilter !== 'all' || selectedTags.size > 0;

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
    <div className="space-y-5">
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
            <Button variant="ghost" size="sm" icon={<Upload strokeWidth={1.75} />} asChild>
              <Link href="/monitors/import">Import</Link>
            </Button>
            <Button variant="ghost" size="sm" icon={<Plus strokeWidth={1.75} />} asChild>
              <Link href="/monitors/new">Add monitor</Link>
            </Button>
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
              className="input input-sm pl-8"
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
        </div>

        {/* Type Filter Dropdown */}
        {showTypeFilter && typeCounts.length > 0 && (
          <div className="rounded-xl border border-white/[0.06] bg-slate-900/60 p-3 backdrop-blur-sm">
            <div className="flex items-center justify-between mb-2">
              <span className="text-[10px] font-medium uppercase tracking-wider text-slate-500">
                Filter by type
              </span>
              {selectedTypes.size > 0 && (
                <button
                  onClick={() => setSelectedTypes(new Set())}
                  className="text-[10px] text-slate-400 hover:text-white transition-colors"
                >
                  Clear all
                </button>
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
          <div className="rounded-xl border border-white/[0.06] bg-slate-900/60 p-3 backdrop-blur-sm">
            <div className="flex items-center justify-between mb-2">
              <span className="text-[10px] font-medium uppercase tracking-wider text-slate-500">
                Filter by tags
              </span>
              {selectedTags.size > 0 && (
                <button
                  onClick={clearTagFilters}
                  className="text-[10px] text-slate-400 hover:text-white transition-colors"
                >
                  Clear all
                </button>
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
        <div className="flex items-center justify-between rounded-lg border border-rose-500/20 bg-rose-500/10 px-3 py-2">
          <p className="text-xs text-rose-400">{error}</p>
          <Button variant="danger" size="xs" onClick={loadMonitors}>Retry</Button>
        </div>
      )}

      {/* Content */}
      {loading ? (
        <LoadingSkeleton />
      ) : monitors.length === 0 ? (
        <EmptyState />
      ) : (
        <div className="grid gap-5 lg:grid-cols-[minmax(0,1fr)_320px]">
          {/* Monitor List */}
          <div className="min-w-0 space-y-2">
            {/* Selection Mode Toggle & Actions Bar */}
            {filteredMonitors.length > 0 && (
              <div className="flex flex-wrap items-center gap-x-2 gap-y-2">
                {/* Selection mode toggle */}
                <Button
                  variant={selectionMode ? 'accent' : 'ghost'}
                  size="xs"
                  icon={<CheckSquare strokeWidth={1.75} />}
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
                    <div className="h-4 w-px bg-white/[0.08]" />

                    <Button variant="ghost" size="xs" onClick={toggleSelectAllFiltered}>
                      {allFilteredSelected ? 'Deselect all' : 'Select all'}
                    </Button>

                    {selectedMonitorIds.size > 0 && (
                      <>
                        <Pill tone="info" size="xs">{selectedMonitorIds.size} selected</Pill>

                        <div className="h-4 w-px bg-white/[0.08]" />

                        {/* Group creation */}
                        <div className="flex items-center gap-1.5">
                          <input
                            type="text"
                            placeholder="New group name..."
                            value={groupNameInput}
                            onChange={(e) => setGroupNameInput(e.target.value)}
                            className="input input-xs h-7 w-32 text-[10px]"
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
                            className="input input-xs h-7 w-36 text-[10px]"
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

                        <div className="h-4 w-px bg-white/[0.08]" />

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
            
            {filteredMonitors.length === 0 ? (
              <div className="rounded-lg border border-white/[0.06] p-6 text-center">
                <p className="text-xs text-slate-500">No monitors match filters</p>
              </div>
            ) : (
              filteredMonitors.map((monitor) => (
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
              ))
            )}
          </div>

          {/* Detail Panel */}
          <div className="hidden lg:block">
            <div className="dashboard-scroll sticky top-6 max-h-[calc(100vh-3rem)] overflow-y-auto overscroll-contain rounded-xl">
              <DetailPanel
                monitor={selectedMonitor}
                results={selectedMonitor ? checkResultsMap[selectedMonitor.id] || [] : []}
                members={selectedMonitor ? groupMembersMap[selectedMonitor.id] || [] : []}
                memberResults={checkResultsMap}
                onSelectMember={setSelectedMonitorId}
              />
            </div>
          </div>
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
