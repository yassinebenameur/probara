'use client';

import { useCallback, useEffect, useMemo, useState } from 'react';
import { ReactFlowProvider } from '@xyflow/react';
import { PanelLeft, Workflow } from 'lucide-react';
import { DependencyGraph, DependencyMonitor } from '@/lib/types';
import { getDependencyGraph } from '@/lib/api';
import { useToast } from '@/components/ui/ToastProvider';
import { apiErrorMessage } from './graphTheme';
import {
  buildAdjacency,
  buildFlowGraph,
  EXPAND_PAGE,
  FAN_THRESHOLD,
  OVERVIEW_THRESHOLD,
  sortForRail,
} from './graphModel';
import { GraphActionsContext, type GraphActions } from './graphActions';
import { GraphCanvas } from './GraphCanvas';
import { ServiceRail } from './ServiceRail';
import { setMatches } from './hoverStore';

export default function DependencyGraphView({ refreshToken = 0 }: { refreshToken?: number }) {
  const { showToast } = useToast();
  const [graph, setGraph] = useState<DependencyGraph | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  // Structural view state: what subset of the graph is on the canvas.
  const [focusId, setFocusId] = useState<string | null>(null);
  const [showAll, setShowAll] = useState(false);
  const [expandedFrom, setExpandedFrom] = useState<ReadonlySet<string>>(new Set());
  const [expandedCounts, setExpandedCounts] = useState<Record<string, number>>({});
  const [extraNodes, setExtraNodes] = useState<DependencyMonitor[]>([]);
  const [query, setQuery] = useState('');
  const [railOpen, setRailOpen] = useState(false);

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

  // Initial load, reload on accepted AI suggestions, full reset on tenant switch.
  useEffect(() => {
    loadGraph();
  }, [loadGraph, refreshToken]);

  useEffect(() => {
    const onTenantChange = () => {
      setFocusId(null);
      setShowAll(false);
      setExpandedFrom(new Set());
      setExpandedCounts({});
      setExtraNodes([]);
      setQuery('');
      loadGraph();
    };
    window.addEventListener('tenant-changed', onTenantChange);
    return () => window.removeEventListener('tenant-changed', onTenantChange);
  }, [loadGraph]);

  const adjacency = useMemo(() => (graph ? buildAdjacency(graph) : null), [graph]);
  const railEntries = useMemo(() => (adjacency ? sortForRail(adjacency) : []), [adjacency]);

  // If the focused service left the graph (deleted, last edge removed), fall
  // back to the most interesting service instead of an empty canvas.
  useEffect(() => {
    if (!adjacency || !focusId) return;
    if (!adjacency.byId.has(focusId) && !extraNodes.some((m) => m.id === focusId)) {
      setFocusId(null);
      showToast('The focused service is no longer in the dependency map', 'info', 3500);
    }
  }, [adjacency, focusId, extraNodes, showToast]);

  const totalServices = graph?.nodes.length ?? 0;
  const isLargeGraph = totalServices > OVERVIEW_THRESHOLD;
  // Large graphs open focused on the top rail entry (worst state, busiest)
  // instead of an unreadable full render; small graphs keep the old overview.
  const effectiveFocusId = showAll
    ? null
    : (focusId ?? (isLargeGraph ? (railEntries[0]?.monitor.id ?? null) : null));

  const flow = useMemo(() => {
    if (!adjacency) return { nodes: [], edges: [] };
    return buildFlowGraph(adjacency, {
      focusId: effectiveFocusId,
      expandedFrom,
      expandedCounts,
      extraNodes,
    });
  }, [adjacency, effectiveFocusId, expandedFrom, expandedCounts, extraNodes]);

  const focusNode = useCallback((id: string) => {
    setFocusId(id);
    setShowAll(false);
    setExpandedFrom(new Set());
    setExpandedCounts({});
    setExtraNodes([]);
  }, []);

  const expandFrontier = useCallback((id: string) => {
    setExpandedFrom((prev) => new Set(prev).add(id));
  }, []);

  const expandGroup = useCallback((groupId: string, mode: 'page' | 'all') => {
    setExpandedCounts((prev) => ({
      ...prev,
      [groupId]:
        mode === 'all'
          ? Number.MAX_SAFE_INTEGER
          : (prev[groupId] ?? FAN_THRESHOLD) + EXPAND_PAGE,
    }));
  }, []);

  const actions = useMemo<GraphActions>(
    () => ({ focusNode, expandFrontier, expandGroup }),
    [focusNode, expandFrontier, expandGroup]
  );

  // Search highlights matches on the canvas without touching the node arrays.
  const matchIds = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q || !graph) return null;
    return new Set(
      graph.nodes
        .filter((n) => n.name.toLowerCase().includes(q) || n.type.toLowerCase().includes(q))
        .map((n) => n.id)
    );
  }, [query, graph]);
  useEffect(() => {
    setMatches(matchIds);
    return () => setMatches(null);
  }, [matchIds]);

  const onEdgePersisted = useCallback((from: string, to: string) => {
    setGraph((g) => {
      if (!g) return g;
      if (g.edges.some((e) => e.from === from && e.to === to)) return g;
      return { ...g, edges: [...g.edges, { from, to }] };
    });
    // A picker-added monitor that just got wired up is now a real graph node.
    setExtraNodes((extras) => extras.filter((m) => m.id !== from && m.id !== to));
  }, []);

  const onEdgeRemoved = useCallback(
    (from: string, to: string) => {
      setGraph((g) =>
        g ? { ...g, edges: g.edges.filter((e) => !(e.from === from && e.to === to)) } : g
      );
      // Keep the endpoints on the canvas even if that was their last edge.
      const orphanCandidates = (graph?.nodes ?? []).filter((n) => n.id === from || n.id === to);
      setExtraNodes((extras) => [
        ...extras,
        ...orphanCandidates.filter((m) => !extras.some((e) => e.id === m.id)),
      ]);
    },
    [graph]
  );

  const onAddExtra = useCallback((monitor: DependencyMonitor) => {
    setExtraNodes((extras) =>
      extras.some((m) => m.id === monitor.id) ? extras : [...extras, monitor]
    );
  }, []);

  const onClearFocus = useCallback(() => {
    setFocusId(null);
    // Leaving focus on a large graph means "let me see everything".
    if (isLargeGraph) setShowAll(true);
  }, [isLargeGraph]);

  if (loading && !graph) {
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

  if (!graph || !adjacency) {
    return null;
  }

  const focusMonitor = effectiveFocusId
    ? (adjacency.byId.get(effectiveFocusId) ?? extraNodes.find((m) => m.id === effectiveFocusId) ?? null)
    : null;
  const downTotal = graph.nodes.filter((n) => n.current_state === 'down').length;

  return (
    <GraphActionsContext.Provider value={actions}>
      <div className="relative flex h-[calc(100vh-220px)] overflow-hidden rounded-xl border border-white/[0.06] bg-slate-950/60">
        <button
          type="button"
          onClick={() => setRailOpen((v) => !v)}
          title={railOpen ? 'Hide service list' : 'Show service list'}
          className="absolute left-2 top-2 z-20 flex h-7 w-7 items-center justify-center rounded-lg border border-white/[0.08] bg-slate-900/90 text-slate-400 transition-colors hover:text-slate-200 md:hidden"
        >
          <PanelLeft className="h-3.5 w-3.5" strokeWidth={1.75} />
        </button>
        <div className={`${railOpen ? 'flex' : 'hidden'} md:flex`}>
          <ServiceRail
            entries={railEntries}
            focusId={effectiveFocusId}
            query={query}
            onQueryChange={setQuery}
            onSelect={focusNode}
          />
        </div>
        <ReactFlowProvider>
          <GraphCanvas
            flowNodes={flow.nodes}
            flowEdges={flow.edges}
            totalServices={totalServices}
            totalLinks={adjacency.edges.length}
            downTotal={downTotal}
            focusMonitor={focusMonitor}
            showAll={showAll}
            isLargeGraph={isLargeGraph}
            onClearFocus={onClearFocus}
            onShowAll={() => setShowAll(true)}
            onExitShowAll={() => setShowAll(false)}
            onEdgePersisted={onEdgePersisted}
            onEdgeRemoved={onEdgeRemoved}
            onAddExtra={onAddExtra}
          />
        </ReactFlowProvider>
      </div>
    </GraphActionsContext.Provider>
  );
}
