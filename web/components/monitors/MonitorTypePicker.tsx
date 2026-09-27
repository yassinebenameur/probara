'use client';

import { useState } from 'react';
import { Globe, Radio, Search, Folder, Server, Webhook, Phone, Network, Code, MousePointer2, Lock, Database, Leaf, Zap, MessageSquare, PlugZap, Cylinder, Cable, type LucideIcon } from 'lucide-react';
import type { MonitorType } from '@/lib/types';
import { BTN_GHOST_SM } from './MonitorFormControls';

// Monitor type registry for the UI. The picker, categories, search, and name
// placeholder all derive from it; MonitorForm dispatches each type to its
// editor (an inline editor, DatabaseForm, or DELEGATED_FORMS).
export type MonitorTypeMeta = {
  type: MonitorType;
  label: string;
  description: string;
  icon: LucideIcon;
  category: (typeof MONITOR_TYPE_CATEGORIES)[number];
  namePlaceholder: string;
};

const MONITOR_TYPE_CATEGORIES = ['Web & API', 'Network', 'Databases & Brokers', 'Infrastructure', 'Organization'] as const;

export const MONITOR_TYPE_META: MonitorTypeMeta[] = [
  { type: 'http', label: 'HTTP', description: 'Check an HTTP endpoint', icon: Globe, category: 'Web & API', namePlaceholder: 'My API health check' },
  { type: 'websocket', label: 'WebSocket', description: 'Upgrade handshake and reply checks', icon: Cable, category: 'Web & API', namePlaceholder: 'Live updates socket' },
  { type: 'synthetic_api', label: 'Synthetic API', description: 'Multi-step API journey', icon: Code, category: 'Web & API', namePlaceholder: 'Checkout API journey' },
  { type: 'synthetic_browser', label: 'Synthetic Browser', description: 'Real-browser user flow', icon: MousePointer2, category: 'Web & API', namePlaceholder: 'Login flow check' },
  { type: 'ping', label: 'Ping', description: 'ICMP reachability check', icon: Radio, category: 'Network', namePlaceholder: 'Edge gateway ping' },
  { type: 'dns', label: 'DNS', description: 'Resolve and verify records', icon: Search, category: 'Network', namePlaceholder: 'example.com DNS' },
  { type: 'grpc', label: 'gRPC', description: 'gRPC health checks', icon: Network, category: 'Network', namePlaceholder: 'My gRPC service' },
  { type: 'tcp', label: 'TCP', description: 'Connect to a host and port', icon: PlugZap, category: 'Network', namePlaceholder: 'Postgres port reachability' },
  { type: 'sip', label: 'SIP', description: 'OPTIONS ping and REGISTER auth probes', icon: Phone, category: 'Network', namePlaceholder: 'My SIP server' },
  { type: 'postgres', label: 'PostgreSQL', description: 'Connect, auth, and query checks', icon: Database, category: 'Databases & Brokers', namePlaceholder: 'Postgres production' },
  { type: 'mysql', label: 'MySQL', description: 'Connect, auth, and query checks', icon: Cylinder, category: 'Databases & Brokers', namePlaceholder: 'MySQL production' },
  { type: 'redis', label: 'Redis', description: 'Connect and PING latency', icon: Zap, category: 'Databases & Brokers', namePlaceholder: 'Redis cache' },
  { type: 'mongodb', label: 'MongoDB', description: 'Connect, auth, and ping', icon: Leaf, category: 'Databases & Brokers', namePlaceholder: 'Mongo cluster' },
  { type: 'prometheus', label: 'Prometheus', description: 'PromQL queries with numeric thresholds', icon: Radio, category: 'Infrastructure', namePlaceholder: 'API error rate' },
  { type: 'rabbitmq', label: 'RabbitMQ', description: 'AMQP connect and auth checks', icon: MessageSquare, category: 'Databases & Brokers', namePlaceholder: 'RabbitMQ production' },
  { type: 'agent', label: 'Agent', description: 'Host metrics from an agent', icon: Server, category: 'Infrastructure', namePlaceholder: 'Production server' },
  { type: 'push', label: 'Push', description: 'Heartbeat sent by your service', icon: Webhook, category: 'Infrastructure', namePlaceholder: 'My service health' },
  { type: 'group', label: 'Group', description: 'Roll up monitors into one status', icon: Folder, category: 'Organization', namePlaceholder: 'Production services' },
];

export const getTypeMeta = (type: MonitorType): MonitorTypeMeta =>
  MONITOR_TYPE_META.find((m) => m.type === type) ?? MONITOR_TYPE_META[0];

export default function MonitorTypePicker({
  value,
  onSelect,
  open,
  onOpenChange,
  locked,
}: {
  value: MonitorType;
  onSelect: (type: MonitorType) => void;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  locked: boolean;
}) {
  const [query, setQuery] = useState('');
  const selected = getTypeMeta(value);
  const SelectedIcon = selected.icon;
  const q = query.trim().toLowerCase();
  const matches = (m: MonitorTypeMeta) =>
    !q ||
    m.label.toLowerCase().includes(q) ||
    m.description.toLowerCase().includes(q) ||
    m.category.toLowerCase().includes(q);
  const visible = MONITOR_TYPE_META.filter(matches);

  if (locked || !open) {
    return (
      <div className="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-white/[0.06] bg-slate-900/40 px-4 py-3">
        <div className="flex min-w-0 items-center gap-3">
          <div className="shrink-0 rounded-lg bg-cyan-500/15 p-2 text-cyan-400">
            <SelectedIcon className="h-4 w-4" strokeWidth={1.75} />
          </div>
          <div className="min-w-0">
            <p className="text-sm font-medium text-white">{selected.label} monitor</p>
            <p className="truncate text-xs text-slate-500">{selected.description}</p>
          </div>
        </div>
        {locked ? (
          <span className="flex shrink-0 items-center gap-1.5 text-xs text-slate-500">
            <Lock className="h-3.5 w-3.5" strokeWidth={1.75} />
            Type is fixed after creation
          </span>
        ) : (
          <button type="button" onClick={() => onOpenChange(true)} className={BTN_GHOST_SM}>
            Change type
          </button>
        )}
      </div>
    );
  }

  return (
    <div className="rounded-xl border border-white/[0.06] bg-slate-900/40 p-4">
      <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
        <div>
          <p className="text-sm font-medium text-white">Monitor type</p>
          <p className="text-xs text-slate-500">Pick what you want to watch. The type can&apos;t be changed later.</p>
        </div>
        <div className="relative">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-slate-500" strokeWidth={1.75} />
          <input
            type="text"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search types…"
            aria-label="Search monitor types"
            className="h-8 w-48 rounded-lg border border-white/[0.08] bg-slate-950/60 pl-8 pr-3 text-xs text-slate-200 placeholder:text-slate-600 focus:border-cyan-500/40 focus:outline-none"
          />
        </div>
      </div>
      <div className="space-y-4" role="radiogroup" aria-label="Monitor type">
        {MONITOR_TYPE_CATEGORIES.map((category) => {
          const items = visible.filter((m) => m.category === category);
          if (items.length === 0) return null;
          return (
            <div key={category}>
              <p className="mb-2 text-[10px] font-semibold uppercase tracking-widest text-slate-600">{category}</p>
              <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
                {items.map((m) => {
                  const Icon = m.icon;
                  const isSelected = m.type === value;
                  return (
                    <button
                      key={m.type}
                      type="button"
                      role="radio"
                      aria-checked={isSelected}
                      onClick={() => onSelect(m.type)}
                      className={`flex items-start gap-2.5 rounded-lg border p-2.5 text-left transition-all ${
                        isSelected
                          ? 'border-cyan-500/50 bg-cyan-500/10'
                          : 'border-white/[0.06] bg-slate-800/30 hover:border-white/[0.12] hover:bg-slate-800/60'
                      }`}
                    >
                      <div className={`shrink-0 rounded-lg p-1.5 ${isSelected ? 'bg-cyan-500/20 text-cyan-400' : 'bg-slate-700/50 text-slate-400'}`}>
                        <Icon className="h-4 w-4" strokeWidth={1.75} />
                      </div>
                      <div className="min-w-0">
                        <p className={`text-xs font-medium ${isSelected ? 'text-white' : 'text-slate-300'}`}>{m.label}</p>
                        <p className="mt-0.5 text-[11px] leading-snug text-slate-500">{m.description}</p>
                      </div>
                    </button>
                  );
                })}
              </div>
            </div>
          );
        })}
        {visible.length === 0 && (
          <p className="py-2 text-center text-xs text-slate-500">No monitor types match &ldquo;{query}&rdquo;.</p>
        )}
      </div>
    </div>
  );
}
