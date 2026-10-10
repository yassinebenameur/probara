'use client';

import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react';
import { ReactFlowProvider } from '@xyflow/react';
import { PanelLeft, Workflow } from 'lucide-react';
import { DependencyGraph, DependencyMonitor } from '@/lib/types';
import { getDependencyGraph } from '@/lib/api';
import { useToast } from '@/components/ui/ToastProvider';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import { ICON_BUTTON_CLASS } from '@/components/ui/IconButton';
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
import { AddMonitorPicker } from './AddMonitorPicker';
import { setMatches } from './hoverStore';

interface DependencyGraphViewProps {
  refreshToken?: number;
  /** Show graph editing (linking, unlinking, placing monitors). */
  canWrite?: boolean;
  /** Identity still loading: canWrite reads false, so say nothing role-specific yet. */
  permissionsLoading?: boolean;
  /** The "Suggest with AI" trigger, sized for where it sits. */
  renderAIButton?: (size: 'sm' | 'xs') => ReactNode;
}

export default function DependencyGraphView({ refreshToken = 0, canWrite = true, permissionsLoading = false, renderAIButton }: DependencyGraphViewProps) {
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

  // Initial load and reload on accepted AI suggestions. A tenant switch needs no
  // handling here: TenantScope remounts the page, resetting graph and view state.
  useEffect(() => {
    loadGraph();
  }, [loadGraph, refreshToken]);

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
      <div className="glass flex h-[calc(100vh-220px)] items-center justify-center">
        <div className="flex items-center gap-3 text-sm text-slate-500">
          <Workflow className="h-4 w-4 animate-pulse" strokeWidth={1.5} />
          Mapping dependencies…
        </div>
      </div>
    );
  }

  if (error) {
    return (
      <Callout
        tone="danger"
        action={
          <Button variant="ghost" size="xs" onClick={loadGraph}>
            Retry
          </Button>
        }
      >
        {error}
      </Callout>
    );
  }

  if (!graph || !adjacency) {
    return null;
  }

  // Nothing declared and nothing placed yet: one empty state with the two
  // ways in, instead of an empty rail, an empty canvas and their chrome.
  if (graph.nodes.length === 0 && extraNodes.length === 0) {
    return (
      <div className="glass flex min-h-[420px] flex-col items-center justify-center px-6 py-14 text-center">
        <span
          className="mb-4 inline-flex h-11 w-11 items-center justify-center rounded-full bg-well-hover text-slate-400"
          aria-hidden="true"
        >
          <Workflow className="h-5 w-5" strokeWidth={1.5} />
        </span>
        <h3 className="text-title font-semibold text-white">No dependencies yet</h3>
        {!permissionsLoading && (
          <p className="mt-1.5 max-w-sm text-sm text-slate-400">
            {canWrite
              ? 'Place monitors on the canvas, then drag from one to another to declare who depends on whom.'
              : 'No dependencies have been declared in this workspace.'}
          </p>
        )}
        {canWrite && (
          <div className="mt-5 flex flex-wrap items-center justify-center gap-2">
            <AddMonitorPicker existingIds={new Set()} onAdd={onAddExtra} variant="accent" align="center" />
            {renderAIButton?.('sm')}
          </div>
        )}
      </div>
    );
  }

  const focusMonitor = effectiveFocusId
    ? (adjacency.byId.get(effectiveFocusId) ?? extraNodes.find((m) => m.id === effectiveFocusId) ?? null)
    : null;
  const downTotal = graph.nodes.filter((n) => n.current_state === 'down').length;

  return (
    <GraphActionsContext.Provider value={actions}>
      <div className="glass relative flex h-[calc(100vh-220px)] overflow-hidden">
        <button
          type="button"
          onClick={() => setRailOpen((v) => !v)}
          title={railOpen ? 'Hide service list' : 'Show service list'}
          className={`${ICON_BUTTON_CLASS} absolute left-2 top-2 z-20 border border-edge-control bg-glass-strong md:hidden`}
        >
          <PanelLeft strokeWidth={1.75} />
        </button>
        <div
          className={`${railOpen ? 'flex' : 'hidden'} absolute inset-y-0 left-0 z-10 bg-slate-900 shadow-overlay md:static md:flex md:bg-transparent md:shadow-none`}
        >
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
            canWrite={canWrite}
            aiAction={renderAIButton?.('xs')}
          />
        </ReactFlowProvider>
      </div>
    </GraphActionsContext.Provider>
  );
}
