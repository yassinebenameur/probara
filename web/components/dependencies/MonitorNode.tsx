'use client';

import { memo } from 'react';
import { Handle, Position, useConnection, type NodeProps } from '@xyflow/react';
import { ChevronLeft, ChevronRight, Crosshair, Workflow } from 'lucide-react';
import { stateStyle, TYPE_ICONS } from './graphTheme';
import { type MonitorFlowNode } from './graphModel';
import { useGraphActions } from './graphActions';
import { useNodeVisual } from './hoverStore';
import { monitorTypeLabel } from '@/lib/monitor-type-labels';

function MonitorNodeImpl({ id, data }: NodeProps<MonitorFlowNode>) {
  const { monitor } = data;
  const s = stateStyle(monitor.current_state);
  const Icon = TYPE_ICONS[monitor.type] ?? Workflow;
  const { dimmed, highlighted } = useNodeVisual(id);
  const { focusNode, expandFrontier } = useGraphActions();
  // While a connection is being dragged from another node, the whole card
  // becomes the drop target — dropping anywhere on it completes the link.
  const connection = useConnection();
  const isDropTarget = connection.inProgress && connection.fromNode?.id !== id;
  const emphasized = highlighted || data.isFocus || isDropTarget;

  // Nodes sit inside the canvas transform, so they are opaque cards rather
  // than blurred glass.
  return (
    <div
      className={[
        'group relative w-[216px] rounded-xl border bg-glass-strong shadow-[0_1px_2px_rgb(0_0_0/0.12)] transition-[opacity,border-color] duration-200',
        emphasized ? 'border-cyan-500/70' : `${s.card} hover:border-cyan-500/40`,
        dimmed ? 'opacity-20' : 'opacity-100',
      ].join(' ')}
    >
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
          className="!h-2.5 !w-2.5 !border-2 !border-slate-500 !bg-slate-900 transition-colors group-hover:!border-cyan-500"
        />
      )}
      <div className="flex cursor-pointer items-center gap-2.5 px-3 py-2.5">
        <span className={`flex h-8 w-8 shrink-0 items-center justify-center rounded-lg ${s.iconTile}`}>
          <Icon className="h-4 w-4" strokeWidth={1.75} />
        </span>
        <div className="min-w-0 flex-1">
          <p className="truncate text-[13px] font-medium leading-tight text-white" title={monitor.name}>
            {monitor.name}
          </p>
          <p className="node-meta mt-1 flex items-center gap-1.5 text-[11px] text-slate-500">
            {monitorTypeLabel(monitor.type)}
            <span className={`h-1.5 w-1.5 rounded-full ${s.dot}`} />
            <span className={`capitalize ${s.text}`}>{monitor.current_state || 'unknown'}</span>
          </p>
        </div>
      </div>
      <Handle
        type="source"
        position={Position.Right}
        className="!h-2.5 !w-2.5 !border-2 !border-slate-500 !bg-slate-900 transition-colors group-hover:!border-cyan-500 group-hover:!bg-cyan-500/30"
      />

      {!data.isFocus && (
        <button
          type="button"
          title="Focus on this service"
          onClick={(e) => {
            e.stopPropagation();
            focusNode(id);
          }}
          className="nodrag nopan absolute -right-2 -top-2 z-10 hidden h-6 w-6 items-center justify-center rounded-full border border-edge-strong bg-slate-900 text-slate-400 shadow-[0_1px_2px_rgb(0_0_0/0.12)] transition-colors hover:border-cyan-500/50 hover:text-cyan-300 group-hover:flex"
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
          className="nodrag nopan absolute -bottom-3 left-2 z-10 flex items-center gap-0.5 rounded-full border border-cyan-500/30 bg-slate-900 py-px pl-0.5 pr-1.5 text-[11px] font-medium tabular-nums text-cyan-300 transition-colors hover:border-cyan-500/60"
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
          className="nodrag nopan absolute -bottom-3 right-2 z-10 flex items-center gap-0.5 rounded-full border border-cyan-500/30 bg-slate-900 py-px pl-1.5 pr-0.5 text-[11px] font-medium tabular-nums text-cyan-300 transition-colors hover:border-cyan-500/60"
        >
          {data.hiddenOut}
          <ChevronRight className="h-3 w-3" strokeWidth={2} />
        </button>
      )}
    </div>
  );
}

export const MonitorNode = memo(MonitorNodeImpl);
