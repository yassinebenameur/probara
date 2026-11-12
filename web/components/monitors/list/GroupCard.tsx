'use client';

import { useContext, useId, useState, useEffect, useRef } from 'react';
import Link from 'next/link';
import { ChevronRight } from 'lucide-react';
import { FOCUS_RING } from '@/components/ui/focus';
import TypeBadge from '@/components/ui/TypeBadge';
import { flashStyle } from '@/lib/useRowFlash';
import { Monitor, CheckResult } from '@/lib/types';
import { getEffectiveMonitorStatus } from '@/lib/monitor-utils';
import { MONITOR_STATUS_TONE, TONE_TEXT } from '@/lib/status-tones';
import { useRowLayout, rowStateClass, RowFlashContext, handleRowLinkClick } from '@/components/monitors/list/row-layout';
import { StatusDot, StatePill, TargetCell, TagList, RowMetrics } from '@/components/monitors/list/RowCells';
import { SelectCheckbox, RowActions } from '@/components/monitors/list/RowActions';
import { MonitorRow } from '@/components/monitors/list/MonitorRow';

// Group card with expandable children
export function GroupCard({
  monitor,
  results,
  members,
  groupMembersMap,
  memberResults,
  isSelected,
  onClick,
  onDelete,
  onToggleEnabled,
  onSnooze,
  isChecked,
  onToggleSelect,
  onToggleMonitorSelection,
  onDeleteMonitor,
  selectionMode,
  onSelectMonitor,
  selectedMonitorId,
  selectedMonitorIds,
  orderMembers,
  visitedGroupIds = new Set<string>(),
  autoExpand = false,
  memberMatcher,
  groupHasMatch,
}: {
  monitor: Monitor;
  results: CheckResult[];
  members: Monitor[];
  groupMembersMap: Record<string, Monitor[]>;
  memberResults: Record<string, CheckResult[]>;
  isSelected: boolean;
  onClick: () => void;
  onDelete: () => void;
  onToggleEnabled: (monitor: Monitor) => void;
  onSnooze: (monitor: Monitor, durationMinutes: number) => void;
  isChecked: boolean;
  onToggleSelect: () => void;
  onToggleMonitorSelection: (id: string) => void;
  onDeleteMonitor: (id: string) => void;
  selectionMode: boolean;
  onSelectMonitor: (id: string) => void;
  selectedMonitorId: string | null;
  selectedMonitorIds: Set<string>;
  /** The list's sort, applied to members too. */
  orderMembers: (members: Monitor[]) => Monitor[];
  visitedGroupIds?: Set<string>;
  autoExpand?: boolean;
  memberMatcher?: (monitor: Monitor) => boolean;
  groupHasMatch?: (groupId: string) => boolean;
}) {
  const layout = useRowLayout();
  const flashClass = useContext(RowFlashContext);
  const membersId = useId();
  const [expanded, setExpanded] = useState(false); // Collapsed by default

  // Expand automatically while a filter matches members inside this group,
  // and collapse again when that filter is cleared (unless the user expanded it).
  const wasAutoExpanded = useRef(false);
  useEffect(() => {
    if (autoExpand) {
      setExpanded((prev) => {
        if (!prev) wasAutoExpanded.current = true;
        return true;
      });
    } else if (wasAutoExpanded.current) {
      wasAutoExpanded.current = false;
      setExpanded(false);
    }
  }, [autoExpand]);
  const status = getEffectiveMonitorStatus(monitor, results);
  const nextVisitedGroupIDs = new Set(visitedGroupIds);
  nextVisitedGroupIDs.add(monitor.id);

  // Calculate aggregate stats from members
  const memberStatuses = members.map((m) => getEffectiveMonitorStatus(m, memberResults[m.id] || []));
  const healthyCount = memberStatuses.filter((s) => s === 'up').length;
  // The summary takes the group's own rolled-up status tone, so it always
  // agrees with the group's dot (a group can roll a down member up to
  // degraded); no members or no status yet stays neutral.
  const healthTone =
    members.length > 0 && (status === 'down' || status === 'degraded' || status === 'up')
      ? TONE_TEXT[MONITOR_STATUS_TONE[status]]
      : 'text-slate-500';

  // While filtering, only show the members that match (fall back to all if
  // the group itself matched but none of its members did).
  const matchingMembers = memberMatcher
    ? members.filter(
        (member) =>
          memberMatcher(member) ||
          (member.type === 'group' && groupHasMatch?.(member.id))
      )
    : members;
  const visibleMembers = orderMembers(matchingMembers.length > 0 ? matchingMembers : members);
  const hiddenMemberCount = members.length - visibleMembers.length;

  return (
    <div role="none">
      {/* Group Header */}
      <div
        role="row"
        onClick={onClick}
        style={flashStyle(MONITOR_STATUS_TONE[status])}
        className={`group ${layout.cols} cursor-pointer items-center gap-x-3 px-4 py-1.5 transition-colors ${rowStateClass(isSelected, isChecked)} ${flashClass(monitor.id)}`}
      >
        <div role="cell" className="flex items-center justify-end gap-2">
          <SelectCheckbox
            checked={isChecked}
            onChange={onToggleSelect}
            pinned={selectionMode}
            label={`Select ${monitor.name}`}
          />
          <button
            type="button"
            onClick={(e) => { e.stopPropagation(); setExpanded(!expanded); }}
            aria-expanded={expanded}
            aria-controls={expanded ? membersId : undefined}
            aria-label={`${expanded ? 'Collapse' : 'Expand'} ${monitor.name}`}
            className={`flex h-6 w-6 flex-shrink-0 items-center justify-center rounded-md text-slate-400 transition-colors hover:bg-well-hover hover:text-white active:bg-control-hover ${FOCUS_RING}`}
          >
            <ChevronRight
              className={`h-4 w-4 transition-transform duration-base ease-standard ${expanded ? 'rotate-90' : ''}`}
              strokeWidth={1.75}
            />
          </button>
          <StatusDot status={status} />
        </div>

        <div role="cell" className="min-w-0">
          <div className="flex h-5 min-w-0 items-center gap-2 overflow-hidden">
            <Link
              href={`/monitors/${monitor.id}`}
              onClick={handleRowLinkClick}
              className="row-link min-w-0 truncate text-sm font-medium text-white focus-visible:outline-none"
            >
              {monitor.name}
            </Link>
            <StatePill monitor={monitor} status={status} />
            <TagList tags={monitor.tags} max={2} />
          </div>
          <div className="mt-1 flex h-4 min-w-0 items-center gap-2">
            <TypeBadge type="group" size="sm" />
            <span className={`whitespace-nowrap text-xs tabular-nums ${healthTone}`}>
              {healthyCount}/{members.length} healthy
            </span>
            {/* Member dots: which members, at a glance. */}
            <span className="hidden min-w-0 items-center gap-1 overflow-hidden sm:flex">
              {members.slice(0, 8).map((m, i) => (
                <StatusDot key={m.id} status={memberStatuses[i]} name={m.name} />
              ))}
              {members.length > 8 && <span className="text-2xs text-slate-500">+{members.length - 8}</span>}
            </span>
          </div>
        </div>

        <TargetCell />

        {/* A group's own checks are its roll-up; latency means nothing there. */}
        <RowMetrics results={results} showLatency={false} paused={status === 'paused'} />

        <RowActions
          monitor={monitor}
          monitorId={monitor.id}
          onToggleEnabled={onToggleEnabled}
          onSnooze={onSnooze}
          onDelete={onDelete}
        />
      </div>

      {/* Expanded Children */}
      {expanded && visibleMembers.length > 0 && (
        <div
          id={membersId}
          role="none"
          className="dashboard-scroll max-h-[60vh] animate-fade-in overflow-y-auto overscroll-contain border-t border-divider bg-well pl-6 ![scrollbar-gutter:auto]"
        >
          {hiddenMemberCount > 0 && (
            <div role="row">
              <p role="cell" className="px-4 pb-1 pt-2.5 text-xs text-slate-500">
                Showing {visibleMembers.length} matching of {members.length} members
              </p>
            </div>
          )}
          <div role="none" className="divide-y divide-divider">
          {visibleMembers.map((member) => {
            const memberKey = `${monitor.id}:${member.id}`;
            if (member.type === 'group') {
              const cycleDetected = nextVisitedGroupIDs.has(member.id);
              return (
                <div key={memberKey} role="none">
                  {cycleDetected ? (
                    <>
                      <MonitorRow
                        monitor={member}
                        results={memberResults[member.id] || []}
                        isSelected={member.id === selectedMonitorId}
                        onClick={() => onSelectMonitor(member.id)}
                        onDelete={() => onDeleteMonitor(member.id)}
                        onToggleEnabled={onToggleEnabled}
                        onSnooze={onSnooze}
                        isChecked={selectedMonitorIds.has(member.id)}
                        onToggleSelect={() => onToggleMonitorSelection(member.id)}
                        selectionMode={selectionMode}
                      />
                      <div role="row">
                        <p role="cell" className="px-4 pb-2.5 text-xs text-rose-300">
                          Cycle detected in group nesting. Expansion stopped.
                        </p>
                      </div>
                    </>
                  ) : (
                    <GroupCard
                      monitor={member}
                      results={memberResults[member.id] || []}
                      members={groupMembersMap[member.id] || []}
                      groupMembersMap={groupMembersMap}
                      memberResults={memberResults}
                      isSelected={member.id === selectedMonitorId}
                      onClick={() => onSelectMonitor(member.id)}
                      onDelete={() => onDeleteMonitor(member.id)}
                      onToggleEnabled={onToggleEnabled}
                      onSnooze={onSnooze}
                      isChecked={selectedMonitorIds.has(member.id)}
                      onToggleSelect={() => onToggleMonitorSelection(member.id)}
                      onToggleMonitorSelection={onToggleMonitorSelection}
                      onDeleteMonitor={onDeleteMonitor}
                      selectionMode={selectionMode}
                      onSelectMonitor={onSelectMonitor}
                      selectedMonitorId={selectedMonitorId}
                      selectedMonitorIds={selectedMonitorIds}
                      orderMembers={orderMembers}
                      visitedGroupIds={nextVisitedGroupIDs}
                      autoExpand={Boolean(memberMatcher) && groupHasMatch?.(member.id)}
                      memberMatcher={memberMatcher}
                      groupHasMatch={groupHasMatch}
                    />
                  )}
                </div>
              );
            }

            return (
              <MonitorRow
                key={memberKey}
                monitor={member}
                results={memberResults[member.id] || []}
                isSelected={member.id === selectedMonitorId}
                onClick={() => onSelectMonitor(member.id)}
                onDelete={() => onDeleteMonitor(member.id)}
                onToggleEnabled={onToggleEnabled}
                onSnooze={onSnooze}
                isChecked={selectedMonitorIds.has(member.id)}
                onToggleSelect={() => onToggleMonitorSelection(member.id)}
                selectionMode={selectionMode}
              />
            );
          })}
          </div>
        </div>
      )}
    </div>
  );
}
