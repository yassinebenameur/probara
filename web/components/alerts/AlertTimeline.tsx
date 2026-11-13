'use client';

import { CSSProperties, KeyboardEvent, useMemo, useState } from 'react';
import { ChevronRight } from 'lucide-react';
import {
  groupMarkers,
  groupSegments,
  segmentBox,
  timelineTicks,
  type Timeline,
  type TimelineLane,
  type TimelineSegment,
  type TimelineTone,
  type TimelineWindow,
} from '@/lib/alert-timeline';
import { formatClock, formatDate, pluralize } from '@/lib/format';
import { TIMELINE_DOT, TIMELINE_LINE, TIMELINE_TONE_LABEL } from '@/lib/status-tones';
import Button from '@/components/ui/Button';
import { ElapsedTime } from '@/components/ui/RelativeTime';
import { FOCUS_RING_INSET } from '@/components/ui/focus';

interface AlertTimelineProps {
  timeline: Timeline;
  window: TimelineWindow;
  /** "last 24 hours": the range in running text. */
  rangeLabel: string;
  /** The alert the list has open; its lane is highlighted. */
  selectedId: string | null;
  /** A lane was chosen: the page opens that alert in the list. */
  onSelect: (alertId: string) => void;
  /** Monitors in the workspace, for "23 of 160 monitors alerted". */
  monitorCount?: number;
  /**
   * Resolved alerts older than this (epoch ms) were not loaded: that span is
   * hatched, so a partly loaded window never reads as a quiet one.
   */
  coveredFrom?: number | null;
}

const OPEN_GROUP = 'open';
const LEGEND: TimelineTone[] = ['active', 'acknowledged', 'suppressed', 'resolved'];

// Label and duration columns are fixed so the overlays (gridlines, the now
// line) can be placed over the track with one calc().
const COLUMNS = 'grid grid-cols-[var(--tl-label)_minmax(0,1fr)_var(--tl-dur)] items-center';
const overTrack = (fraction: number) =>
  `calc(var(--tl-label) + (100% - var(--tl-label) - var(--tl-dur)) * ${fraction.toFixed(5)})`;

function Lines({ segments, window, faded }: { segments: readonly TimelineSegment[]; window: TimelineWindow; faded?: boolean }) {
  return (
    <>
      {segments.map((segment) => {
        const box = segmentBox(segment, window);
        return (
          <span
            key={`${segment.alertId}:${segment.tone}:${segment.from}`}
            className={`absolute top-1/2 h-[3px] min-w-[2px] -translate-y-1/2 rounded-full ${TIMELINE_LINE[segment.tone]} ${
              faded ? 'opacity-70' : ''
            }`}
            style={{ left: `${box.left * 100}%`, width: `${box.width * 100}%` }}
          />
        );
      })}
    </>
  );
}

// live-ping's halo colour (see the motion note in app/globals.css).
const FIRING_PING = { '--flash': 'rgb(var(--tw-rose-500) / 0.6)' } as CSSProperties;
const DOT = 'absolute top-1/2 h-[7px] w-[7px] -translate-x-1/2 -translate-y-1/2 rounded-full';

/**
 * The /alerts timeline: one thin line per subject that alerted in the
 * window, so a large fleet stays readable. Subjects with an open alert lead
 * (the queue, with the alerts they suppress folded under them); the rest
 * gather into collapsible groups, a collapsed group drawing all its lines on
 * one row. The model is lib/alert-timeline.ts; this only lays it out.
 *
 * Lanes are one roving tab stop (arrows move between them), so the list
 * below is one Tab away however many lanes there are.
 */
export default function AlertTimeline({
  timeline,
  window,
  rangeLabel,
  selectedId,
  onSelect,
  monitorCount,
  coveredFrom,
}: AlertTimelineProps) {
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});
  const [expandAll, setExpandAll] = useState(false);
  const [unfolded, setUnfolded] = useState<ReadonlySet<string>>(() => new Set());
  const ticks = useMemo(() => timelineTicks(window), [window]);

  const isExpanded = (key: string) => expanded[key] ?? (key === OPEN_GROUP ? true : expandAll);
  const toggleGroup = (key: string) => setExpanded((prev) => ({ ...prev, [key]: !isExpanded(key) }));
  const toggleChildren = (key: string) =>
    setUnfolded((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });

  const moveFocus = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return;
    const lanes = Array.from(event.currentTarget.querySelectorAll<HTMLElement>('[data-lane]'));
    const next = lanes[lanes.indexOf(document.activeElement as HTMLElement) + (event.key === 'ArrowDown' ? 1 : -1)];
    if (!next) return;
    event.preventDefault();
    next.focus();
  };

  const hasSelected = (lane: TimelineLane) =>
    selectedId !== null && (lane.focusAlertId === selectedId || lane.segments.some((s) => s.alertId === selectedId));

  // The one lane in the Tab order: the first one shown.
  const firstLaneKey =
    (isExpanded(OPEN_GROUP) ? timeline.open[0]?.key : undefined) ??
    timeline.groups.find((group) => isExpanded(`group:${group.key}`))?.lanes[0]?.key ??
    null;
  const renderLane = (lane: TimelineLane, child = false) => {
    const first = lane.key === firstLaneKey;
    const open = lane.openSince !== null;
    return (
      <div key={lane.key} className={`group/lane relative h-6 ${COLUMNS} ${hasSelected(lane) ? 'bg-well-strong' : ''}`}>
        <button
          type="button"
          data-lane
          tabIndex={first ? 0 : -1}
          onClick={() => onSelect(lane.focusAlertId)}
          aria-label={`${lane.label}, ${pluralize(lane.alertCount, 'alert')}${
            open && lane.openTone ? `, ${TIMELINE_TONE_LABEL[lane.openTone].toLowerCase()}` : ''
          }. Show in the list`}
          className={`absolute inset-0 w-full hover:bg-well ${FOCUS_RING_INSET}`}
        />
        <div className={`pointer-events-none relative flex min-w-0 items-center gap-2 pr-3 text-xs ${child ? 'pl-12 text-slate-400' : 'pl-8 text-slate-200'}`}>
          <span className="truncate">{lane.label}</span>
          {lane.children.length > 0 && (
            <button
              type="button"
              onClick={() => toggleChildren(lane.key)}
              aria-expanded={unfolded.has(lane.key)}
              className={`pointer-events-auto relative shrink-0 rounded-full bg-well-hover px-2 text-2xs text-slate-300 hover:text-white ${FOCUS_RING_INSET}`}
            >
              {unfolded.has(lane.key) ? 'Hide' : `+${lane.children.length} affected`}
            </button>
          )}
        </div>
        <div className="pointer-events-none relative h-full">
          <Lines segments={lane.segments} window={window} />
          {lane.markers.map((marker) => (
            <span
              key={marker.alertId}
              className={`${DOT} ${TIMELINE_DOT[marker.tone]}`}
              style={{ left: `${segmentBox({ from: marker.at, to: marker.at }, window).left * 100}%` }}
            />
          ))}
          {/* The "now" end of an open line; an unacknowledged one pings, as its row in the list does. */}
          {open && lane.openTone && (
            <span
              className={`${DOT} ${TIMELINE_DOT[lane.openTone]} ${lane.openTone === 'active' ? 'animate-live-ping' : ''}`}
              style={{ left: '100%', ...(lane.openTone === 'active' ? FIRING_PING : null) }}
            />
          )}
        </div>
        <div className="pointer-events-none relative pr-3 text-right text-2xs tabular-nums text-slate-300">
          {open && lane.openSince !== null && <ElapsedTime start={new Date(lane.openSince)} />}
        </div>
      </div>
    );
  };

  const renderLanes = (lanes: readonly TimelineLane[]) =>
    lanes.flatMap((lane) => [
      renderLane(lane),
      ...(unfolded.has(lane.key) ? lane.children.map((childLane) => renderLane(childLane, true)) : []),
    ]);

  const renderGroup = (key: string, label: string, meta: string, lanes: readonly TimelineLane[]) => {
    const open = isExpanded(key);
    return (
      <div key={key} className="border-t border-divider first:border-t-0">
        <div className={`relative h-7 ${COLUMNS}`}>
          <button
            type="button"
            onClick={() => toggleGroup(key)}
            aria-expanded={open}
            aria-label={`${label}, ${meta}`}
            className={`absolute inset-0 w-full hover:bg-well ${FOCUS_RING_INSET}`}
          />
          <div className="pointer-events-none relative flex min-w-0 items-center gap-2 pl-3 pr-3 text-xs">
            <ChevronRight
              className={`h-3.5 w-3.5 shrink-0 text-slate-500 transition-transform duration-fast ${open ? 'rotate-90' : ''}`}
              strokeWidth={2}
              aria-hidden="true"
            />
            <span className="truncate font-semibold text-white">{label}</span>
            <span className="shrink-0 text-slate-500">{meta}</span>
          </div>
          <div className="pointer-events-none relative h-full">
            {!open && <Lines segments={groupSegments(lanes)} window={window} faded />}
            {/* Each alert's start, smaller than a lane's: a five-minute alert is under a pixel of line at 30 days. */}
            {!open &&
              groupMarkers(lanes).map((marker) => (
                <span
                  key={marker.alertId}
                  className={`absolute top-1/2 h-[5px] w-[5px] -translate-x-1/2 -translate-y-1/2 rounded-full opacity-70 ${TIMELINE_DOT[marker.tone]}`}
                  style={{ left: `${segmentBox({ from: marker.at, to: marker.at }, window).left * 100}%` }}
                />
              ))}
          </div>
          <div />
        </div>
        {open && renderLanes(lanes)}
      </div>
    );
  };

  const empty = timeline.open.length === 0 && timeline.groups.length === 0;
  const hatch = coveredFrom && coveredFrom > window.start ? segmentBox({ from: window.start, to: coveredFrom }, window) : null;
  const caption =
    monitorCount && monitorCount >= timeline.subjectCount
      ? `${timeline.subjectCount} of ${pluralize(monitorCount, 'monitor')} alerted in the ${rangeLabel}`
      : `${pluralize(timeline.subjectCount, 'subject')} alerted in the ${rangeLabel}`;

  return (
    <section className="glass overflow-hidden" aria-label="Alert timeline">
      <div className="flex flex-wrap items-center justify-between gap-x-6 gap-y-2 px-4 py-3">
        <div className="flex min-w-0 flex-wrap items-baseline gap-x-3">
          <h2 className="text-title font-semibold text-white">Timeline</h2>
          {!empty && <span className="text-xs text-slate-500">{caption}</span>}
        </div>
        <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-slate-400">
          {LEGEND.map((tone) => (
            <span key={tone} className="inline-flex items-center gap-1.5">
              <span className={`h-[3px] w-4 rounded-full ${TIMELINE_LINE[tone]}`} aria-hidden="true" />
              {TIMELINE_TONE_LABEL[tone]}
            </span>
          ))}
          {timeline.groups.length > 0 && (
            <Button
              variant="subtle"
              size="xs"
              onClick={() => {
                setExpandAll(!expandAll);
                setExpanded({});
              }}
            >
              {expandAll ? 'Collapse groups' : 'Expand all'}
            </Button>
          )}
        </div>
      </div>

      {empty ? (
        <p className="border-t border-divider px-4 py-8 text-center text-ui text-slate-400">No alerts in the {rangeLabel}.</p>
      ) : (
        <div className="max-h-[26rem] overflow-auto border-t border-divider" onKeyDown={moveFocus}>
          <div className="relative min-w-[40rem] [--tl-dur:4.25rem] [--tl-label:10rem] sm:[--tl-label:15rem]">
            <div className={`sticky top-0 z-10 h-6 border-b border-divider bg-[rgb(var(--panel-rgb))] ${COLUMNS}`}>
              <div />
              <div className="relative h-full">
                {ticks.map((tick) => (
                  <span
                    key={tick.at}
                    className="absolute top-1 -translate-x-1/2 whitespace-nowrap font-mono text-2xs text-slate-500"
                    style={{ left: `${segmentBox({ from: tick.at, to: tick.at }, window).left * 100}%` }}
                  >
                    {tick.kind === 'day' ? formatDate(tick.at) : formatClock(tick.at)}
                  </span>
                ))}
              </div>
              <div className="pr-3 text-right font-mono text-2xs text-slate-300">now</div>
            </div>

            {timeline.open.length > 0 &&
              renderGroup(OPEN_GROUP, 'Open now', pluralize(timeline.open.length, 'subject'), timeline.open)}
            {timeline.groups.map((group) =>
              renderGroup(
                `group:${group.key}`,
                group.label,
                `${pluralize(group.lanes.length, 'subject')} · ${pluralize(group.alertCount, 'alert')}`,
                group.lanes
              )
            )}

            {ticks.map((tick) => (
              <span
                key={tick.at}
                className="pointer-events-none absolute bottom-0 top-6 w-px bg-divider"
                style={{ left: overTrack(segmentBox({ from: tick.at, to: tick.at }, window).left) }}
                aria-hidden="true"
              />
            ))}
            {hatch && (
              <span
                className="pointer-events-none absolute bottom-0 top-6 bg-[repeating-linear-gradient(135deg,rgb(var(--tw-slate-500)/0.22)_0_4px,transparent_4px_8px)]"
                style={{ left: overTrack(0), width: `calc((100% - var(--tl-label) - var(--tl-dur)) * ${hatch.width.toFixed(5)})` }}
                title="Older resolved alerts are not loaded"
              />
            )}
            <span
              className="pointer-events-none absolute bottom-0 top-6 w-px bg-white/50"
              style={{ left: overTrack(1) }}
              aria-hidden="true"
            />
          </div>
        </div>
      )}
    </section>
  );
}
