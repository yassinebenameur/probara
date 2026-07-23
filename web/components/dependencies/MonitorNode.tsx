'use client';

import { memo } from 'react';
import { Handle, Position, useConnection, type NodeProps } from '@xyflow/react';
import { ChevronLeft, ChevronRight, Crosshair, Workflow } from 'lucide-react';
import { stateStyle, TYPE_ICONS } from './graphTheme';
import { type MonitorFlowNode } from './graphModel';
import { useGraphActions } from './graphActions';
import { useNodeVisual } from './hoverStore';

function MonitorNodeImpl({ id, data }: NodeProps<MonitorFlowNode>) {
  const { monitor } = data;
  const s = stateStyle(monitor.current_state);
  const Icon = TYPE_ICONS[monitor.type] ?? Workflow;
  const failing =
    monitor.current_state === 'down' ||
    monitor.current_state === 'suspect' ||
    monitor.current_state === 'degraded';
  const { dimmed, highlighted } = useNodeVisual(id);
  const { focusNode, expandFrontier } = useGraphActions();
  // While a connection is being dragged from another node, the whole card
  // becomes the drop target — dropping anywhere on it completes the link.
  const connection = useConnection();
  const isDropTarget = connection.inProgress && connection.fromNode?.id !== id;

  return (
    <div
      className={[
        'group relative w-[216px] rounded-xl border',
        'bg-gradient-to-br from-slate-900/95 to-slate-950/95',
        'transition-all duration-200 hover:-translate-y-px hover:border-cyan-400/40 hover:shadow-[0_0_20px_-6px_rgba(255,138,92,0.4)]',
        s.card,
        dimmed ? 'opacity-20' : 'opacity-100',
        highlighted || data.isFocus
          ? 'border-cyan-400/70 shadow-[0_0_24px_-4px_rgba(255,138,92,0.55)]'
          : '',
        isDropTarget ? 'border-cyan-400/60 shadow-[0_0_20px_-4px_rgba(255,138,92,0.5)]' : '',
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
          <p className="truncate text-[13px] font-medium leading-tight text-white" title={monitor.name}>
            {monitor.name}
          </p>
          <p className="node-meta mt-0.5 flex items-center gap-1.5 text-[10px] uppercase tracking-wider text-slate-500">
            {monitor.type}
            <span className="relative flex h-1.5 w-1.5">
              {failing && !data.lowDetail && (
                <span className={`absolute inline-flex h-full w-full animate-ping rounded-full ${s.dot} opacity-60`} />
              )}
              <span className={`relative inline-flex h-1.5 w-1.5 rounded-full ${s.dot}`} />
            </span>
            <span className={s.text}>{monitor.current_state || 'unknown'}</span>
          </p>
        </div>
      </div>
      <Handle
        type="source"
        position={Position.Right}
        className="!h-2.5 !w-2.5 !border-2 !border-slate-700 !bg-slate-900 transition-colors group-hover:!border-cyan-400/70 group-hover:!bg-cyan-500/30"
      />

      {!data.isFocus && (
        <button
          type="button"
          title="Focus on this service"
          onClick={(e) => {
            e.stopPropagation();
            focusNode(id);
          }}
          className="nodrag nopan absolute -right-2 -top-2 z-10 hidden h-6 w-6 items-center justify-center rounded-full border border-white/[0.1] bg-slate-900 text-slate-400 shadow-lg transition-colors hover:border-cyan-400/50 hover:text-cyan-400 group-hover:flex"
        >
          <Crosshair className="h-3.5 w-3.5" strokeWidth={1.75} />
        </button>
      )}

      {data.hiddenIn > 0 && (
        <button
          type="button"
          title={`Reveal ${data.hiddenIn} more dependent service${data.hiddenIn === 1 ? '' : 's'}`}
          onClick={(e) => {
            e.stopPropagation();
            expandFrontier(id);
          }}
          className="nodrag nopan absolute -bottom-3 left-2 z-10 flex items-center gap-0.5 rounded-full border border-cyan-500/30 bg-slate-900 py-px pl-0.5 pr-1.5 text-[10px] font-medium tabular-nums text-cyan-400 transition-colors hover:border-cyan-500/60 hover:bg-cyan-500/15"
        >
          <ChevronLeft className="h-3 w-3" strokeWidth={2} />
          {data.hiddenIn}
        </button>
      )}
      {data.hiddenOut > 0 && (
        <button
          type="button"
          title={`Reveal ${data.hiddenOut} more ${data.hiddenOut === 1 ? 'dependency' : 'dependencies'}`}
          onClick={(e) => {
            e.stopPropagation();
            expandFrontier(id);
          }}
          className="nodrag nopan absolute -bottom-3 right-2 z-10 flex items-center gap-0.5 rounded-full border border-cyan-500/30 bg-slate-900 py-px pl-1.5 pr-0.5 text-[10px] font-medium tabular-nums text-cyan-400 transition-colors hover:border-cyan-500/60 hover:bg-cyan-500/15"
        >
          {data.hiddenOut}
          <ChevronRight className="h-3 w-3" strokeWidth={2} />
        </button>
      )}
    </div>
  );
}

export const MonitorNode = memo(MonitorNodeImpl);
