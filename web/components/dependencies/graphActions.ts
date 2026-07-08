'use client';

import { createContext, useContext } from 'react';

// Actions the custom nodes trigger (focus, walk open a frontier, page a group
// node). Provided by the DependencyGraphView shell so node data stays pure.
export interface GraphActions {
  focusNode: (id: string) => void;
  expandFrontier: (id: string) => void;
  expandGroup: (groupId: string, mode: 'page' | 'all') => void;
}

export const GraphActionsContext = createContext<GraphActions | null>(null);

export function useGraphActions(): GraphActions {
  const actions = useContext(GraphActionsContext);
  if (!actions) throw new Error('useGraphActions must be used inside GraphActionsContext');
  return actions;
}
