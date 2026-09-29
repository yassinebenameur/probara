'use client';

import { useEffect, useState } from 'react';
import Link from 'next/link';
import { Monitor } from '@/lib/types';
import { getGroupMembers } from '@/lib/api';
import { monitorStateColors } from '@/lib/monitor-utils';
import FormCard from '@/components/ui/FormCard';

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
    <FormCard className="p-4">
      <h3 className="mb-3 text-xs font-medium uppercase tracking-wider text-slate-500">
        Members ({members.length})
      </h3>
      {members.length === 0 ? (
        <p className="text-xs text-slate-500">This group has no members yet.</p>
      ) : (
        <div className="space-y-0.5">
          {members.map((member) => {
            const colors = monitorStateColors(member.enabled ? member.current_state : undefined);
            return (
              <Link
                key={member.id}
                href={`/monitors/${member.id}`}
                className="flex items-center gap-2 rounded-lg px-2 py-1.5 transition-colors hover:bg-white/[0.04]"
              >
                <span className={`h-2 w-2 shrink-0 rounded-full ${colors.dot}`} />
                <span className="min-w-0 flex-1 truncate text-sm text-slate-300">{member.name}</span>
                <span className="text-[10px] uppercase text-slate-600">
                  {member.enabled ? member.type : 'paused'}
                </span>
              </Link>
            );
          })}
        </div>
      )}
    </FormCard>
  );
}
