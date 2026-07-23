'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import { useRouter } from 'next/navigation';
import {
  applyEdgeChanges,
  applyNodeChanges,
  Background,
  BackgroundVariant,
  Controls,
  MiniMap,
  ReactFlow,
  useReactFlow,
  type Connection,
  type Edge,
  type EdgeChange,
  type NodeChange,
} from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import { Expand, Focus, LayoutGrid, Workflow, X } from 'lucide-react';
import { DependencyMonitor } from '@/lib/types';
import { addMonitorDependency, removeMonitorDependency } from '@/lib/api';
import { useToast } from '@/components/ui/ToastProvider';
import { apiErrorMessage, LEGEND, stateStyle, styleEdge } from './graphTheme';
import { isGroupId, type AppNode, type MonitorNodeData } from './graphModel';
import { MonitorNode } from './MonitorNode';
import { GroupNode } from './GroupNode';
import { DependencyEdge } from './DependencyEdge';
import { AddMonitorPicker } from './AddMonitorPicker';
import { setHover } from './hoverStore';
import { useThemeName } from '@/components/ui/useThemeName';

const nodeTypes = { monitor: MonitorNode, depGroup: GroupNode };
const edgeTypes = { dependency: DependencyEdge };

export interface GraphCanvasProps {
  flowNodes: AppNode[];
  flowEdges: Edge[];
  totalServices: number;
  totalLinks: number;
  downTotal: number;
  focusMonitor: DependencyMonitor | null;
  showAll: boolean;
  isLargeGraph: boolean;
  onClearFocus: () => void;
  onShowAll: () => void;
  onExitShowAll: () => void;
  onEdgePersisted: (from: string, to: string) => void;
  onEdgeRemoved: (from: string, to: string) => void;
  onAddExtra: (monitor: DependencyMonitor) => void;
}

export function GraphCanvas({
  flowNodes,
  flowEdges,
  totalServices,
  totalLinks,
  downTotal,
  focusMonitor,
  showAll,
  isLargeGraph,
  onClearFocus,
  onShowAll,
  onExitShowAll,
  onEdgePersisted,
  onEdgeRemoved,
  onAddExtra,
}: GraphCanvasProps) {
  const router = useRouter();
  const { fitView } = useReactFlow();
  const { showToast } = useToast();
  const themeName = useThemeName();
  const isLight = themeName === 'light';
  // Local copies so user drags layer on top of the computed layout; reseeded
  // whenever the structural state (focus/expand/graph) produces a new layout.
  const [rfNodes, setRfNodes] = useState<AppNode[]>(flowNodes);
  const [rfEdges, setRfEdges] = useState<Edge[]>(flowEdges);

  const mountedRef = useRef(false);
  useEffect(() => {
    setRfNodes(flowNodes);
    setRfEdges(flowEdges);
    // The very first fit is ReactFlow's own fitView prop (it waits for node
    // measurement); this effect only re-fits after focus/expand changes.
    if (!mountedRef.current) {
      mountedRef.current = true;
      return;
    }
    const timer = window.setTimeout(
      () => fitView({ padding: 0.2, duration: 400, maxZoom: 1.1 }),
      60
    );
    return () => window.clearTimeout(timer);
  }, [flowNodes, flowEdges, fitView]);

  const onNodesChange = useCallback(
    (changes: NodeChange<AppNode>[]) => setRfNodes((ns) => applyNodeChanges(changes, ns)),
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
      !isGroupId(conn.source) &&
      !isGroupId(conn.target) &&
      !rfEdges.some((e) => e.source === conn.source && e.target === conn.target),
    [rfEdges]
  );

  // Drawing an edge = "source depends on target". Optimistic: the edge lands
  // immediately and snaps back if the API rejects it (e.g. a cycle).
  const onConnect = useCallback(
    (conn: Connection) => {
      if (!isValidConnection(conn)) return;
      const targetNode = rfNodes.find((n) => n.id === conn.target && n.type === 'monitor');
      const targetState = targetNode ? (targetNode.data as MonitorNodeData).monitor.current_state : undefined;
      const edge = styleEdge(conn.source, conn.target, targetState);
      setRfEdges((es) => [...es, edge]);
      addMonitorDependency(conn.source, conn.target)
        .then(() => {
          showToast('Dependency linked', 'success', 2500);
          onEdgePersisted(conn.source, conn.target);
        })
        .catch((err) => {
          setRfEdges((es) => es.filter((e) => e.id !== edge.id));
          showToast(apiErrorMessage(err), 'error');
        });
    },
    [isValidConnection, rfNodes, showToast, onEdgePersisted]
  );

  // Fires for the edge ✕ button and the Delete/Backspace key alike.
  const onEdgesDelete = useCallback(
    (deleted: Edge[]) => {
      for (const edge of deleted) {
        if (isGroupId(edge.source) || isGroupId(edge.target)) continue;
        removeMonitorDependency(edge.source, edge.target)
          .then(() => {
            showToast('Dependency removed', 'success', 2500);
            onEdgeRemoved(edge.source, edge.target);
          })
          .catch((err) => {
            setRfEdges((es) => (es.some((e) => e.id === edge.id) ? es : [...es, edge]));
            showToast(apiErrorMessage(err), 'error');
          });
      }
    },
    [showToast, onEdgeRemoved]
  );

  const relayout = useCallback(() => {
    setRfNodes(flowNodes);
    window.requestAnimationFrame(() => fitView({ padding: 0.2, duration: 500 }));
  }, [flowNodes, fitView]);

  const onNodeMouseEnter = useCallback(
    (_: unknown, node: AppNode) => {
      const hood = new Set([node.id]);
      for (const e of rfEdges) {
        if (e.source === node.id) hood.add(e.target);
        if (e.target === node.id) hood.add(e.source);
      }
      setHover(hood);
    },
    [rfEdges]
  );
  const onNodeMouseLeave = useCallback(() => setHover(null), []);

  const visibleMonitors = rfNodes.filter((n) => n.type === 'monitor').length;
  const existingIds = new Set(rfNodes.map((n) => n.id));

  return (
    <div className="flex min-w-0 flex-1 flex-col">
      {/* Toolbar */}
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-white/[0.06] bg-slate-950/40 px-4 py-2.5">
        <div className="flex items-center gap-2 text-xs text-slate-400">
          <span className="font-medium text-slate-300">
            {visibleMonitors === totalServices ? totalServices : `${visibleMonitors} of ${totalServices}`}
          </span>{' '}
          services
          <span className="text-slate-700">·</span>
          <span className="font-medium text-slate-300">{totalLinks}</span> links
          {downTotal > 0 && (
            <span className="ml-1 inline-flex items-center gap-1.5 rounded-full border border-rose-500/30 bg-rose-500/10 px-2 py-0.5 font-medium text-rose-400">
              <span className="relative flex h-1.5 w-1.5">
                <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-rose-500 opacity-60" />
                <span className="relative inline-flex h-1.5 w-1.5 rounded-full bg-rose-500" />
              </span>
              {downTotal} down
            </span>
          )}
        </div>

        {focusMonitor && (
          <span className="inline-flex items-center gap-1.5 rounded-full border border-cyan-500/30 bg-cyan-500/10 py-0.5 pl-2.5 pr-1 text-xs text-cyan-300">
            <Focus className="h-3 w-3" strokeWidth={2} />
            <span className="max-w-[180px] truncate" title={focusMonitor.name}>
              {focusMonitor.name}
            </span>
            <button
              type="button"
              onClick={onClearFocus}
              title="Leave focus"
              className="flex h-4 w-4 items-center justify-center rounded-full text-cyan-400/70 transition-colors hover:bg-cyan-500/20 hover:text-cyan-300"
            >
              <X className="h-3 w-3" strokeWidth={2} />
            </button>
          </span>
        )}

        <div className="ml-auto flex items-center gap-2">
          {focusMonitor && !showAll && (
            <button
              type="button"
              onClick={onShowAll}
              title="Render the entire dependency map"
              className="flex items-center gap-1.5 rounded-lg border border-white/[0.08] bg-slate-900/80 px-2.5 py-1.5 text-xs text-slate-400 transition-colors hover:border-white/[0.15] hover:text-slate-200"
            >
              <Expand className="h-3.5 w-3.5" strokeWidth={1.75} />
              Show all ({totalServices})
            </button>
          )}
          {showAll && isLargeGraph && (
            <button
              type="button"
              onClick={onExitShowAll}
              className="flex items-center gap-1.5 rounded-lg border border-cyan-500/30 bg-cyan-500/10 px-2.5 py-1.5 text-xs text-cyan-400 transition-colors hover:border-cyan-500/50 hover:bg-cyan-500/20"
            >
              <Focus className="h-3.5 w-3.5" strokeWidth={1.75} />
              Focus view
            </button>
          )}
          <button
            type="button"
            onClick={relayout}
            title="Re-run automatic layout"
            className="flex items-center gap-1.5 rounded-lg border border-white/[0.08] bg-slate-900/80 px-2.5 py-1.5 text-xs text-slate-400 transition-colors hover:border-white/[0.15] hover:text-slate-200"
          >
            <LayoutGrid className="h-3.5 w-3.5" strokeWidth={1.75} />
            Tidy up
          </button>
          <AddMonitorPicker existingIds={existingIds} onAdd={onAddExtra} />
        </div>

        <div className="flex w-full items-center gap-3 xl:w-auto">
          {LEGEND.map(({ state, label }) => (
            <span key={label} className="flex items-center gap-1.5 text-[11px] text-slate-500">
              <span className={`h-1.5 w-1.5 rounded-full ${stateStyle(state).dot}`} />
              {label}
            </span>
          ))}
          <span className="hidden border-l border-white/[0.06] pl-3 text-[11px] text-slate-600 2xl:inline">
            drag handle → node to link · click a link to unlink · double-click opens the monitor
          </span>
        </div>
      </div>

      {/* Canvas */}
      <div className="relative min-h-0 flex-1">
        <div
          className="pointer-events-none absolute inset-0 z-[1]"
          style={{
            background:
              'radial-gradient(ellipse 80% 60% at 50% 0%, rgba(var(--accent-rgb), 0.05), transparent 70%)',
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
          nodes={rfNodes}
          edges={rfEdges}
          nodeTypes={nodeTypes}
          edgeTypes={edgeTypes}
          onNodesChange={onNodesChange}
          onEdgesChange={onEdgesChange}
          onConnect={onConnect}
          onEdgesDelete={onEdgesDelete}
          isValidConnection={isValidConnection}
          onNodeDoubleClick={(_, node) => {
            if (node.type === 'monitor') router.push(`/monitors/${node.id}`);
          }}
          onNodeMouseEnter={onNodeMouseEnter}
          onNodeMouseLeave={onNodeMouseLeave}
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
          connectionLineStyle={{ stroke: '#ff8a5c', strokeWidth: 2, strokeDasharray: '6 4' }}
          colorMode={themeName}
          onlyRenderVisibleElements
          className="!bg-transparent"
        >
          <Background
            variant={BackgroundVariant.Dots}
            gap={24}
            size={1}
            color={isLight ? '#d9dde3' : '#161b24'}
          />
          <Controls showInteractive={false} position="bottom-left" />
          {!focusMonitor && rfNodes.length > 20 && (
            <MiniMap
              pannable
              zoomable
              position="bottom-right"
              maskColor={isLight ? 'rgba(238,240,243, 0.6)' : 'rgba(11,13,17, 0.55)'}
              bgColor={isLight ? 'rgba(255,255,255, 0.9)' : 'rgba(16,19,26, 0.85)'}
              nodeStrokeWidth={3}
              nodeColor={(node) => {
                const data = node.data as MonitorNodeData | undefined;
                const state = data?.monitor?.current_state;
                if (state === 'down') return '#f04a5a';
                if (state === 'suspect' || state === 'degraded') return '#e6b23f';
                if (state === 'up') return '#46d17f';
                return isLight ? '#a8b0ba' : '#475569';
              }}
              className="!rounded-lg !border !border-white/[0.08]"
            />
          )}
        </ReactFlow>
      </div>
    </div>
  );
}
