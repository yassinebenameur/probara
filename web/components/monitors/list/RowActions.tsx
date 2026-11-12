'use client';

import { useContext } from 'react';
import { Trash2, Pencil, Copy, Pause, Play } from 'lucide-react';
import RowMenu, { RowMenuItem, RowMenuSeparator, useCloseRowMenu } from '@/components/ui/RowMenu';
import Button from '@/components/ui/Button';
import SharedSelectCheckbox from '@/components/ui/SelectCheckbox';
import { Monitor } from '@/lib/types';
import { useIsTenantAdmin } from '@/components/providers/CurrentUserProvider';
import { isSipCall } from '@/lib/sip-config';
import { SelectableContext } from '@/components/monitors/list/row-layout';

// The row's selection box is always in the DOM (its slot is reserved, so
// nothing shifts) but only shows on row hover or keyboard focus — or always
// once a selection exists (`pinned`), and always on touch screens, which
// have no hover to reveal it.
const REVEAL_ON_HOVER =
  'opacity-0 transition-opacity duration-fast ease-standard focus-within:opacity-100 group-hover:opacity-100 [@media(hover:none)]:opacity-100';

export function SelectCheckbox({
  checked,
  onChange,
  pinned,
  label,
}: {
  checked: boolean;
  onChange: () => void;
  pinned: boolean;
  label?: string;
}) {
  const selectable = useContext(SelectableContext);
  if (!selectable) return null;
  return (
    <SharedSelectCheckbox
      checked={checked}
      onChange={onChange}
      label={label}
      // mr-auto: the lead cell is right-aligned (dot last), so the box keeps
      // the column's left edge whether or not a chevron sits between.
      className={`mr-auto ${pinned || checked ? 'opacity-100' : REVEAL_ON_HOVER}`}
    />
  );
}

function SnoozeOptions({ onSnooze }: { onSnooze: (minutes: number) => void }) {
  const close = useCloseRowMenu();
  return (
    <>
      <div className="px-2.5 pt-1 text-xs text-slate-500">Snooze alerts</div>
      <div className="flex gap-1 px-1.5 pb-1 pt-1.5">
        {[
          { label: '1h', minutes: 60 },
          { label: '4h', minutes: 240 },
          { label: '24h', minutes: 1440 },
        ].map(({ label, minutes }) => (
          <Button
            key={label}
            type="button"
            variant="ghost"
            size="xs"
            className="flex-1 !px-0"
            onClick={() => {
              close();
              onSnooze(minutes);
            }}
          >
            {label}
          </Button>
        ))}
      </div>
    </>
  );
}

function MonitorActionsMenu({
  monitor,
  onToggleEnabled,
  monitorId,
  onDelete,
  onSnooze,
}: {
  monitor: Monitor;
  onToggleEnabled: (monitor: Monitor) => void;
  monitorId: string;
  onDelete: () => void;
  onSnooze: (monitor: Monitor, durationMinutes: number) => void;
}) {
  // Cloning, pausing or resuming a SIP test call is a tenant admin's: the
  // API refuses anyone else, since each check places a real call.
  const isTenantAdmin = useIsTenantAdmin();
  const callLocked = isSipCall(monitor.type, monitor.config) && !isTenantAdmin;
  return (
    <RowMenu label={`Actions for ${monitor.name}`}>
      <RowMenuItem href={`/monitors/${monitorId}`} icon={<Pencil strokeWidth={1.75} />}>Edit</RowMenuItem>
      {!callLocked && (
        <RowMenuItem href={`/monitors/new?clone=${monitorId}`} icon={<Copy strokeWidth={1.75} />}>Clone</RowMenuItem>
      )}
      {!callLocked && (
        <RowMenuItem
          onSelect={() => onToggleEnabled(monitor)}
          icon={monitor.enabled ? <Pause strokeWidth={1.75} /> : <Play strokeWidth={1.75} />}
        >
          {monitor.enabled ? 'Pause' : 'Resume'}
        </RowMenuItem>
      )}
      <RowMenuSeparator />
      <SnoozeOptions onSnooze={(minutes) => onSnooze(monitor, minutes)} />
      <RowMenuSeparator />
      <RowMenuItem onSelect={onDelete} icon={<Trash2 strokeWidth={1.75} />} danger>Delete</RowMenuItem>
    </RowMenu>
  );
}

// The ⋮ cell. Hover-revealed on pointer layouts, always shown on touch.
export function RowActions(props: Parameters<typeof MonitorActionsMenu>[0]) {
  return (
    <div
      role="cell"
      className="flex justify-end opacity-100 transition-opacity focus-within:opacity-100 md:opacity-0 md:focus-within:opacity-100 md:group-hover:opacity-100 md:group-has-[.row-link:focus-visible]:opacity-100"
    >
      <MonitorActionsMenu {...props} />
    </div>
  );
}
