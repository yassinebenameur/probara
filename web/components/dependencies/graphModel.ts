import dagre from 'dagre';
import { MarkerType, type Edge, type Node } from '@xyflow/react';
import { DependencyGraph, DependencyMonitor, MonitorState } from '@/lib/types';
import { EDGE_COLORS, GROUP_NODE_HEIGHT, NODE_HEIGHT, NODE_WIDTH, styleEdge } from './graphTheme';

// Graphs at or below this node count default to the classic show-everything
// overview; above it the canvas opens focused on the most interesting service.
export const OVERVIEW_THRESHOLD = 30;
// A node keeps at most this many leaf neighbors per direction before the
// remainder folds into a single expandable group node.
export const FAN_THRESHOLD = 8;
// Each click on a group node reveals this many more monitors.
export const EXPAND_PAGE = 10;
// Above this many rendered nodes, per-node ping animations are disabled.
const PING_LIMIT = 60;
// Never leave a tiny "+2 more" stub group — just show them.
const GROUP_STUB = 2;

export type Direction = 'in' | 'out';

export function groupNodeId(parentId: string, direction: Direction): string {
  return `group:${parentId}:${direction}`;
}

export function isGroupId(id: string): boolean {
  return id.startsWith('group:');
}

export type MonitorNodeData = {
  monitor: DependencyMonitor;
  // Neighbors that exist in the full graph but are outside the focused view;
  // rendered as the ◂ / ▸ expand chips on the card.
  hiddenIn: number;
  hiddenOut: number;
  isFocus: boolean;
  lowDetail: boolean;
} & Record<string, unknown>;

export type GroupNodeData = {
  parentId: string;
  direction: Direction;
  hiddenCount: number;
  downCount: number;
  warnCount: number;
} & Record<string, unknown>;

export type MonitorFlowNode = Node<MonitorNodeData, 'monitor'>;
export type GroupFlowNode = Node<GroupNodeData, 'depGroup'>;
export type AppNode = MonitorFlowNode | GroupFlowNode;

export interface Adjacency {
  byId: Map<string, DependencyMonitor>;
  /** id -> monitors that depend on it (edge {from,to} means "from depends on to") */
  dependentsOf: Map<string, string[]>;
  /** id -> monitors it depends on */
  dependenciesOf: Map<string, string[]>;
  degree: Map<string, number>;
  edges: Array<{ from: string; to: string }>;
}

export function buildAdjacency(graph: DependencyGraph): Adjacency {
  const byId = new Map(graph.nodes.map((n) => [n.id, n]));
  // Drop self-edges and edges referencing missing nodes defensively.
  const edges = graph.edges.filter((e) => e.from !== e.to && byId.has(e.from) && byId.has(e.to));

  const dependentsOf = new Map<string, string[]>();
  const dependenciesOf = new Map<string, string[]>();
  const degree = new Map<string, number>();
  for (const e of edges) {
    (dependenciesOf.get(e.from) ?? dependenciesOf.set(e.from, []).get(e.from)!).push(e.to);
    (dependentsOf.get(e.to) ?? dependentsOf.set(e.to, []).get(e.to)!).push(e.from);
    degree.set(e.from, (degree.get(e.from) ?? 0) + 1);
    degree.set(e.to, (degree.get(e.to) ?? 0) + 1);
  }
  return { byId, dependentsOf, dependenciesOf, degree, edges };
}

const STATE_RANK: Record<string, number> = { down: 0, suspect: 1, degraded: 1, up: 2, unknown: 3 };

function stateRank(state?: MonitorState): number {
  return STATE_RANK[state || 'unknown'] ?? 3;
}

export interface RailEntry {
  monitor: DependencyMonitor;
  dependents: number;
  dependencies: number;
}

// Down services first, then suspect/degraded, then busiest by degree.
export function sortForRail(adj: Adjacency): RailEntry[] {
  const entries: RailEntry[] = [];
  for (const monitor of adj.byId.values()) {
    entries.push({
      monitor,
      dependents: adj.dependentsOf.get(monitor.id)?.length ?? 0,
      dependencies: adj.dependenciesOf.get(monitor.id)?.length ?? 0,
    });
  }
  entries.sort((a, b) => {
    const rank = stateRank(a.monitor.current_state) - stateRank(b.monitor.current_state);
    if (rank !== 0) return rank;
    const deg = (adj.degree.get(b.monitor.id) ?? 0) - (adj.degree.get(a.monitor.id) ?? 0);
    if (deg !== 0) return deg;
    return a.monitor.name.localeCompare(b.monitor.name);
  });
  return entries;
}

export interface FlowInput {
  /** null = render the whole graph (overview / show-all) */
  focusId: string | null;
  /** nodes whose full neighborhood has been walked open via the ◂ / ▸ chips */
  expandedFrom: ReadonlySet<string>;
  /** groupNodeId -> how many of its monitors have been revealed */
  expandedCounts: Record<string, number>;
  /** edgeless monitors placed on the canvas via the picker */
  extraNodes: DependencyMonitor[];
}

function worstTone(monitors: DependencyMonitor[]): 'down' | 'suspect' | 'default' {
  let tone: 'down' | 'suspect' | 'default' = 'default';
  for (const m of monitors) {
    if (m.current_state === 'down') return 'down';
    if (m.current_state === 'suspect' || m.current_state === 'degraded') tone = 'suspect';
  }
  return tone;
}

function groupEdge(gid: string, parentId: string, direction: Direction, tone: 'down' | 'suspect' | 'default'): Edge {
  const color = EDGE_COLORS[tone];
  const [source, target] = direction === 'in' ? [gid, parentId] : [parentId, gid];
  return {
    id: `${source}->${target}`,
    source,
    target,
    type: 'smoothstep',
    selectable: false,
    animated: tone === 'down',
    style: { stroke: color, strokeWidth: 1.5, strokeDasharray: '6 4' },
    markerEnd: { type: MarkerType.ArrowClosed, color, width: 15, height: 15 },
  };
}

// Computes the visible subgraph (focus neighborhood + walked expansions, with
// large leaf fans folded into group nodes) and lays it out with dagre. Runs
// only when the structural state changes — never on hover or search.
export function buildFlowGraph(adj: Adjacency, input: FlowInput): { nodes: AppNode[]; edges: Edge[] } {
  const extras = input.extraNodes.filter((m) => !adj.byId.has(m.id));
  const monitorOf = (id: string) => adj.byId.get(id) ?? extras.find((m) => m.id === id);

  // 1. Reveal: the focus and its 1-hop neighborhood, then the neighborhoods
  // of every walked-open node reachable from there (fixpoint for chains).
  const revealed = new Set<string>();
  const focusId = input.focusId && adj.byId.has(input.focusId) ? input.focusId : null;
  if (!focusId) {
    for (const id of adj.byId.keys()) revealed.add(id);
  } else {
    const addNeighbors = (id: string) => {
      for (const n of adj.dependentsOf.get(id) ?? []) revealed.add(n);
      for (const n of adj.dependenciesOf.get(id) ?? []) revealed.add(n);
    };
    revealed.add(focusId);
    addNeighbors(focusId);
    const pending = new Set(input.expandedFrom);
    let progress = true;
    while (progress) {
      progress = false;
      for (const id of pending) {
        if (revealed.has(id)) {
          addNeighbors(id);
          pending.delete(id);
          progress = true;
        }
      }
    }
  }
  for (const m of extras) revealed.add(m.id);

  const visibleEdges = adj.edges.filter((e) => revealed.has(e.from) && revealed.has(e.to));

  // 2. Collapse: a "leaf" hangs off exactly one visible node, so hiding it
  // loses nothing but its single edge — represented by the group node.
  const visDegree = new Map<string, number>();
  for (const e of visibleEdges) {
    visDegree.set(e.from, (visDegree.get(e.from) ?? 0) + 1);
    visDegree.set(e.to, (visDegree.get(e.to) ?? 0) + 1);
  }
  const anchored = new Set<string>(input.expandedFrom);
  if (focusId) anchored.add(focusId);
  for (const m of extras) anchored.add(m.id);
  const isLeaf = (id: string) => visDegree.get(id) === 1 && !anchored.has(id);

  const hidden = new Set<string>();
  const groupNodes: GroupFlowNode[] = [];
  const groupEdges: Edge[] = [];
  // Big fans (visible leaves + their group node) get laid out as a compact
  // grid block instead of dagre's single mile-high column.
  const fans: Array<{ parentId: string; direction: Direction; memberIds: string[] }> = [];
  for (const parentId of revealed) {
    if (isLeaf(parentId)) continue;
    for (const direction of ['in', 'out'] as const) {
      const neighbors = direction === 'in' ? adj.dependentsOf.get(parentId) : adj.dependenciesOf.get(parentId);
      if (!neighbors || neighbors.length <= FAN_THRESHOLD) continue;
      const leaves = neighbors
        .filter((n) => revealed.has(n) && isLeaf(n) && !hidden.has(n))
        .map((n) => adj.byId.get(n)!)
        .sort((a, b) => {
          const rank = stateRank(a.current_state) - stateRank(b.current_state);
          if (rank !== 0) return rank;
          const deg = (adj.degree.get(b.id) ?? 0) - (adj.degree.get(a.id) ?? 0);
          if (deg !== 0) return deg;
          return a.name.localeCompare(b.name);
        });
      if (leaves.length <= FAN_THRESHOLD) continue;
      const gid = groupNodeId(parentId, direction);
      let shown = input.expandedCounts[gid] ?? FAN_THRESHOLD;
      if (leaves.length - shown <= GROUP_STUB) shown = leaves.length;
      const shownLeaves = leaves.slice(0, shown);
      const folded = leaves.slice(shown);
      const memberIds = shownLeaves.map((m) => m.id);
      if (folded.length > 0) {
        for (const m of folded) hidden.add(m.id);
        groupNodes.push({
          id: gid,
          type: 'depGroup',
          position: { x: 0, y: 0 },
          width: NODE_WIDTH,
          height: GROUP_NODE_HEIGHT,
          connectable: false,
          data: {
            parentId,
            direction,
            hiddenCount: folded.length,
            downCount: folded.filter((m) => m.current_state === 'down').length,
            warnCount: folded.filter((m) => m.current_state === 'suspect' || m.current_state === 'degraded').length,
          },
        });
        groupEdges.push(
          groupEdge(gid, parentId, direction, direction === 'out' ? worstTone(folded) : worstTone([adj.byId.get(parentId)!]))
        );
        memberIds.push(gid);
      }
      if (memberIds.length > FAN_THRESHOLD) fans.push({ parentId, direction, memberIds });
    }
  }

  // 3. Assemble rendered monitors, real edges, and frontier counts.
  const renderedIds = [...revealed].filter((id) => !hidden.has(id));
  const lowDetail = renderedIds.length + groupNodes.length > PING_LIMIT;

  const monitorNodes: MonitorFlowNode[] = [];
  for (const id of renderedIds) {
    const monitor = monitorOf(id);
    if (!monitor) continue;
    const hiddenIn = (adj.dependentsOf.get(id) ?? []).filter((n) => !revealed.has(n)).length;
    const hiddenOut = (adj.dependenciesOf.get(id) ?? []).filter((n) => !revealed.has(n)).length;
    monitorNodes.push({
      id,
      type: 'monitor',
      position: { x: 0, y: 0 },
      // Fixed dimensions so fitView has correct bounds even for nodes that
      // onlyRenderVisibleElements has not mounted (and thus not measured).
      width: NODE_WIDTH,
      height: NODE_HEIGHT,
      data: { monitor, hiddenIn, hiddenOut, isFocus: id === focusId, lowDetail },
    });
  }

  const stateOf = new Map([...adj.byId.values()].map((n) => [n.id, n.current_state]));
  const realEdges = visibleEdges
    .filter((e) => !hidden.has(e.from) && !hidden.has(e.to))
    .map((e) => styleEdge(e.from, e.to, stateOf.get(e.to)));

  // 4. Layout the visible subgraph only (bounded in focus mode). Fan members
  // are represented in dagre by one block node sized for their grid, then
  // placed cell by cell afterwards.
  const FAN_MAX_ROWS = 8;
  const FAN_GAP_X = 70;
  const FAN_GAP_Y = 18;
  const fanMemberIds = new Set(fans.flatMap((f) => f.memberIds));
  const fanDims = fans.map((f) => {
    const m = f.memberIds.length;
    const cols = Math.ceil(m / FAN_MAX_ROWS);
    const rows = Math.ceil(m / cols);
    return {
      ...f,
      id: `fan:${f.parentId}:${f.direction}`,
      cols,
      rows,
      width: cols * NODE_WIDTH + (cols - 1) * FAN_GAP_X,
      height: rows * (NODE_HEIGHT + FAN_GAP_Y) - FAN_GAP_Y,
    };
  });

  const g = new dagre.graphlib.Graph();
  g.setGraph({ rankdir: 'LR', nodesep: 36, ranksep: 110 });
  g.setDefaultEdgeLabel(() => ({}));
  for (const node of monitorNodes) {
    if (!fanMemberIds.has(node.id)) g.setNode(node.id, { width: NODE_WIDTH, height: NODE_HEIGHT });
  }
  for (const node of groupNodes) {
    if (!fanMemberIds.has(node.id)) g.setNode(node.id, { width: NODE_WIDTH, height: GROUP_NODE_HEIGHT });
  }
  for (const f of fanDims) g.setNode(f.id, { width: f.width, height: f.height });
  for (const edge of [...realEdges, ...groupEdges]) {
    if (fanMemberIds.has(edge.source) || fanMemberIds.has(edge.target)) continue;
    g.setEdge(edge.source, edge.target);
  }
  for (const f of fanDims) {
    if (f.direction === 'in') g.setEdge(f.id, f.parentId);
    else g.setEdge(f.parentId, f.id);
  }
  dagre.layout(g);

  // Fill each fan grid column-major, keeping the most severe entries in the
  // column nearest the parent.
  const fanPositions = new Map<string, { x: number; y: number }>();
  for (const f of fanDims) {
    const center = g.node(f.id);
    const left = center.x - f.width / 2;
    const top = center.y - f.height / 2;
    f.memberIds.forEach((id, i) => {
      const col = Math.floor(i / f.rows);
      const row = i % f.rows;
      const colIdx = f.direction === 'in' ? f.cols - 1 - col : col;
      fanPositions.set(id, {
        x: left + colIdx * (NODE_WIDTH + FAN_GAP_X),
        y: top + row * (NODE_HEIGHT + FAN_GAP_Y),
      });
    });
  }

  const nodes: AppNode[] = [...monitorNodes, ...groupNodes];
  for (const node of nodes) {
    const fanPos = fanPositions.get(node.id);
    if (fanPos) {
      node.position = fanPos;
      continue;
    }
    const pos = g.node(node.id);
    const height = node.type === 'depGroup' ? GROUP_NODE_HEIGHT : NODE_HEIGHT;
    node.position = { x: pos.x - NODE_WIDTH / 2, y: pos.y - height / 2 };
  }

  return { nodes, edges: [...realEdges, ...groupEdges] };
}
