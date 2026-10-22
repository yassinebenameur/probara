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

// Per-state visual treatment for node cards: icon tile, dot, state label,
// and the border a failing node wears on the map.
export const STATE_STYLES: Record<string, {
  iconTile: string;
  dot: string;
  text: string;
  card: string;
}> = {
  up: {
    iconTile: 'bg-emerald-500/[0.12] text-emerald-400',
    dot: 'bg-emerald-500',
    text: 'text-emerald-300',
    card: 'border-edge',
  },
  suspect: {
    iconTile: 'bg-amber-500/[0.14] text-amber-400',
    dot: 'bg-amber-500',
    text: 'text-amber-300',
    card: 'border-amber-500/50',
  },
  down: {
    iconTile: 'bg-rose-500/[0.14] text-rose-400',
    dot: 'bg-rose-500',
    text: 'text-rose-300',
    card: 'border-rose-500/60',
  },
  degraded: {
    iconTile: 'bg-amber-500/[0.14] text-amber-400',
    dot: 'bg-amber-500',
    text: 'text-amber-300',
    card: 'border-amber-500/50',
  },
  unknown: {
    iconTile: 'bg-well-hover text-slate-400',
    dot: 'bg-slate-500',
    text: 'text-slate-400',
    card: 'border-edge',
  },
};

export function stateStyle(state?: MonitorState) {
  return STATE_STYLES[state || 'unknown'] ?? STATE_STYLES.unknown;
}

// Literal colours, not CSS variables: React Flow bakes a marker's colour into
// its SVG id, which a var() reference would break. So each is a mid-tone
// that clears 3:1 on the graph's panel in BOTH themes (white and #13161d):
// down 4.4 / 4.1, suspect 3.3 / 5.5, up 3.3 / 5.4, default 3.9 / 4.7.
export const EDGE_COLORS: Record<string, string> = {
  down: '#dc3a4b',
  suspect: '#b98317',
  degraded: '#b98317',
  up: '#2ba05d',
  default: '#79828f',
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
    },
    markerEnd: { type: MarkerType.ArrowClosed, color, width: 15, height: 15 },
  };
}

// Suspect and degraded share one colour on nodes and edges alike, so the
// legend names them as one entry rather than two identical swatches.
export const LEGEND: Array<{ state: MonitorState; label: string }> = [
  { state: 'up', label: 'Up' },
  { state: 'suspect', label: 'Suspect or degraded' },
  { state: 'down', label: 'Down' },
  { state: 'unknown', label: 'Unknown' },
];
