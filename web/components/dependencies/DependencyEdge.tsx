'use client';

import { memo } from 'react';
import {
  BaseEdge,
  EdgeLabelRenderer,
  getSmoothStepPath,
  useReactFlow,
  type EdgeProps,
} from '@xyflow/react';
import { X } from 'lucide-react';
import { useEdgeVisual } from './hoverStore';

// Edge with an unlink button at its midpoint when selected. Deleting goes
// through deleteElements so onEdgesDelete (the API call) fires either way.
function DependencyEdgeImpl({
  id,
  source,
  target,
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
  const faded = useEdgeVisual(source, target);
  const [path, labelX, labelY] = getSmoothStepPath({
    sourceX,
    sourceY,
    sourcePosition,
    targetX,
    targetY,
    targetPosition,
  });

  return (
    <g style={{ opacity: faded ? 0.12 : 1, transition: 'opacity 150ms ease' }}>
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
    </g>
  );
}

export const DependencyEdge = memo(DependencyEdgeImpl);
