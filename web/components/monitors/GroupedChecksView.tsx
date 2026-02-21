'use client';

import { useState, useEffect } from 'react';
import { Monitor, CheckResult } from '@/lib/types';
import { getMonitors, getMonitorResults, getGroupMembers } from '@/lib/api';
import StatusPill from '@/components/ui/StatusPill';
import { calculateUptime, countOperationalResults, getLatestStatus, MonitorHealthStatus } from '@/lib/monitor-utils';

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
      const response = await getMonitors({ page_size: 100 });
      const allMonitors = response?.items || [];
      
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
          const members = await getGroupMembers(groupId);
          setGroupMembers({ ...groupMembers, [groupId]: members });
        } catch (error) {
          console.error(`Failed to load group members for ${groupId}:`, error);
        }
      }
    }
    
    setExpandedGroups(newExpanded);
  };

  const getGroupStatus = (groupId: string): MonitorHealthStatus => {
    const members = groupMembers[groupId] || [];
    if (members.length === 0) return 'unknown';

    let upCount = 0;
    let downCount = 0;
    let unknownCount = 0;

    members.forEach(member => {
      const results = checkResults[member.id] || [];
      const status = getLatestStatus(results);
      if (status === 'up') upCount++;
      else if (status === 'down') downCount++;
      else if (status === 'degraded') downCount++;
      else if (status === 'unknown') unknownCount++;
    });

    if (upCount === members.length) return 'up';
    if (downCount === members.length) return 'down';
    if (unknownCount === members.length) return 'unknown';
    return 'degraded';
  };

  if (loading) {
    return <div className="text-center py-8 text-muted">Loading...</div>;
  }

  return (
    <div className="space-y-4">
      <h2 className="text-xl font-semibold">Grouped Checks</h2>

      {/* Groups Section */}
      {groups.length > 0 && (
        <div className="space-y-2">
          <h3 className="text-sm font-medium text-muted">Monitor Groups</h3>
          {groups.map((group) => {
            const isExpanded = expandedGroups.has(group.id);
            const members = groupMembers[group.id] || [];
            const status = getGroupStatus(group.id);
            const results = checkResults[group.id] || [];
            const uptime = calculateUptime(results);
            const operationalCount = countOperationalResults(results);

            return (
              <div key={group.id} className="rounded-lg border border-[rgba(255,255,255,0.06)] bg-[rgba(15,23,42,0.92)] overflow-hidden">
                {/* Group Header */}
                <div
                  onClick={() => toggleGroup(group.id)}
                  className="flex items-center justify-between p-4 cursor-pointer hover:bg-[rgba(15,23,42,0.95)]"
                >
                  <div className="flex items-center gap-3">
                    <span className="text-lg">{isExpanded ? '▼' : '▶'}</span>
                    <div>
                      <div className="font-medium">{group.name}</div>
                      <div className="text-xs text-muted">
                        GROUP • {group.member_ids?.length || 0} monitors
                      </div>
                    </div>
                  </div>
                  <div className="flex items-center gap-4">
                    <div className="text-sm text-muted">
                      {operationalCount > 0 ? `${uptime.toFixed(2)}% uptime` : 'No data'}
                    </div>
                    <StatusPill
                      status={status}
                      label={
                        status === 'up' ? 'All Up' : status === 'down' ? 'All Down' : status === 'unknown' ? 'Paused' : 'Degraded'
                      }
                    />
                  </div>
                </div>

                {/* Group Members */}
                {isExpanded && members.length > 0 && (
                  <div className="border-t border-[rgba(255,255,255,0.06)] bg-[rgba(15,23,42,0.98)]">
                    {members.map((member) => {
                      const memberResults = checkResults[member.id] || [];
                      const memberStatus = getLatestStatus(memberResults);
                      const memberUptime = calculateUptime(memberResults);
                      const memberOperationalCount = countOperationalResults(memberResults);

                      return (
                        <div
                          key={member.id}
                          className="flex items-center justify-between p-3 pl-12 border-b border-[rgba(255,255,255,0.03)] last:border-b-0"
                        >
                          <div>
                            <div className="text-sm font-medium">{member.name}</div>
                            <div className="text-xs text-muted">{member.type.toUpperCase()}</div>
                          </div>
                          <div className="flex items-center gap-4">
                            <div className="text-xs text-muted">
                              {memberOperationalCount > 0 ? `${memberUptime.toFixed(2)}%` : 'N/A'}
                            </div>
                            <StatusPill
                              status={memberStatus}
                              label={
                                memberStatus === 'up'
                                  ? 'Up'
                                  : memberStatus === 'down'
                                  ? 'Down'
                                  : memberStatus === 'unknown'
                                  ? 'Paused'
                                  : 'Degraded'
                              }
                            />
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
      )}

      {/* Ungrouped Monitors Section */}
      {monitors.length > 0 && (
        <div className="space-y-2">
          <h3 className="text-sm font-medium text-muted">Individual Monitors</h3>
          <div className="rounded-lg border border-[rgba(255,255,255,0.06)] bg-[rgba(15,23,42,0.92)]">
            {monitors.map((monitor, idx) => {
              const results = checkResults[monitor.id] || [];
              const status = getLatestStatus(results);
              const uptime = calculateUptime(results);
              const operationalCount = countOperationalResults(results);

              return (
                <div
                  key={monitor.id}
                  className={`flex items-center justify-between p-3 ${
                    idx !== monitors.length - 1
                      ? 'border-b border-[rgba(255,255,255,0.03)]'
                      : ''
                  }`}
                >
                  <div>
                    <div className="text-sm font-medium">{monitor.name}</div>
                    <div className="text-xs text-muted">{monitor.type.toUpperCase()}</div>
                  </div>
                  <div className="flex items-center gap-4">
                    <div className="text-xs text-muted">
                      {operationalCount > 0 ? `${uptime.toFixed(2)}%` : 'N/A'}
                    </div>
                    <StatusPill
                      status={status}
                      label={
                        status === 'up' ? 'Up' : status === 'down' ? 'Down' : status === 'unknown' ? 'Paused' : 'Degraded'
                      }
                    />
                  </div>
                </div>
              );
            })}
          </div>
        </div>
      )}

      {groups.length === 0 && monitors.length === 0 && (
        <div className="text-center py-12 text-muted">
          <div className="text-4xl mb-3">📊</div>
          <div className="text-lg">No monitors or groups found</div>
        </div>
      )}
    </div>
  );
}
