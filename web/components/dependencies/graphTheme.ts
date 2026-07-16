import { MarkerType, type Edge } from '@xyflow/react';
import {
  Code,
  Database,
  Folder,
  Globe,
  Leaf,
  MessageSquare,
  MousePointer2,
  Network,
  Phone,
  Radio,
  Search,
  Server,
  Webhook,
  Zap,
  type LucideIcon,
} from 'lucide-react';
import { MonitorState } from '@/lib/types';

export const NODE_WIDTH = 216;
export const NODE_HEIGHT = 64;
export const GROUP_NODE_HEIGHT = 52;

export function apiErrorMessage(err: unknown): string {
  if (err && typeof err === 'object' && 'message' in err) {
    return String((err as { message?: unknown }).message || 'Something went wrong');
  }
  return err instanceof Error ? err.message : 'Something went wrong';
}

// Same icon-per-type vocabulary as MONITOR_TYPE_META in MonitorForm, so the
// map reads like the rest of the app.
export const TYPE_ICONS: Record<string, LucideIcon> = {
  http: Globe,
  synthetic_api: Code,
  synthetic_browser: MousePointer2,
  ping: Radio,
  dns: Search,
  grpc: Network,
  sip: Phone,
  postgres: Database,
  redis: Zap,
  mongodb: Leaf,
  rabbitmq: MessageSquare,
  agent: Server,
  push: Webhook,
  group: Folder,
};

// Per-state visual treatment for node cards: accent bar, icon tile, dot,
// state label, and the halo a failing node casts on the map.
export const STATE_STYLES: Record<string, {
  accent: string;
  iconTile: string;
  dot: string;
  text: string;
  card: string;
}> = {
  up: {
    accent: 'bg-emerald-500/80',
    iconTile: 'bg-emerald-500/10 text-emerald-400',
    dot: 'bg-emerald-500',
    text: 'text-emerald-400',
    card: 'border-white/[0.08]',
  },
  suspect: {
    accent: 'bg-amber-400',
    iconTile: 'bg-amber-500/10 text-amber-400',
    dot: 'bg-amber-400',
    text: 'text-amber-400',
    card: 'border-amber-500/40 shadow-[0_0_24px_-6px_rgba(251,191,36,0.35)]',
  },
  down: {
    accent: 'bg-rose-500',
    iconTile: 'bg-rose-500/10 text-rose-400',
    dot: 'bg-rose-500',
    text: 'text-rose-400',
    card: 'border-rose-500/50 shadow-[0_0_28px_-4px_rgba(244,63,94,0.45)]',
  },
  degraded: {
    accent: 'bg-amber-400',
    iconTile: 'bg-amber-500/10 text-amber-400',
    dot: 'bg-amber-400',
    text: 'text-amber-400',
    card: 'border-amber-500/40 shadow-[0_0_24px_-6px_rgba(251,191,36,0.35)]',
  },
  unknown: {
    accent: 'bg-slate-600',
    iconTile: 'bg-slate-500/10 text-slate-400',
    dot: 'bg-slate-500',
    text: 'text-slate-400',
    card: 'border-white/[0.06]',
  },
};

export function stateStyle(state?: MonitorState) {
  return STATE_STYLES[state || 'unknown'] ?? STATE_STYLES.unknown;
}

export const EDGE_COLORS: Record<string, string> = {
  down: '#fb7185',
  suspect: '#fbbf24',
  degraded: '#fbbf24',
  default: '#475569',
};

export function styleEdge(from: string, to: string, upstreamState?: MonitorState): Edge {
  const tone =
    upstreamState === 'down'
      ? 'down'
      : upstreamState === 'suspect' || upstreamState === 'degraded'
        ? 'suspect'
        : 'default';
  const color = EDGE_COLORS[tone];
  return {
    id: `${from}->${to}`,
    source: from,
    target: to,
    type: 'dependency',
    animated: tone === 'down',
    interactionWidth: 20,
    style: {
      stroke: color,
      strokeWidth: tone === 'default' ? 1.5 : 2,
      ...(tone === 'down' ? { filter: 'drop-shadow(0 0 4px rgba(251,113,133,0.55))' } : {}),
    },
    markerEnd: { type: MarkerType.ArrowClosed, color, width: 15, height: 15 },
  };
}

export const LEGEND: Array<{ state: MonitorState; label: string }> = [
  { state: 'up', label: 'Up' },
  { state: 'suspect', label: 'Suspect' },
  { state: 'degraded', label: 'Degraded' },
  { state: 'down', label: 'Down' },
  { state: 'unknown', label: 'Unknown' },
];
