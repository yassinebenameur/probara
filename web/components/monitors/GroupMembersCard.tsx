'use client';

import { useEffect, useState } from 'react';
import Link from 'next/link';
import { Monitor } from '@/lib/types';
import { getGroupMembers } from '@/lib/api';
import { monitorStateColors } from '@/lib/monitor-utils';
import Panel from '@/components/ui/Panel';
import { monitorTypeLabel } from '@/lib/monitor-type-labels';

// The monitors a group rolls up, with each one's persisted state.
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

  if (members === null) return null;

  return (
    <Panel title={`Members (${members.length})`}>
      {members.length === 0 ? (
        <p className="text-[13px] text-slate-400">This group has no members yet.</p>
      ) : (
        <div className="-mx-2 space-y-0.5">
          {members.map((member) => {
            const colors = monitorStateColors(member.enabled ? member.current_state : undefined);
            return (
              <Link
                key={member.id}
                href={`/monitors/${member.id}`}
                className="flex items-center gap-2.5 rounded-lg px-2 py-1.5 transition-colors hover:bg-well"
              >
                <span className={`h-2 w-2 shrink-0 rounded-full ${colors.dot}`} />
                <span className="min-w-0 flex-1 truncate text-sm text-slate-200">{member.name}</span>
                <span className="text-xs text-slate-500">
                  {member.enabled ? monitorTypeLabel(member.type) : 'Paused'}
                </span>
              </Link>
            );
          })}
        </div>
      )}
    </Panel>
  );
}
