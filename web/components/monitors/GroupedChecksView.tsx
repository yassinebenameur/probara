'use client';

import { useState, useEffect } from 'react';
import { Monitor, CheckResult } from '@/lib/types';
import { getMonitorResults, getGroupMembers } from '@/lib/api';
import { Activity, ChevronRight } from 'lucide-react';
import Pill, { PillTone } from '@/components/ui/Pill';
import EmptyState from '@/components/ui/EmptyState';

type StatusKey = 'up' | 'down' | 'degraded' | 'unknown' | 'paused' | 'maintenance';

const STATUS_TONE: Record<StatusKey, PillTone> = {
  up: 'success',
  down: 'danger',
  degraded: 'warning',
  unknown: 'neutral',
  paused: 'neutral',
  maintenance: 'info',
};
import { calculateUptime, countOperationalResults, getEffectiveMonitorStatus, MonitorDisplayStatus } from '@/lib/monitor-utils';
import { getAllMonitors, sortMonitorsByName } from '@/lib/monitor-list';
import { monitorTypeLabel } from '@/lib/monitor-type-labels';

export default function GroupedChecksView() {
  const [monitors, setMonitors] = useState<Monitor[]>([]);
  const [groups, setGroups] = useState<Monitor[]>([]);
  const [expandedGroups, setExpandedGroups] = useState<Set<string>>(new Set());
  const [groupMembers, setGroupMembers] = useState<Record<string, Monitor[]>>({});
  const [checkResults, setCheckResults] = useState<Record<string, CheckResult[]>>({});
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    loadData();
  }, []);

  const loadData = async () => {
    try {
      setLoading(true);
      const allMonitors = await getAllMonitors();
      
      // Separate groups from regular monitors
      const groupMonitors = allMonitors.filter(m => m.type === 'group');
      const regularMonitors = allMonitors.filter(m => m.type !== 'group');
      
      setGroups(groupMonitors);
      setMonitors(regularMonitors);

      // Load check results for all monitors
      const resultsMap: Record<string, CheckResult[]> = {};
      await Promise.all(
        allMonitors.map(async (monitor) => {
          try {
            const results = await getMonitorResults(monitor.id, { limit: 100 });
            resultsMap[monitor.id] = results.results || [];
          } catch (err) {
            console.error(`Failed to fetch results for monitor ${monitor.id}:`, err);
            resultsMap[monitor.id] = [];
          }
        })
      );
      setCheckResults(resultsMap);
    } catch (error) {
      console.error('Failed to load monitors:', error);
    } finally {
      setLoading(false);
    }
  };

  const toggleGroup = async (groupId: string) => {
    const newExpanded = new Set(expandedGroups);
    
    if (newExpanded.has(groupId)) {
      newExpanded.delete(groupId);
    } else {
      newExpanded.add(groupId);
      
      // Load group members if not already loaded
      if (!groupMembers[groupId]) {
        try {
          const members = sortMonitorsByName(await getGroupMembers(groupId));
          setGroupMembers({ ...groupMembers, [groupId]: members });
        } catch (error) {
          console.error(`Failed to load group members for ${groupId}:`, error);
        }
      }
    }
    
    setExpandedGroups(newExpanded);
  };

  const getGroupStatus = (group: Monitor): MonitorDisplayStatus => {
    if (!group.enabled) return 'paused';

    const members = groupMembers[group.id] || [];
    if (members.length === 0) return 'unknown';

    let upCount = 0;
    let downCount = 0;
    let unknownCount = 0;

    members.forEach(member => {
      const results = checkResults[member.id] || [];
      const status = getEffectiveMonitorStatus(member, results);
      if (status === 'up') upCount++;
      else if (status === 'down') downCount++;
      else if (status === 'degraded') downCount++;
      else if (status === 'unknown' || status === 'paused' || status === 'maintenance') unknownCount++;
    });

    if (upCount === members.length) return 'up';
    if (downCount === members.length) return 'down';
    if (unknownCount === members.length) return 'unknown';
    return 'degraded';
  };

  if (loading) {
    return <div className="py-8 text-center text-sm text-slate-500">Loading...</div>;
  }

  return (
    <div className="space-y-6">
      <h2 className="text-[15px] font-semibold text-white">Grouped checks</h2>

      {/* Groups Section */}
      {groups.length > 0 && (
        <div className="space-y-2">
          <h3 className="text-[13px] font-medium text-slate-400">Monitor groups</h3>
          <div className="glass divide-y divide-divider overflow-hidden">
            {groups.map((group) => {
              const isExpanded = expandedGroups.has(group.id);
              const members = groupMembers[group.id] || [];
              const status = getGroupStatus(group);
              const results = checkResults[group.id] || [];
              const uptime = calculateUptime(results);
              const operationalCount = countOperationalResults(results);

              return (
                <div key={group.id}>
                  {/* Group Header */}
                  <div
                    onClick={() => toggleGroup(group.id)}
                    className="flex cursor-pointer items-center justify-between gap-4 px-5 py-3.5 transition-colors hover:bg-well"
                  >
                    <div className="flex min-w-0 items-center gap-3">
                      <ChevronRight
                        className={`h-4 w-4 flex-shrink-0 text-slate-400 transition-transform ${isExpanded ? 'rotate-90' : ''}`}
                        strokeWidth={1.75}
                        aria-hidden="true"
                      />
                      <div className="min-w-0">
                        <div className="truncate text-sm font-medium text-white">{group.name}</div>
                        <div className="text-xs text-slate-500">
                          Group · {group.member_ids?.length || 0} monitors
                        </div>
                      </div>
                    </div>
                    <div className="flex items-center gap-4">
                      <div className="text-[13px] tabular-nums text-slate-400">
                        {operationalCount > 0 ? `${uptime.toFixed(2)}% uptime` : 'No data'}
                      </div>
                      <Pill tone={STATUS_TONE[status as StatusKey]} size="sm" dot>
                        {status === 'up' ? 'All up' : status === 'down' ? 'All down' : status === 'paused' ? 'Paused' : status === 'unknown' ? 'Unknown' : 'Degraded'}
                      </Pill>
                    </div>
                  </div>

                  {/* Group Members */}
                  {isExpanded && members.length > 0 && (
                    <div className="divide-y divide-divider border-t border-divider bg-well">
                      {members.map((member) => {
                        const memberResults = checkResults[member.id] || [];
                        const memberStatus = getEffectiveMonitorStatus(member, memberResults);
                        const memberUptime = calculateUptime(memberResults);
                        const memberOperationalCount = countOperationalResults(memberResults);

                        return (
                          <div
                            key={member.id}
                            className="flex items-center justify-between gap-4 py-3 pl-12 pr-5"
                          >
                            <div className="min-w-0">
                              <div className="truncate text-sm font-medium text-slate-200">{member.name}</div>
                              <div className="text-[11px] font-medium text-slate-500">{monitorTypeLabel(member.type)}</div>
                            </div>
                            <div className="flex items-center gap-4">
                              <div className="text-xs tabular-nums text-slate-400">
                                {memberOperationalCount > 0 ? `${memberUptime.toFixed(2)}%` : 'N/A'}
                              </div>
                              <Pill tone={STATUS_TONE[memberStatus as StatusKey]} size="sm" dot>
                                {memberStatus === 'up' ? 'Up' : memberStatus === 'down' ? 'Down' : memberStatus === 'paused' ? 'Paused' : memberStatus === 'unknown' ? 'Unknown' : 'Degraded'}
                              </Pill>
                            </div>
                          </div>
                        );
                      })}
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        </div>
      )}

      {/* Ungrouped Monitors Section */}
      {monitors.length > 0 && (
        <div className="space-y-2">
          <h3 className="text-[13px] font-medium text-slate-400">Individual monitors</h3>
          <div className="glass divide-y divide-divider overflow-hidden">
            {monitors.map((monitor) => {
              const results = checkResults[monitor.id] || [];
              const status = getEffectiveMonitorStatus(monitor, results);
              const uptime = calculateUptime(results);
              const operationalCount = countOperationalResults(results);

              return (
                <div
                  key={monitor.id}
                  className="flex items-center justify-between gap-4 px-5 py-3 transition-colors hover:bg-well"
                >
                  <div className="min-w-0">
                    <div className="truncate text-sm font-medium text-white">{monitor.name}</div>
                    <div className="text-[11px] font-medium text-slate-500">{monitorTypeLabel(monitor.type)}</div>
                  </div>
                  <div className="flex items-center gap-4">
                    <div className="text-xs tabular-nums text-slate-400">
                      {operationalCount > 0 ? `${uptime.toFixed(2)}%` : 'N/A'}
                    </div>
                    <Pill tone={STATUS_TONE[status as StatusKey]} size="sm" dot>
                      {status === 'up' ? 'Up' : status === 'down' ? 'Down' : status === 'paused' ? 'Paused' : status === 'unknown' ? 'Unknown' : 'Degraded'}
                    </Pill>
                  </div>
                </div>
              );
            })}
          </div>
        </div>
      )}

      {groups.length === 0 && monitors.length === 0 && (
        <EmptyState icon={<Activity strokeWidth={1.5} />} title="No monitors or groups found" />
      )}
    </div>
  );
}
