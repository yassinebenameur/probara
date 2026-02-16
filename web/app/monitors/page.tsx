'use client';

import { useState, useEffect, useMemo } from 'react';
import Link from 'next/link';
import { Monitor, CheckResult, AgentMetrics } from '@/lib/types';
import { getMonitors, deleteMonitor, getMonitorResults, getGroupMembers, createMonitor, getSyntheticBrowserScreenshotUrl } from '@/lib/api';
import { getApiKey } from '@/lib/auth';
import {
  calculateUptime,
  formatInterval,
  formatTimeAgo,
  getLatestStatus,
  calculateLatencyStats,
} from '@/lib/monitor-utils';

const DEBUG_INGEST_URL = process.env.NEXT_PUBLIC_DEBUG_INGEST_URL;

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

// Tag pill component
function TagPill({ tag, size = 'sm', onClick, selected = false }: { 
  tag: string; 
  size?: 'xs' | 'sm'; 
  onClick?: () => void;
  selected?: boolean;
}) {
  const color = getTagColor(tag);
  const sizeClasses = size === 'xs' 
    ? 'text-[9px] px-1.5 py-0.5' 
    : 'text-[10px] px-2 py-0.5';
  
  return (
    <span
      onClick={onClick}
      className={`inline-flex items-center rounded-full border ${sizeClasses} font-medium transition-all ${
        selected 
          ? `${color.bg} ${color.text} ${color.border} ring-1 ring-offset-1 ring-offset-slate-900 ring-white/20` 
          : `${color.bg} ${color.text} ${color.border}`
      } ${onClick ? 'cursor-pointer hover:brightness-125' : ''}`}
    >
      {tag}
    </span>
  );
}

// Status dot component
function StatusDot({ status }: { status: 'up' | 'down' | 'degraded' }) {
  const colors = {
    up: 'bg-emerald-500',
    down: 'bg-rose-500',
    degraded: 'bg-amber-500',
  };
  return <span className={`h-2 w-2 rounded-full ${colors[status]}`} />;
}

// Type badge component
function TypeBadge({ type }: { type: string }) {
  const colors: Record<string, string> = {
    http: 'text-cyan-400',
    ping: 'text-violet-400',
    dns: 'text-sky-400',
    agent: 'text-amber-400',
    group: 'text-indigo-400',
    push: 'text-emerald-400',
    sip: 'text-orange-400',
    synthetic_api: 'text-fuchsia-400',
    synthetic_browser: 'text-pink-400',
  };
  return (
    <span className={`text-[10px] font-medium uppercase ${colors[type] || 'text-slate-400'}`}>
      {type}
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
            result.status === 'success' ? 'bg-emerald-500' : 'bg-rose-500'
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

// Compact monitor row component
function MonitorRow({ 
  monitor, 
  results, 
  isSelected, 
  onClick, 
  onDelete,
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
  isChecked: boolean;
  onToggleSelect: () => void;
  selectionMode: boolean;
  isChild?: boolean;
}) {
  const status = getLatestStatus(results);
  const uptime = calculateUptime(results);
  const latencyStats = calculateLatencyStats(results);
  const latestResult = results[0];
  const agentMetrics = isAgentMetrics(latestResult?.metrics_data) ? latestResult.metrics_data : null;

  const getUrl = () => {
    if (monitor.config && 'url' in monitor.config) return monitor.config.url;
    if (monitor.config && 'base_url' in monitor.config) return monitor.config.base_url || null;
    if (monitor.config && 'start_url' in monitor.config) return monitor.config.start_url;
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
      } ${isChild ? 'ml-4 bg-slate-800/20 rounded-lg' : ''}`}
    >
      {!isChild && (
        <SelectCheckbox 
          checked={isChecked} 
          onChange={onToggleSelect}
          visible={selectionMode}
          label={`Select ${monitor.name}`}
        />
      )}
      {/* Status */}
      <StatusDot status={status} />

      {/* Name & Type */}
      <div className="flex-1 min-w-0">
        <div className="flex items-center gap-2 flex-wrap">
          <span className="text-sm font-medium text-white truncate">{monitor.name}</span>
          <TypeBadge type={monitor.type} />
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
            {results.length > 0 ? `${uptime.toFixed(1)}%` : '—'}
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
      <div className="flex items-center gap-1 opacity-0 group-hover:opacity-100 transition-opacity">
        <Link href={`/monitors/${monitor.id}`} onClick={(e) => e.stopPropagation()}>
          <button className="rounded p-1.5 text-slate-400 hover:bg-slate-700 hover:text-white">
            <svg className="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M15.232 5.232l3.536 3.536m-2.036-5.036a2.5 2.5 0 113.536 3.536L6.5 21.036H3v-3.572L16.732 3.732z" />
            </svg>
          </button>
        </Link>
        <button
          onClick={(e) => { e.stopPropagation(); onDelete(); }}
          className="rounded p-1.5 text-slate-400 hover:bg-rose-500/10 hover:text-rose-400"
        >
          <svg className="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
          </svg>
        </button>
      </div>
    </div>
  );
}

// Group card with expandable children
function GroupCard({
  monitor,
  results,
  members,
  memberResults,
  isSelected,
  onClick,
  onDelete,
  isChecked,
  onToggleSelect,
  selectionMode,
  onSelectMember,
  selectedMonitorId,
}: {
  monitor: Monitor;
  results: CheckResult[];
  members: Monitor[];
  memberResults: Record<string, CheckResult[]>;
  isSelected: boolean;
  onClick: () => void;
  onDelete: () => void;
  isChecked: boolean;
  onToggleSelect: () => void;
  selectionMode: boolean;
  onSelectMember: (id: string) => void;
  selectedMonitorId: string | null;
}) {
  const [expanded, setExpanded] = useState(false); // Collapsed by default
  const status = getLatestStatus(results);
  const uptime = calculateUptime(results);

  // Calculate aggregate stats from members
  const memberStats = members.map(m => ({
    status: getLatestStatus(memberResults[m.id] || []),
    uptime: calculateUptime(memberResults[m.id] || [])
  }));
  const healthyCount = memberStats.filter(s => s.status === 'up').length;

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
            <span className="text-sm font-medium text-white">{monitor.name}</span>
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
            {healthyCount}/{members.length} healthy · {uptime.toFixed(1)}% uptime
          </p>
        </div>

        {/* Member Status Dots */}
        <div className="hidden sm:flex items-center gap-1">
          {members.slice(0, 8).map((m) => (
            <div
              key={m.id}
              className={`h-2 w-2 rounded-full ${
                getLatestStatus(memberResults[m.id] || []) === 'up' 
                  ? 'bg-emerald-500' 
                  : 'bg-rose-500'
              }`}
              title={m.name}
            />
          ))}
          {members.length > 8 && (
            <span className="text-[10px] text-slate-500">+{members.length - 8}</span>
          )}
        </div>

        {/* Status */}
        <StatusDot status={status} />

        {/* Actions */}
        <div className="flex items-center gap-1 opacity-0 group-hover:opacity-100 transition-opacity">
          <Link href={`/monitors/${monitor.id}`} onClick={(e) => e.stopPropagation()}>
            <button className="rounded p-1.5 text-slate-400 hover:bg-slate-700 hover:text-white">
              <svg className="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M15.232 5.232l3.536 3.536m-2.036-5.036a2.5 2.5 0 113.536 3.536L6.5 21.036H3v-3.572L16.732 3.732z" />
              </svg>
            </button>
          </Link>
          <button
            onClick={(e) => { e.stopPropagation(); onDelete(); }}
            className="rounded p-1.5 text-slate-400 hover:bg-rose-500/10 hover:text-rose-400"
          >
            <svg className="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
            </svg>
          </button>
        </div>
      </div>

      {/* Expanded Children */}
      {expanded && members.length > 0 && (
        <div className="border-t border-white/[0.04] px-3 py-3 space-y-2">
          {members.map((member) => (
            <MonitorRow
              key={member.id}
              monitor={member}
              results={memberResults[member.id] || []}
              isSelected={member.id === selectedMonitorId}
              onClick={() => onSelectMember(member.id)}
              onDelete={() => {}}
              isChecked={false}
              onToggleSelect={() => {}}
              selectionMode={false}
              isChild
            />
          ))}
        </div>
      )}
    </div>
  );
}

// Detail Panel Component
function DetailPanel({ 
  monitor, 
  results 
}: { 
  monitor: Monitor | null;
  results: CheckResult[];
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

  const status = getLatestStatus(results);
  const uptime = calculateUptime(results);
  const latencyStats = calculateLatencyStats(results);
  const latestResult = results[0];
  const agentMetrics = isAgentMetrics(latestResult?.metrics_data) ? latestResult.metrics_data : null;

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
          <p className="text-lg font-semibold text-white">{uptime.toFixed(1)}%</p>
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
                  <span className={`h-1.5 w-1.5 rounded-full ${r.status === 'success' ? 'bg-emerald-500' : 'bg-rose-500'}`} />
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
        <Link href={`/monitors/${monitor.id}`}>
          <button className="w-full rounded-lg bg-cyan-500 py-2 text-xs font-medium text-white hover:bg-cyan-400 transition-colors">
            View Details
          </button>
        </Link>
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
    <div className="flex flex-col items-center justify-center rounded-xl border border-dashed border-white/[0.1] py-12">
      <svg className="h-10 w-10 text-slate-600" fill="none" viewBox="0 0 24 24" stroke="currentColor">
        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1} d="M9 19v-6a2 2 0 00-2-2H5a2 2 0 00-2 2v6a2 2 0 002 2h2a2 2 0 002-2zm0 0V9a2 2 0 012-2h2a2 2 0 012 2v10m-6 0a2 2 0 002 2h2a2 2 0 002-2m0 0V5a2 2 0 012-2h2a2 2 0 012 2v14a2 2 0 01-2 2h-2a2 2 0 01-2-2z" />
      </svg>
      <h3 className="mt-3 text-sm font-medium text-white">No monitors yet</h3>
      <p className="mt-1 text-xs text-slate-500">Create your first monitor to start tracking</p>
      <Link href="/monitors/new">
        <button className="mt-4 inline-flex items-center gap-1.5 rounded-lg bg-cyan-500 px-3 py-1.5 text-xs font-medium text-white hover:bg-cyan-400">
          <svg className="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
          </svg>
          Create Monitor
        </button>
      </Link>
    </div>
  );
}

export default function MonitorsPage() {
  const [monitors, setMonitors] = useState<Monitor[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>('');
  const [searchTerm, setSearchTerm] = useState('');
  const [typeFilter, setTypeFilter] = useState<string>('all');
  const [statusFilter, setStatusFilter] = useState<string>('all');
  const [selectedTags, setSelectedTags] = useState<Set<string>>(new Set());
  const [selectedMonitorId, setSelectedMonitorId] = useState<string | null>(null);
  const [checkResultsMap, setCheckResultsMap] = useState<Record<string, CheckResult[]>>({});
  const [groupMembersMap, setGroupMembersMap] = useState<Record<string, Monitor[]>>({});
  const [toast, setToast] = useState<{ message: string; type: 'success' | 'error' } | null>(null);
  const [showTagFilter, setShowTagFilter] = useState(false);
  const [selectionMode, setSelectionMode] = useState(false);
  const [selectedMonitorIds, setSelectedMonitorIds] = useState<Set<string>>(new Set());
  const [groupNameInput, setGroupNameInput] = useState('');

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
      const response = await getMonitors({ page_size: 100 });
      // #region agent log
      debugIngest({
        location: 'page.tsx:loadMonitors:getMonitors',
        message: 'getMonitors API call completed',
        data: {
          durationMs: Date.now() - getMonitorsStart,
          monitorCount: response?.items?.length || 0,
        },
        timestamp: Date.now(),
        sessionId: 'debug-session',
        runId: 'progressive',
        hypothesisId: 'A',
      });
      // #endregion
      const monitorsList = response?.items || [];
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
                  const members = await getGroupMembers(monitor.id);
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

  const handleDelete = async (id: string) => {
    if (!confirm('Are you sure you want to delete this monitor?')) return;

    try {
      await deleteMonitor(id);
      setMonitors(monitors.filter((m) => m.id !== id));
      if (selectedMonitorId === id) {
        setSelectedMonitorId(monitors.find((m) => m.id !== id)?.id || null);
      }
      setToast({ message: 'Monitor deleted', type: 'success' });
    } catch (err: any) {
      setToast({ message: err.message || 'Failed to delete', type: 'error' });
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

  // Filter monitors (exclude monitors that are members of a group - they only show under their group)
  const filteredMonitors = monitors.filter((monitor) => {
    // Don't show monitors that belong to a group as standalone items
    if (groupMemberIds.has(monitor.id)) return false;
    
    const matchesSearch = monitor.name.toLowerCase().includes(searchTerm.toLowerCase());
    const matchesType = typeFilter === 'all' || monitor.type === typeFilter;
    const status = getLatestStatus(checkResultsMap[monitor.id] || []);
    const matchesStatus = statusFilter === 'all' || 
      (statusFilter === 'up' && status === 'up') ||
      (statusFilter === 'down' && (status === 'down' || status === 'degraded'));
    const matchesTags = selectedTags.size === 0 || 
      (monitor.tags && monitor.tags.some((tag) => selectedTags.has(tag)));
    return matchesSearch && matchesType && matchesStatus && matchesTags;
  });

  const selectedMonitor = monitors.find((m) => m.id === selectedMonitorId) || null;
  const selectedMonitors = monitors.filter((m) => selectedMonitorIds.has(m.id));
  const allFilteredSelected = filteredMonitors.length > 0 && filteredMonitors.every((m) => selectedMonitorIds.has(m.id));
  const canDeleteSelected = selectedMonitorIds.size > 0;
  const canCreateGroup = selectedMonitorIds.size > 1 && groupNameInput.trim().length > 0;

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
    if (!confirm(`Delete ${selectedMonitors.length} selected monitor(s)?`)) return;
    try {
      await Promise.all(selectedMonitors.map((m) => deleteMonitor(m.id)));
      setMonitors(monitors.filter((m) => !selectedMonitorIds.has(m.id)));
      setSelectedMonitorIds(new Set());
      setToast({ message: 'Selected monitors deleted', type: 'success' });
    } catch (err: any) {
      setToast({ message: err.message || 'Failed to delete selected monitors', type: 'error' });
    }
  };

  const handleCreateGroup = async () => {
    const trimmedName = groupNameInput.trim();
    if (!trimmedName) {
      setToast({ message: 'Group name is required', type: 'error' });
      return;
    }
    if (selectedMonitors.length < 2) {
      setToast({ message: 'Select at least two monitors to create a group', type: 'error' });
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
      setToast({ message: 'Group created', type: 'success' });
    } catch (err: any) {
      setToast({ message: err.message || 'Failed to create group', type: 'error' });
    }
  };

  // Stats
  const totalUp = monitors.filter((m) => getLatestStatus(checkResultsMap[m.id] || []) === 'up').length;
  const totalDown = monitors.filter((m) => getLatestStatus(checkResultsMap[m.id] || []) !== 'up').length;

  return (
    <div className="space-y-5">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-semibold text-white">Monitors</h1>
          <p className="text-xs text-slate-500 mt-0.5">
            {monitors.length} monitors · {totalUp} up · {totalDown} down
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Link href="/monitors/import">
            <button className="btn btn-secondary btn-sm">
              <svg className="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-8l-4-4m0 0L8 8m4-4v12" />
              </svg>
              Import
            </button>
          </Link>
          <Link href="/monitors/new">
            <button className="btn btn-primary btn-sm">
              <svg className="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
              </svg>
              New Monitor
            </button>
          </Link>
        </div>
      </div>

      {/* Filters */}
      <div className="space-y-3">
        <div className="flex flex-wrap items-center gap-3">
          <div className="relative flex-1 max-w-xs">
            <svg className="absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-slate-500" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" />
            </svg>
            <input
              type="text"
              placeholder="Search..."
              value={searchTerm}
              onChange={(e) => setSearchTerm(e.target.value)}
              className="w-full rounded-lg border border-white/[0.06] bg-slate-900/50 py-1.5 pl-8 pr-3 text-xs text-white placeholder-slate-500 outline-none focus:border-cyan-500/50"
            />
          </div>

          <div className="flex items-center gap-0.5 rounded-lg border border-white/[0.06] bg-slate-900/50 p-0.5">
            {['all', 'http', 'ping', 'dns', 'agent', 'group', 'push', 'sip', 'synthetic_api', 'synthetic_browser'].map((type) => (
              <button
                key={type}
                onClick={() => setTypeFilter(type)}
                className={`rounded-md px-2 py-1 text-[10px] font-medium transition-colors ${
                  typeFilter === type ? 'bg-white/[0.08] text-white' : 'text-slate-400 hover:text-white'
                }`}
              >
                {type === 'all'
                  ? 'All'
                  : type === 'synthetic_api'
                    ? 'SYN API'
                    : type === 'synthetic_browser'
                      ? 'SYN BROWSER'
                      : type.toUpperCase()}
              </button>
            ))}
          </div>

          <div className="flex items-center gap-0.5 rounded-lg border border-white/[0.06] bg-slate-900/50 p-0.5">
            {['all', 'up', 'down'].map((status) => (
              <button
                key={status}
                onClick={() => setStatusFilter(status)}
                className={`rounded-md px-2 py-1 text-[10px] font-medium transition-colors ${
                  statusFilter === status ? 'bg-white/[0.08] text-white' : 'text-slate-400 hover:text-white'
                }`}
              >
                {status === 'all' ? 'All' : status === 'up' ? 'Up' : 'Down'}
              </button>
            ))}
          </div>

          {/* Tag Filter Toggle */}
          {allTags.length > 0 && (
            <button
              onClick={() => setShowTagFilter(!showTagFilter)}
              className={`inline-flex items-center gap-1.5 rounded-lg border px-2.5 py-1.5 text-[10px] font-medium transition-all ${
                selectedTags.size > 0 
                  ? 'border-cyan-500/40 bg-cyan-500/10 text-cyan-400' 
                  : 'border-white/[0.06] bg-slate-900/50 text-slate-400 hover:text-white'
              }`}
            >
              <svg className="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M7 7h.01M7 3h5c.512 0 1.024.195 1.414.586l7 7a2 2 0 010 2.828l-7 7a2 2 0 01-2.828 0l-7-7A1.994 1.994 0 013 12V7a4 4 0 014-4z" />
              </svg>
              Tags
              {selectedTags.size > 0 && (
                <span className="rounded-full bg-cyan-500/20 px-1.5 py-0.5 text-[9px]">
                  {selectedTags.size}
                </span>
              )}
              <svg className={`h-3 w-3 transition-transform ${showTagFilter ? 'rotate-180' : ''}`} fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 9l-7 7-7-7" />
              </svg>
            </button>
          )}
        </div>

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
          <button onClick={loadMonitors} className="btn btn-danger btn-xs">Retry</button>
        </div>
      )}

      {/* Content */}
      {loading ? (
        <LoadingSkeleton />
      ) : monitors.length === 0 ? (
        <EmptyState />
      ) : (
        <div className="grid gap-5 lg:grid-cols-[1fr_320px]">
          {/* Monitor List */}
          <div className="space-y-2">
            {/* Selection Mode Toggle & Actions Bar */}
            {filteredMonitors.length > 0 && (
              <div className="flex items-center gap-3">
                {/* Selection mode toggle */}
                <button
                  onClick={() => {
                    if (selectionMode) {
                      clearSelection();
                    } else {
                      setSelectionMode(true);
                    }
                  }}
                  className={`inline-flex items-center gap-1.5 rounded-lg border px-2.5 py-1.5 text-[10px] font-medium transition-all ${
                    selectionMode
                      ? 'border-cyan-500/40 bg-cyan-500/10 text-cyan-400'
                      : 'border-white/[0.06] bg-slate-900/50 text-slate-400 hover:text-white'
                  }`}
                >
                  <svg className="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 5H7a2 2 0 00-2 2v12a2 2 0 002 2h10a2 2 0 002-2V7a2 2 0 00-2-2h-2M9 5a2 2 0 002 2h2a2 2 0 002-2M9 5a2 2 0 012-2h2a2 2 0 012 2" />
                  </svg>
                  {selectionMode ? 'Exit select' : 'Select'}
                </button>

                {/* Selection actions - only show when in selection mode */}
                {selectionMode && (
                  <>
                    <div className="h-4 w-px bg-white/[0.08]" />
                    
                    {/* Select all toggle */}
                    <button
                      onClick={toggleSelectAllFiltered}
                      className="btn btn-outline btn-xs"
                    >
                      {allFilteredSelected ? 'Deselect all' : 'Select all'}
                    </button>

                    {selectedMonitorIds.size > 0 && (
                      <>
                        <span className="text-[10px] text-slate-500">
                          {selectedMonitorIds.size} selected
                        </span>
                        
                        <div className="h-4 w-px bg-white/[0.08]" />

                        {/* Group creation */}
                        <div className="flex items-center gap-1.5">
                          <input
                            type="text"
                            placeholder="New group name..."
                            value={groupNameInput}
                            onChange={(e) => setGroupNameInput(e.target.value)}
                            className="input input-xs h-6 w-32 text-[10px]"
                          />
                          <button
                            onClick={handleCreateGroup}
                            disabled={!canCreateGroup}
                            className={`btn btn-secondary btn-xs ${canCreateGroup ? '' : 'cursor-not-allowed opacity-60'}`}
                          >
                            <svg className="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 11H5m14 0a2 2 0 012 2v6a2 2 0 01-2 2H5a2 2 0 01-2-2v-6a2 2 0 012-2m14 0V9a2 2 0 00-2-2M5 11V9a2 2 0 012-2m0 0V5a2 2 0 012-2h6a2 2 0 012 2v2M7 7h10" />
                            </svg>
                            Group
                          </button>
                        </div>

                        <div className="h-4 w-px bg-white/[0.08]" />

                        {/* Delete */}
                        <button
                          onClick={handleBulkDelete}
                          className="btn btn-danger btn-xs"
                        >
                          <svg className="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
                          </svg>
                          Delete
                        </button>
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
                    memberResults={checkResultsMap}
                    isSelected={monitor.id === selectedMonitorId}
                    isChecked={selectedMonitorIds.has(monitor.id)}
                    onToggleSelect={() => toggleMonitorSelection(monitor.id)}
                    selectionMode={selectionMode}
                    onClick={() => setSelectedMonitorId(monitor.id)}
                    onDelete={() => handleDelete(monitor.id)}
                    onSelectMember={(id) => setSelectedMonitorId(id)}
                    selectedMonitorId={selectedMonitorId}
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
                    onClick={() => setSelectedMonitorId(monitor.id)}
                    onDelete={() => handleDelete(monitor.id)}
                  />
                )
              ))
            )}
          </div>

          {/* Detail Panel */}
          <div className="hidden lg:block">
            <div className="sticky top-6">
              <DetailPanel
                monitor={selectedMonitor}
                results={selectedMonitor ? checkResultsMap[selectedMonitor.id] || [] : []}
              />
            </div>
          </div>
        </div>
      )}

      {/* Toast */}
      {toast && (
        <div className={`fixed bottom-4 right-4 rounded-lg px-3 py-2 shadow-lg text-xs ${
          toast.type === 'success' ? 'bg-emerald-500' : 'bg-rose-500'
        }`}>
          <div className="flex items-center gap-2">
            <span className="text-white">{toast.message}</span>
            <button onClick={() => setToast(null)} className="text-white/80 hover:text-white">×</button>
          </div>
        </div>
      )}
    </div>
  );
}
