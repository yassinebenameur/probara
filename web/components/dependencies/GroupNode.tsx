'use client';

import { memo } from 'react';
import { Handle, Position, type NodeProps } from '@xyflow/react';
import { Layers } from 'lucide-react';
import { EXPAND_PAGE, type GroupFlowNode } from './graphModel';
import { useGraphActions } from './graphActions';
import { useNodeVisual } from './hoverStore';

// A folded fan: stands in for the monitors of one node's oversized
// neighborhood. Clicking the label deals out the next few; "all" unfolds
// everything at once.
function GroupNodeImpl({ id, data }: NodeProps<GroupFlowNode>) {
  const { expandGroup } = useGraphActions();
  const { dimmed } = useNodeVisual(id);
  const label = data.direction === 'in' ? 'dependents' : 'dependencies';
  const allQuiet = data.downCount === 0 && data.warnCount === 0;

  return (
    <div className={`relative w-[216px] transition-opacity duration-200 ${dimmed ? 'opacity-20' : 'opacity-100'}`}>
      <div className="relative rounded-xl border border-dashed border-slate-500/50 bg-glass-strong shadow-[0_1px_2px_rgb(0_0_0/0.12)]">
        <Handle
          type="target"
          position={Position.Left}
          isConnectable={false}
          className="!h-2 !w-2 !border-0 !bg-transparent"
        />
        <div className="flex items-center gap-2.5 py-2 pl-3 pr-2.5">
          <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-well-hover text-slate-400">
            <Layers className="h-4 w-4" strokeWidth={1.75} />
          </span>
          <div className="min-w-0 flex-1">
            <button
              type="button"
              onClick={(e) => {
                e.stopPropagation();
                expandGroup(id, 'page');
              }}
              title={`Show ${Math.min(EXPAND_PAGE, data.hiddenCount)} more`}
              className="nodrag nopan block w-full truncate text-left text-[13px] font-medium text-slate-200 transition-colors hover:text-cyan-300"
            >
              +{data.hiddenCount} more {label}
            </button>
            <p className="mt-1 flex items-center gap-2 text-[11px]">
              {data.downCount > 0 && (
                <span className="flex items-center gap-1 text-rose-300">
                  <span className="h-1.5 w-1.5 rounded-full bg-rose-500" />
                  {data.downCount} down
                </span>
              )}
              {data.warnCount > 0 && (
                <span className="flex items-center gap-1 text-amber-300">
                  <span className="h-1.5 w-1.5 rounded-full bg-amber-500" />
                  {data.warnCount} degraded
                </span>
              )}
              {allQuiet && (
                <span className="flex items-center gap-1 text-slate-500">
                  <span className="h-1.5 w-1.5 rounded-full bg-emerald-500" />
                  No alerts
                </span>
              )}
            </p>
          </div>
          <button
            type="button"
            onClick={(e) => {
              e.stopPropagation();
              expandGroup(id, 'all');
            }}
            title={`Show all ${data.hiddenCount}`}
            className="nodrag nopan shrink-0 rounded-md px-1.5 py-0.5 text-[11px] font-medium text-slate-400 transition-colors hover:bg-well-hover hover:text-cyan-300"
          >
            All
          </button>
        </div>
        <Handle
          type="source"
          position={Position.Right}
          isConnectable={false}
          className="!h-2 !w-2 !border-0 !bg-transparent"
        />
      </div>
    </div>
  );
}

export const GroupNode = memo(GroupNodeImpl);
