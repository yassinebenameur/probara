'use client';

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useRouter } from 'next/navigation';
import dagre from 'dagre';
import {
  applyEdgeChanges,
  applyNodeChanges,
  Background,
  BackgroundVariant,
  BaseEdge,
  Controls,
  EdgeLabelRenderer,
  getSmoothStepPath,
  Handle,
  MarkerType,
  MiniMap,
  Position,
  ReactFlow,
  ReactFlowProvider,
  useConnection,
  useReactFlow,
  type Connection,
  type Edge,
  type EdgeChange,
  type EdgeProps,
  type Node,
  type NodeChange,
  type NodeProps,
} from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import {
  Code,
  Database,
  Folder,
  Globe,
  LayoutGrid,
  Leaf,
  MessageSquare,
  MousePointer2,
  Network,
  Phone,
  Plus,
  Radio,
  Search,
  Server,
  Webhook,
  Workflow,
  X,
  Zap,
  type LucideIcon,
} from 'lucide-react';
import { DependencyGraph, DependencyMonitor, MonitorState } from '@/lib/types';
import {
  addMonitorDependency,
  getDependencyGraph,
  getMonitors,
  removeMonitorDependency,
} from '@/lib/api';
import { useToast } from '@/components/ui/ToastProvider';

const NODE_WIDTH = 216;
const NODE_HEIGHT = 64;

export function apiErrorMessage(err: unknown): string {
  if (err && typeof err === 'object' && 'message' in err) {
    return String((err as { message?: unknown }).message || 'Something went wrong');
  }
  return err instanceof Error ? err.message : 'Something went wrong';
}

// Same icon-per-type vocabulary as MONITOR_TYPE_META in MonitorForm, so the
// map reads like the rest of the app.
const TYPE_ICONS: Record<string, LucideIcon> = {
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
const STATE_STYLES: Record<string, {
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
  unknown: {
    accent: 'bg-slate-600',
    iconTile: 'bg-slate-500/10 text-slate-400',
    dot: 'bg-slate-500',
    text: 'text-slate-400',
    card: 'border-white/[0.06]',
  },
};

function stateStyle(state?: MonitorState) {
  return STATE_STYLES[state || 'unknown'] ?? STATE_STYLES.unknown;
}

const EDGE_COLORS: Record<string, string> = {
  down: '#fb7185',
  suspect: '#fbbf24',
  default: '#475569',
};

type MonitorNodeData = DependencyMonitor & {
  dimmed?: boolean;
  highlighted?: boolean;
} & Record<string, unknown>;
type MonitorFlowNode = Node<MonitorNodeData, 'monitor'>;

function MonitorNode({ id, data }: NodeProps<MonitorFlowNode>) {
  const s = stateStyle(data.current_state);
  const Icon = TYPE_ICONS[data.type] ?? Workflow;
  const failing = data.current_state === 'down' || data.current_state === 'suspect';
  // While a connection is being dragged from another node, the whole card
  // becomes the drop target — dropping anywhere on it completes the link.
  const connection = useConnection();
  const isDropTarget = connection.inProgress && connection.fromNode?.id !== id;

  return (
    <div
      className={[
        'group relative w-[216px] rounded-xl border',
        'bg-gradient-to-br from-slate-900/95 to-slate-950/95 backdrop-blur',
        'transition-all duration-200 hover:-translate-y-px hover:border-cyan-400/40 hover:shadow-[0_0_20px_-6px_rgba(34,211,238,0.4)]',
        s.card,
        data.dimmed ? 'opacity-20' : 'opacity-100',
        data.highlighted ? 'border-cyan-400/70 shadow-[0_0_24px_-4px_rgba(34,211,238,0.55)]' : '',
        isDropTarget ? 'border-cyan-400/60 shadow-[0_0_20px_-4px_rgba(34,211,238,0.5)]' : '',
      ].join(' ')}
      style={{ transitionProperty: 'opacity, transform, border-color, box-shadow' }}
    >
      <span className="pointer-events-none absolute inset-0 overflow-hidden rounded-xl">
        <span className={`absolute inset-y-0 left-0 w-[3px] ${s.accent}`} />
      </span>
      {isDropTarget ? (
        <Handle
          type="target"
          position={Position.Left}
          className="!absolute !inset-0 !h-full !w-full !transform-none !rounded-xl !border-0 !bg-transparent"
        />
      ) : (
        <Handle
          type="target"
          position={Position.Left}
          className="!h-2.5 !w-2.5 !border-2 !border-slate-700 !bg-slate-900 transition-colors group-hover:!border-cyan-400/70"
        />
      )}
      <div className="flex cursor-pointer items-center gap-2.5 py-2.5 pl-3.5 pr-3">
        <span className={`flex h-8 w-8 shrink-0 items-center justify-center rounded-lg ${s.iconTile}`}>
          <Icon className="h-4 w-4" strokeWidth={1.75} />
        </span>
        <div className="min-w-0 flex-1">
          <p className="truncate text-[13px] font-medium leading-tight text-white" title={data.name}>
            {data.name}
          </p>
          <p className="mt-0.5 flex items-center gap-1.5 text-[10px] uppercase tracking-wider text-slate-500">
            {data.type}
            <span className="relative flex h-1.5 w-1.5">
              {failing && (
                <span className={`absolute inline-flex h-full w-full animate-ping rounded-full ${s.dot} opacity-60`} />
              )}
              <span className={`relative inline-flex h-1.5 w-1.5 rounded-full ${s.dot}`} />
            </span>
            <span className={s.text}>{data.current_state || 'unknown'}</span>
          </p>
        </div>
      </div>
      <Handle
        type="source"
        position={Position.Right}
        className="!h-2.5 !w-2.5 !border-2 !border-slate-700 !bg-slate-900 transition-colors group-hover:!border-cyan-400/70 group-hover:!bg-cyan-500/30"
      />
    </div>
  );
}

// Edge with an unlink button at its midpoint when selected. Deleting goes
// through deleteElements so onEdgesDelete (the API call) fires either way.
function DependencyEdge({
  id,
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition,
  targetPosition,
  style,
  markerEnd,
  selected,
}: EdgeProps) {
  const { deleteElements } = useReactFlow();
  const [path, labelX, labelY] = getSmoothStepPath({
    sourceX,
    sourceY,
    sourcePosition,
    targetX,
    targetY,
    targetPosition,
  });

  return (
    <>
      <BaseEdge id={id} path={path} style={style} markerEnd={markerEnd as string | undefined} />
      {selected && (
        <EdgeLabelRenderer>
          <button
            type="button"
            title="Unlink dependency"
            onClick={(e) => {
              e.stopPropagation();
              deleteElements({ edges: [{ id }] });
            }}
            className="nodrag nopan absolute z-10 flex h-6 w-6 items-center justify-center rounded-full border border-rose-500/50 bg-slate-900 text-rose-400 shadow-lg transition-colors hover:bg-rose-500/20"
            style={{
              transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)`,
              pointerEvents: 'all',
            }}
          >
            <X className="h-3.5 w-3.5" strokeWidth={2} />
          </button>
        </EdgeLabelRenderer>
      )}
    </>
  );
}

const nodeTypes = { monitor: MonitorNode };
const edgeTypes = { dependency: DependencyEdge };

function styleEdge(from: string, to: string, upstreamState?: MonitorState): Edge {
  const tone = upstreamState === 'down' ? 'down' : upstreamState === 'suspect' ? 'suspect' : 'default';
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

// Deterministic layered left-to-right layout: downstream services on the
// left, the things they depend on to their right.
function layoutGraph(graph: DependencyGraph): { nodes: MonitorFlowNode[]; edges: Edge[] } {
  const nodeIds = new Set(graph.nodes.map((n) => n.id));
  // Drop self-edges and edges referencing missing nodes defensively.
  const validEdges = graph.edges.filter(
    (e) => e.from !== e.to && nodeIds.has(e.from) && nodeIds.has(e.to)
  );

  const g = new dagre.graphlib.Graph();
  g.setGraph({ rankdir: 'LR', nodesep: 36, ranksep: 110 });
  g.setDefaultEdgeLabel(() => ({}));

  for (const node of graph.nodes) {
    g.setNode(node.id, { width: NODE_WIDTH, height: NODE_HEIGHT });
  }
  for (const edge of validEdges) {
    g.setEdge(edge.from, edge.to);
  }
  dagre.layout(g);

  const stateOf = new Map(graph.nodes.map((n) => [n.id, n.current_state]));

  const nodes: MonitorFlowNode[] = graph.nodes.map((node) => {
    const pos = g.node(node.id);
    return {
      id: node.id,
      type: 'monitor',
      position: { x: pos.x - NODE_WIDTH / 2, y: pos.y - NODE_HEIGHT / 2 },
      data: node as MonitorNodeData,
    };
  });

  const edges: Edge[] = validEdges.map((edge) => styleEdge(edge.from, edge.to, stateOf.get(edge.to)));

  return { nodes, edges };
}

const LEGEND: Array<{ state: MonitorState; label: string }> = [
  { state: 'up', label: 'Up' },
  { state: 'suspect', label: 'Suspect' },
  { state: 'down', label: 'Down' },
  { state: 'unknown', label: 'Unknown' },
];

// Toolbar popover: search any monitor and drop it onto the canvas so it can
// be wired up, even if it has no dependencies yet.
function AddMonitorPicker({
  existingIds,
  onAdd,
}: {
  existingIds: Set<string>;
  onAdd: (monitor: DependencyMonitor) => void;
}) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const [allMonitors, setAllMonitors] = useState<DependencyMonitor[] | null>(null);
  const rootRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open || allMonitors !== null) return;
    getMonitors({ page_size: 100 })
      .then((res) =>
        setAllMonitors(
          (res.items || []).map((m) => ({
            id: m.id,
            name: m.name,
            type: m.type,
            current_state: (m.current_state || 'unknown') as MonitorState,
          }))
        )
      )
      .catch(() => setAllMonitors([]));
  }, [open, allMonitors]);

  useEffect(() => {
    if (!open) return;
    const onClickAway = (e: MouseEvent) => {
      if (rootRef.current && !rootRef.current.contains(e.target as globalThis.Node)) setOpen(false);
    };
    document.addEventListener('mousedown', onClickAway);
    return () => document.removeEventListener('mousedown', onClickAway);
  }, [open]);

  const candidates = useMemo(() => {
    if (!allMonitors) return [];
    const q = query.trim().toLowerCase();
    return allMonitors
      .filter((m) => !existingIds.has(m.id))
      .filter((m) => !q || m.name.toLowerCase().includes(q) || m.type.toLowerCase().includes(q))
      .slice(0, 8);
  }, [allMonitors, existingIds, query]);

  return (
    <div className="relative" ref={rootRef}>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex items-center gap-1.5 rounded-lg border border-cyan-500/30 bg-cyan-500/10 px-2.5 py-1.5 text-xs font-medium text-cyan-400 transition-colors hover:border-cyan-500/50 hover:bg-cyan-500/20"
      >
        <Plus className="h-3.5 w-3.5" strokeWidth={2} />
        Add monitor
      </button>
      {open && (
        <div className="absolute right-0 top-full z-20 mt-2 w-72 rounded-xl border border-white/[0.08] bg-slate-900 p-2 shadow-2xl">
          <input
            type="text"
            autoFocus
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => e.key === 'Escape' && setOpen(false)}
            placeholder="Search monitors…"
            className="mb-1.5 w-full rounded-lg border border-white/[0.08] bg-slate-950/80 px-2.5 py-1.5 text-xs text-white placeholder:text-slate-600 focus:border-cyan-500/50 focus:outline-none"
          />
          <div className="max-h-64 space-y-0.5 overflow-y-auto">
            {allMonitors === null ? (
              <p className="px-2 py-3 text-center text-xs text-slate-500">Loading…</p>
            ) : candidates.length === 0 ? (
              <p className="px-2 py-3 text-center text-xs text-slate-500">
                {query ? 'No monitors match' : 'Everything is already on the canvas'}
              </p>
            ) : (
              candidates.map((m) => {
                const Icon = TYPE_ICONS[m.type] ?? Workflow;
                const s = stateStyle(m.current_state);
                return (
                  <button
                    key={m.id}
                    type="button"
                    onClick={() => {
                      onAdd(m);
                      setOpen(false);
                      setQuery('');
                    }}
                    className="flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left transition-colors hover:bg-white/[0.05]"
                  >
                    <span className={`flex h-6 w-6 shrink-0 items-center justify-center rounded-md ${s.iconTile}`}>
                      <Icon className="h-3.5 w-3.5" strokeWidth={1.75} />
                    </span>
                    <span className="min-w-0 flex-1 truncate text-xs text-slate-200">{m.name}</span>
                    <span className={`h-1.5 w-1.5 shrink-0 rounded-full ${s.dot}`} />
                  </button>
                );
              })
            )}
          </div>
        </div>
      )}
    </div>
  );
}

function GraphCanvas({ graph }: { graph: DependencyGraph }) {
  const router = useRouter();
  const { fitView } = useReactFlow();
  const { showToast } = useToast();
  const [rfNodes, setRfNodes] = useState<MonitorFlowNode[]>([]);
  const [rfEdges, setRfEdges] = useState<Edge[]>([]);
  const [hoveredId, setHoveredId] = useState<string | null>(null);
  const [query, setQuery] = useState('');

  useEffect(() => {
    const { nodes, edges } = layoutGraph(graph);
    setRfNodes(nodes);
    setRfEdges(edges);
  }, [graph]);

  const stateOf = useMemo(
    () => new Map(rfNodes.map((n) => [n.id, n.data.current_state])),
    [rfNodes]
  );

  const onNodesChange = useCallback(
    (changes: NodeChange<MonitorFlowNode>[]) =>
      setRfNodes((ns) => applyNodeChanges(changes, ns)),
    []
  );
  const onEdgesChange = useCallback(
    (changes: EdgeChange[]) => setRfEdges((es) => applyEdgeChanges(changes, es)),
    []
  );

  const isValidConnection = useCallback(
    (conn: Connection | Edge) =>
      Boolean(conn.source && conn.target) &&
      conn.source !== conn.target &&
      !rfEdges.some((e) => e.source === conn.source && e.target === conn.target),
    [rfEdges]
  );

  // Drawing an edge = "source depends on target". Optimistic: the edge lands
  // immediately and snaps back if the API rejects it (e.g. a cycle).
  const onConnect = useCallback(
    (conn: Connection) => {
      if (!isValidConnection(conn)) return;
      const edge = styleEdge(conn.source, conn.target, stateOf.get(conn.target));
      setRfEdges((es) => [...es, edge]);
      addMonitorDependency(conn.source, conn.target)
        .then(() => showToast('Dependency linked', 'success', 2500))
        .catch((err) => {
          setRfEdges((es) => es.filter((e) => e.id !== edge.id));
          showToast(apiErrorMessage(err), 'error');
        });
    },
    [isValidConnection, stateOf, showToast]
  );

  // Fires for the edge ✕ button and the Delete/Backspace key alike.
  const onEdgesDelete = useCallback(
    (deleted: Edge[]) => {
      for (const edge of deleted) {
        removeMonitorDependency(edge.source, edge.target)
          .then(() => showToast('Dependency removed', 'success', 2500))
          .catch((err) => {
            setRfEdges((es) => (es.some((e) => e.id === edge.id) ? es : [...es, edge]));
            showToast(apiErrorMessage(err), 'error');
          });
      }
    },
    [showToast]
  );

  const addToCanvas = useCallback(
    (monitor: DependencyMonitor) => {
      setRfNodes((ns) => {
        if (ns.some((n) => n.id === monitor.id)) return ns;
        const minX = ns.length ? Math.min(...ns.map((n) => n.position.x)) : 0;
        const avgY = ns.length ? ns.reduce((sum, n) => sum + n.position.y, 0) / ns.length : 0;
        return [
          ...ns,
          {
            id: monitor.id,
            type: 'monitor' as const,
            position: { x: minX - NODE_WIDTH - 120, y: avgY },
            data: monitor as MonitorNodeData,
          },
        ];
      });
      window.requestAnimationFrame(() =>
        fitView({ padding: 0.2, duration: 500, maxZoom: 1.1 })
      );
    },
    [fitView]
  );

  const relayout = useCallback(() => {
    const current: DependencyGraph = {
      nodes: rfNodes.map((n) => ({
        id: n.id,
        name: n.data.name,
        type: n.data.type,
        current_state: n.data.current_state,
        last_state_change_at: n.data.last_state_change_at,
      })),
      edges: rfEdges.map((e) => ({ from: e.source, to: e.target })),
    };
    const { nodes } = layoutGraph(current);
    // Keep canvas-only nodes (no edges) where dagre put them too.
    setRfNodes(nodes);
    window.requestAnimationFrame(() => fitView({ padding: 0.2, duration: 500 }));
  }, [rfNodes, rfEdges, fitView]);

  const matches = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return null;
    return new Set(
      rfNodes
        .filter(
          (n) => n.data.name.toLowerCase().includes(q) || n.data.type.toLowerCase().includes(q)
        )
        .map((n) => n.id)
    );
  }, [rfNodes, query]);

  // Neighborhood of the hovered node: itself plus anything it shares an edge with.
  const hoodOf = useMemo(() => {
    if (!hoveredId) return null;
    const hood = new Set([hoveredId]);
    for (const e of rfEdges) {
      if (e.source === hoveredId) hood.add(e.target);
      if (e.target === hoveredId) hood.add(e.source);
    }
    return hood;
  }, [hoveredId, rfEdges]);

  const displayNodes = useMemo(
    () =>
      rfNodes.map((node) => {
        const dimmed =
          (hoodOf ? !hoodOf.has(node.id) : false) || (matches ? !matches.has(node.id) : false);
        const highlighted = matches ? matches.has(node.id) : false;
        return { ...node, data: { ...node.data, dimmed, highlighted } };
      }),
    [rfNodes, hoodOf, matches]
  );

  const displayEdges = useMemo(
    () =>
      rfEdges.map((edge) => {
        const inHood = hoodOf ? hoodOf.has(edge.source) && hoodOf.has(edge.target) : true;
        const inMatch = matches ? matches.has(edge.source) || matches.has(edge.target) : true;
        const faded = !inHood || !inMatch;
        return {
          ...edge,
          style: {
            ...edge.style,
            opacity: faded ? 0.12 : 1,
            transition: 'opacity 150ms ease',
          },
        };
      }),
    [rfEdges, hoodOf, matches]
  );

  const flyToMatches = useCallback(() => {
    if (!matches || matches.size === 0) return;
    fitView({
      nodes: Array.from(matches).map((id) => ({ id })),
      duration: 600,
      padding: 0.3,
      maxZoom: 1.1,
    });
  }, [matches, fitView]);

  const downCount = rfNodes.filter((n) => n.data.current_state === 'down').length;
  const existingIds = useMemo(() => new Set(rfNodes.map((n) => n.id)), [rfNodes]);

  return (
    <div className="flex h-full flex-col">
      {/* Toolbar */}
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-white/[0.06] bg-slate-950/40 px-4 py-2.5">
        <div className="flex items-center gap-2 text-xs text-slate-400">
          <span className="font-medium text-slate-300">{rfNodes.length}</span> services
          <span className="text-slate-700">·</span>
          <span className="font-medium text-slate-300">{rfEdges.length}</span> links
          {downCount > 0 && (
            <span className="ml-1 inline-flex items-center gap-1.5 rounded-full border border-rose-500/30 bg-rose-500/10 px-2 py-0.5 font-medium text-rose-400">
              <span className="relative flex h-1.5 w-1.5">
                <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-rose-500 opacity-60" />
                <span className="relative inline-flex h-1.5 w-1.5 rounded-full bg-rose-500" />
              </span>
              {downCount} down
            </span>
          )}
        </div>

        <div className="relative ml-auto">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-slate-500" />
          <input
            type="text"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') flyToMatches();
              if (e.key === 'Escape') setQuery('');
            }}
            placeholder="Find a service…"
            className="w-48 rounded-lg border border-white/[0.08] bg-slate-900/80 py-1.5 pl-8 pr-2 text-xs text-white placeholder:text-slate-600 focus:border-cyan-500/50 focus:outline-none"
          />
          {matches && (
            <span className="pointer-events-none absolute right-2.5 top-1/2 -translate-y-1/2 text-[10px] tabular-nums text-slate-500">
              {matches.size} ↵
            </span>
          )}
        </div>

        <button
          type="button"
          onClick={relayout}
          title="Re-run automatic layout"
          className="flex items-center gap-1.5 rounded-lg border border-white/[0.08] bg-slate-900/80 px-2.5 py-1.5 text-xs text-slate-400 transition-colors hover:border-white/[0.15] hover:text-slate-200"
        >
          <LayoutGrid className="h-3.5 w-3.5" strokeWidth={1.75} />
          Tidy up
        </button>

        <AddMonitorPicker existingIds={existingIds} onAdd={addToCanvas} />

        <div className="flex w-full items-center gap-3 lg:w-auto">
          {LEGEND.map(({ state, label }) => (
            <span key={label} className="flex items-center gap-1.5 text-[11px] text-slate-500">
              <span className={`h-1.5 w-1.5 rounded-full ${stateStyle(state).dot}`} />
              {label}
            </span>
          ))}
          <span className="border-l border-white/[0.06] pl-3 text-[11px] text-slate-600">
            drag handle → node to link · click a link to unlink · double-click opens the monitor
          </span>
        </div>
      </div>

      {/* Canvas */}
      <div className="relative flex-1">
        <div
          className="pointer-events-none absolute inset-0 z-[1]"
          style={{
            background:
              'radial-gradient(ellipse 80% 60% at 50% 0%, rgba(8,145,178,0.07), transparent 70%)',
          }}
        />
        {rfNodes.length === 0 && (
          <div className="pointer-events-none absolute inset-0 z-[2] flex items-center justify-center">
            <div className="text-center">
              <Workflow className="mx-auto h-9 w-9 text-slate-600" strokeWidth={1.5} />
              <p className="mt-3 text-sm font-medium text-slate-400">The canvas is empty</p>
              <p className="mt-1 max-w-xs text-xs text-slate-500">
                Use “Add monitor” above to place services here, then drag between them to declare
                who depends on whom.
              </p>
            </div>
          </div>
        )}
        <ReactFlow
          nodes={displayNodes}
          edges={displayEdges}
          nodeTypes={nodeTypes}
          edgeTypes={edgeTypes}
          onNodesChange={onNodesChange}
          onEdgesChange={onEdgesChange}
          onConnect={onConnect}
          onEdgesDelete={onEdgesDelete}
          isValidConnection={isValidConnection}
          onNodeDoubleClick={(_, node) => router.push(`/monitors/${node.id}`)}
          onNodeMouseEnter={(_, node) => setHoveredId(node.id)}
          onNodeMouseLeave={() => setHoveredId(null)}
          fitView
          fitViewOptions={{ padding: 0.2, maxZoom: 1.2 }}
          minZoom={0.15}
          proOptions={{ hideAttribution: true }}
          nodesDraggable
          nodesConnectable
          nodesFocusable={false}
          edgesFocusable
          deleteKeyCode={['Delete', 'Backspace']}
          connectionRadius={36}
          connectionLineStyle={{ stroke: '#22d3ee', strokeWidth: 2, strokeDasharray: '6 4' }}
          colorMode="dark"
          className="!bg-transparent"
        >
          <Background variant={BackgroundVariant.Dots} gap={24} size={1} color="#1e293b" />
          <Controls showInteractive={false} position="bottom-left" />
          {rfNodes.length > 20 && (
            <MiniMap
              pannable
              zoomable
              position="bottom-right"
              maskColor="rgba(2, 6, 23, 0.55)"
              bgColor="rgba(15, 23, 42, 0.85)"
              nodeStrokeWidth={3}
              nodeColor={(node) => {
                const state = (node.data as MonitorNodeData | undefined)?.current_state;
                if (state === 'down') return '#f43f5e';
                if (state === 'suspect') return '#fbbf24';
                if (state === 'up') return '#10b981';
                return '#475569';
              }}
              className="!rounded-lg !border !border-white/[0.08]"
            />
          )}
        </ReactFlow>
      </div>
    </div>
  );
}

export default function DependencyGraphView() {
  const [graph, setGraph] = useState<DependencyGraph | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  const loadGraph = useCallback(async () => {
    try {
      setLoading(true);
      setError('');
      const data = await getDependencyGraph();
      setGraph(data);
    } catch (err) {
      setError(apiErrorMessage(err));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    loadGraph();
    window.addEventListener('tenant-changed', loadGraph);
    return () => window.removeEventListener('tenant-changed', loadGraph);
  }, [loadGraph]);

  if (loading) {
    return (
      <div className="flex h-[calc(100vh-220px)] items-center justify-center rounded-xl border border-white/[0.06] bg-slate-900/50">
        <div className="flex items-center gap-3 text-sm text-slate-500">
          <Workflow className="h-4 w-4 animate-pulse" strokeWidth={1.5} />
          Mapping dependencies…
        </div>
      </div>
    );
  }

  if (error) {
    return (
      <div className="rounded-lg border border-rose-500/20 bg-rose-500/10 px-4 py-3 text-sm text-rose-400">
        {error}
        <button onClick={loadGraph} className="ml-2 underline hover:no-underline">
          Retry
        </button>
      </div>
    );
  }

  if (!graph) {
    return null;
  }

  return (
    <div className="h-[calc(100vh-220px)] overflow-hidden rounded-xl border border-white/[0.06] bg-slate-950/60">
      <ReactFlowProvider>
        <GraphCanvas graph={graph} />
      </ReactFlowProvider>
    </div>
  );
}
