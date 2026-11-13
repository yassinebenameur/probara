'use client';

import { useContext } from 'react';
import Link from 'next/link';
import TypeBadge from '@/components/ui/TypeBadge';
import { flashStyle } from '@/lib/useRowFlash';
import { Monitor, CheckResult } from '@/lib/types';
import { getEffectiveMonitorStatus, getHTTPDisplayValues } from '@/lib/monitor-utils';
import { MONITOR_STATUS_TONE } from '@/lib/status-tones';
import { useRowLayout, rowStateClass, RowFlashContext, handleRowLinkClick } from '@/components/monitors/list/row-layout';
import { StatusDot, StatePill, TargetLine, TargetCell, TagList, RowMetrics, DisplayValueChips } from '@/components/monitors/list/RowCells';
import { SelectCheckbox, RowActions } from '@/components/monitors/list/RowActions';

// Compact monitor row component
export function MonitorRow({
  monitor,
  results,
  isSelected,
  onClick,
  onDelete,
  onToggleEnabled,
  onSnooze,
  isChecked,
  onToggleSelect,
  selectionMode,
}: {
  monitor: Monitor;
  results: CheckResult[];
  isSelected: boolean;
  onClick: () => void;
  onDelete: () => void;
  onToggleEnabled: (monitor: Monitor) => void;
  onSnooze: (monitor: Monitor, durationMinutes: number) => void;
  isChecked: boolean;
  onToggleSelect: () => void;
  selectionMode: boolean;
}) {
  const layout = useRowLayout();
  const flashClass = useContext(RowFlashContext);
  const status = getEffectiveMonitorStatus(monitor, results);
  const displayValues = getHTTPDisplayValues(results);

  return (
    <div
      role="row"
      onClick={onClick}
      style={flashStyle(MONITOR_STATUS_TONE[status])}
      className={`group ${layout.cols} cursor-pointer items-center gap-x-3 px-4 py-1.5 transition-colors ${rowStateClass(isSelected, isChecked)} ${flashClass(monitor.id)}`}
    >
      {/* Lead: [checkbox] [chevron slot] dot */}
      <div role="cell" className="flex items-center justify-end gap-2">
        <SelectCheckbox
          checked={isChecked}
          onChange={onToggleSelect}
          pinned={selectionMode}
          label={`Select ${monitor.name}`}
        />
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
          <DisplayValueChips values={displayValues} />
          <TagList tags={monitor.tags} max={3} />
        </div>
        <div className="mt-1 flex h-4 min-w-0 items-center gap-2">
          <TypeBadge type={monitor.type} config={monitor.config} size="sm" />
          <span className={`flex min-w-0 items-center ${layout.targetInline}`}>
            <TargetLine monitor={monitor} />
          </span>
        </div>
      </div>

      <TargetCell monitor={monitor} />

      <RowMetrics results={results} paused={status === 'paused'} />

      <RowActions
        monitor={monitor}
        monitorId={monitor.id}
        onToggleEnabled={onToggleEnabled}
        onSnooze={onSnooze}
        onDelete={onDelete}
      />
    </div>
  );
}
