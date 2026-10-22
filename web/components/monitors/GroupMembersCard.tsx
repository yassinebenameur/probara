'use client';

import { useEffect, useMemo, useState } from 'react';
import Link from 'next/link';
import { Monitor } from '@/lib/types';
import { getGroupMembers } from '@/lib/api';
import { displayStatusFromState, isFailingStatus, monitorStateColors, sortBySeverity } from '@/lib/monitor-utils';
import Panel from '@/components/ui/Panel';
import TypeBadge from '@/components/ui/TypeBadge';
import { FOCUS_RING_INSET } from '@/components/ui/focus';

// The monitors a group rolls up, with each one's persisted state. Failing
// members sort first, so the one dragging the group down is on top.
export function GroupMembersCard({ groupId }: { groupId: string }) {
  const [members, setMembers] = useState<Monitor[] | null>(null);

  useEffect(() => {
    let cancelled = false;
    getGroupMembers(groupId)
      .then((items) => !cancelled && setMembers(items || []))
      .catch(() => !cancelled && setMembers([]));
    return () => {
      cancelled = true;
    };
  }, [groupId]);

  const sorted = useMemo(
    () => (members ? sortBySeverity(members, displayStatusFromState, (m) => m.name) : []),
    [members]
  );

  if (members === null) return null;

  const failing = members.filter((m) => isFailingStatus(displayStatusFromState(m))).length;

  return (
    <Panel
      title="Members"
      actions={
        members.length > 0 ? (
          <span className="tabular-nums text-slate-500">
            {members.length}
            {failing > 0 && <span className="text-rose-300"> · {failing} failing</span>}
          </span>
        ) : undefined
      }
    >
      {members.length === 0 ? (
        <p className="text-ui text-slate-400">This group has no members yet.</p>
      ) : (
        <div className="-mx-2 space-y-0.5">
          {sorted.map((member) => {
            const colors = monitorStateColors(member.enabled ? member.current_state : undefined);
            return (
              <Link
                key={member.id}
                href={`/monitors/${member.id}`}
                className={`flex items-center gap-2.5 rounded-lg px-2 py-1.5 transition-colors hover:bg-well active:bg-well-hover ${FOCUS_RING_INSET}`}
              >
                <span className={`h-2 w-2 shrink-0 rounded-full ${colors.dot}`} aria-hidden="true" />
                <span className="min-w-0 flex-1 truncate text-sm text-slate-200" title={member.name}>
                  {member.name}
                </span>
                {member.enabled && <span className="sr-only">, {member.current_state ?? 'unknown'}</span>}
                {member.enabled ? (
                  <TypeBadge type={member.type} config={member.config} />
                ) : (
                  <span className="text-xs text-slate-500">Paused</span>
                )}
              </Link>
            );
          })}
        </div>
      )}
    </Panel>
  );
}
