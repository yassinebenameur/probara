'use client';

import {
  KeyboardEvent,
  ReactNode,
  useCallback,
  useEffect,
  useId,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import { createPortal } from 'react-dom';
import { ChevronDown, Search, X } from 'lucide-react';
import FilterChip, { FILTER_CHIP_SELECTED, FilterChipCount } from './FilterChip';
import Button from './Button';
import { CHECK_GLYPH_CLASS } from './Checkbox';
import { FOCUS_RING_INSET } from './focus';
import { POPOVER_LAYER_ATTR } from './ModalPortal';

export interface FilterOption {
  value: string;
  label: string;
  /** Matches under the page's other filters; 0 dims the row (still selectable). */
  count?: number;
  /** Colour-dot class (a tag hue): drawn in the row and in the chip summary. */
  dotClassName?: string;
}

interface FilterPopoverProps {
  /** Chip label and the popup's name ("Type", "Tags"). */
  label: string;
  /** Singular noun for the clear button ("type" → "Clear type filter"). */
  noun: string;
  /** Plural noun for the search, counts and empty state; defaults to `${noun}s`. */
  nounPlural?: string;
  /** What an option's count counts, for screen readers ("90 monitors"). */
  countUnit?: string;
  icon?: ReactNode;
  options: FilterOption[];
  selected: ReadonlySet<string>;
  onChange: (next: Set<string>) => void;
  /** Show the search field above this many options. */
  searchThreshold?: number;
  className?: string;
  /**
   * The popup's accessible name; defaults to "Filter by {noun}". A picker
   * that sets a value rather than filtering a list (an SLA's tags) names it.
   */
  popupLabel?: string;
  /** The ×'s name; defaults to "Clear {noun} filter". */
  clearLabel?: string;
  /**
   * Always summarise a selection as "Label ⓝ", even one short value: for a
   * caller that lists the selected values beside the chip itself.
   */
  summarizeAsCount?: boolean;
  /**
   * The chip's accessible name when the visible label is ambiguous on its
   * screen (two "Tags" chips in one dialog); the selection is appended.
   */
  accessibleLabel?: string;
  /**
   * One line at the top of the popup saying how the selection combines
   * ("Monitors carrying any selected tag"); also its description.
   */
  hint?: string;
  /** `md` (32px) lines the chip up with RangePicker and buttons in a page header. */
  size?: 'sm' | 'md';
}

const PANEL_WIDTH = 280;
const PANEL_MAX_HEIGHT = 420;
const GAP = 6;
const EDGE = 8;
// Below this viewport width the panel spans the screen under the chip row.
const SHEET_BELOW = 640;
// A single selected value longer than this summarises as a count, like
// several do, so a selected chip's width stays bounded.
const SUMMARY_MAX_CHARS = 10;
const FADE = 28;

// One open filter popover at a time: opening one closes the other.
let closeOpenPopover: (() => void) | null = null;

/**
 * Swallow the mousedown and click that follow a dismissing pointerdown, so
 * closing the popup by clicking the page never also follows a link, presses
 * a button or dismisses a dialog underneath (Linear, Radix): Dialog closes
 * on a backdrop mousedown, so letting that through would close the whole
 * form along with the popup. The mousedown only stops propagating — its
 * default (focusing what was clicked) still runs. Window capture listeners
 * run before React's root listener, so no React handler sees either event.
 * Dropped once the pointer is released, so a drag that never clicks cannot
 * eat a later one.
 */
function swallowFollowingMouseEvents() {
  const stopMouseDown = (e: MouseEvent) => {
    e.stopPropagation();
    window.removeEventListener('mousedown', stopMouseDown, true);
  };
  const swallowClick = (e: MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    cleanup();
  };
  // A mouse's mousedown/click come in the same turn as its pointerup; a
  // tap's compatibility mouse events follow as a separate gesture event, so
  // give those a moment.
  const later = (e: PointerEvent) => window.setTimeout(cleanup, e.pointerType === 'mouse' ? 0 : 350);
  const cleanup = () => {
    window.removeEventListener('mousedown', stopMouseDown, true);
    window.removeEventListener('click', swallowClick, true);
    window.removeEventListener('pointerup', later, true);
    window.removeEventListener('pointercancel', later, true);
  };
  window.addEventListener('mousedown', stopMouseDown, true);
  window.addEventListener('click', swallowClick, true);
  window.addEventListener('pointerup', later, true);
  window.addEventListener('pointercancel', later, true);
}

type Position = {
  top: number;
  left: number;
  width: number;
  maxHeight: number;
  up: boolean;
  /** Fixed while open above the chip, so typing never moves the search field. */
  height?: number;
};

const samePosition = (a: Position | null, b: Position) =>
  !!a && a.top === b.top && a.left === b.left && a.width === b.width && a.maxHeight === b.maxHeight &&
  a.up === b.up && a.height === b.height;

/**
 * Anchored multi-select list filter (Type, Tags): a FilterChip trigger that
 * summarises the selection, plus a portaled popup that floats over the page
 * instead of pushing the list down. Every toggle applies at once.
 *
 * The chip shows the value when one short value is selected and "Label ⓝ"
 * otherwise, so its width stays bounded (the filter row never has to wrap)
 * and no value is cut off mid-word; the full list is in its tooltip and
 * accessible name.
 *
 * ARIA: the chip opens a dialog holding a search combobox (when there are
 * more than `searchThreshold` options) that drives a multi-select listbox
 * through aria-activedescendant; with no search field the listbox itself
 * takes focus. Arrows, Home and End move, Enter toggles, Space toggles while
 * the search is empty (so a query can still contain a space). Escape closes
 * back to the chip wherever focus is. Tab past the last control closes and
 * moves on from the chip (to its × when a filter is set); Shift+Tab from
 * the first returns to the chip. Focus leaving the popup closes it; clicking
 * outside closes it and swallows that click (and its mousedown, so a
 * dialog's backdrop under the click stays open).
 *
 * Inside a modal (MonitorMultiSelect and the SLA form's tag pickers) the
 * popup stacks above the dialog (z-60 over its z-50 scrim) and is exempt from
 * its focus trap (POPOVER_LAYER_ATTR); Escape is caught in the capture phase
 * and stopped, so it closes the popup back to the chip and never the dialog.
 *
 * Unlike RowMenu, which closes on scroll, the popup follows its chip on
 * scroll and resize: focusing the search field on a phone scrolls and
 * resizes the viewport, which would close it the moment it opened. It closes
 * only once the chip has left the viewport.
 */
export default function FilterPopover({
  label,
  noun,
  nounPlural = `${noun}s`,
  countUnit = 'monitor',
  icon,
  options,
  selected,
  onChange,
  searchThreshold = 7,
  className = 'shrink-0',
  popupLabel = `Filter by ${noun}`,
  clearLabel = `Clear ${noun} filter`,
  summarizeAsCount = false,
  accessibleLabel,
  hint,
  size = 'sm',
}: FilterPopoverProps) {
  const [open, setOpen] = useState(false);
  const [pos, setPos] = useState<Position | null>(null);
  const [query, setQuery] = useState('');
  const [active, setActive] = useState(0);
  // Keyboard-driven highlight gets a marker bar; a hover fill alone is too
  // faint to find the keyboard by (same rule as RowMenu items).
  const [keyboard, setKeyboard] = useState(false);
  // Selection at open time: those options float to the top, and stay put
  // while the user ticks and unticks.
  const [pinned, setPinned] = useState<ReadonlySet<string>>(new Set());
  // Which ends of the list have more rows scrolled out of view.
  const [edges, setEdges] = useState({ top: false, bottom: false });

  const baseId = useId();
  const dialogId = `${baseId}-dialog`;
  const listId = `${baseId}-list`;
  const hintId = `${baseId}-hint`;
  const optionId = (index: number) => `${baseId}-opt-${index}`;
  const triggerRef = useRef<HTMLButtonElement>(null);
  const wrapRef = useRef<HTMLDivElement>(null);
  const panelRef = useRef<HTMLDivElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  // Height locked for this opening once the panel has opened above the chip.
  const upHeightRef = useRef<number | null>(null);

  const searchable = options.length > searchThreshold;

  const close = useCallback(() => {
    setOpen(false);
    setPos(null);
    if (closeOpenPopover === close) closeOpenPopover = null;
  }, []);
  const closeToTrigger = useCallback(() => {
    close();
    triggerRef.current?.focus();
  }, [close]);

  const openPopover = () => {
    if (closeOpenPopover && closeOpenPopover !== close) closeOpenPopover();
    closeOpenPopover = close;
    upHeightRef.current = null;
    setPinned(new Set(selected));
    setQuery('');
    setActive(0);
    setKeyboard(false);
    setOpen(true);
  };

  useEffect(() => () => {
    if (closeOpenPopover === close) closeOpenPopover = null;
  }, [close]);

  const visible = useMemo(() => {
    const q = query.trim().toLowerCase();
    const matches = q
      ? options.filter((o) => o.label.toLowerCase().includes(q) || o.value.toLowerCase().includes(q))
      : options;
    if (pinned.size === 0) return matches;
    return [...matches.filter((o) => pinned.has(o.value)), ...matches.filter((o) => !pinned.has(o.value))];
  }, [options, query, pinned]);

  const activeIndex = visible.length === 0 ? -1 : Math.min(active, visible.length - 1);

  const place = useCallback(() => {
    const anchor = triggerRef.current?.getBoundingClientRect();
    const panel = panelRef.current;
    if (!anchor || !panel) return;
    const vw = window.innerWidth;
    // The visual viewport excludes an on-screen keyboard.
    const vh = window.visualViewport?.height ?? window.innerHeight;
    if (anchor.bottom < 0 || anchor.top > vh) {
      close();
      return;
    }
    const width = vw < SHEET_BELOW ? vw - EDGE * 2 : Math.min(PANEL_WIDTH, vw - EDGE * 2);
    const left = Math.min(Math.max(EDGE, anchor.left), vw - width - EDGE);
    const below = vh - anchor.bottom - GAP - EDGE;
    const above = anchor.top - GAP - EDGE;
    let next: Position;
    const locked = upHeightRef.current;
    if (locked !== null && locked <= above) {
      // Open above: keep the height (and so the search field) where it
      // opened while a search shrinks the list.
      next = { top: anchor.top - GAP - locked, left, width, maxHeight: locked, up: true, height: locked };
    } else {
      upHeightRef.current = null;
      // scrollHeight is the content box: add the borders so the gap is exact.
      const natural = Math.min(panel.scrollHeight + panel.offsetHeight - panel.clientHeight, PANEL_MAX_HEIGHT);
      const up = natural > below && above > below;
      const maxHeight = Math.min(PANEL_MAX_HEIGHT, up ? above : below);
      const height = Math.min(natural, maxHeight);
      if (up) upHeightRef.current = height;
      next = up
        ? { top: anchor.top - GAP - height, left, width, maxHeight: height, up, height }
        : { top: anchor.bottom + GAP, left, width, maxHeight, up };
    }
    setPos((prev) => (samePosition(prev, next) ? prev : next));
  }, [close]);

  useLayoutEffect(() => {
    if (open) place();
    // Re-measure when the list length changes (a search narrows it).
  }, [open, place, visible.length]);

  const measureEdges = useCallback(() => {
    const list = listRef.current;
    if (!list) return;
    const top = list.scrollTop > 1;
    const bottom = list.scrollTop + list.clientHeight < list.scrollHeight - 1;
    setEdges((prev) => (prev.top === top && prev.bottom === bottom ? prev : { top, bottom }));
  }, []);
  useLayoutEffect(() => {
    if (open) measureEdges();
  }, [open, pos, visible, measureEdges]);

  // Focus the search (or the listbox) once placed; preventScroll keeps the
  // page where it is.
  const placed = pos !== null;
  useEffect(() => {
    if (!placed) return;
    // On touch, focusing the search would raise the on-screen keyboard over
    // the list it filters; focus the list and let a tap open the keyboard.
    const touch = window.matchMedia?.('(pointer: coarse)').matches;
    (touch ? listRef.current : inputRef.current ?? listRef.current)?.focus({ preventScroll: true });
  }, [placed]);

  useEffect(() => {
    if (!open) return;
    let frame = 0;
    const follow = (e: Event) => {
      // The panel's own list scrolling does not move the chip.
      if (e.target instanceof Node && panelRef.current?.contains(e.target)) return;
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(place);
    };
    const onPointerDown = (e: PointerEvent) => {
      const target = e.target as Element;
      if (panelRef.current?.contains(target) || wrapRef.current?.contains(target)) return;
      close();
      // Another filter chip opens its own popup on this click; anything
      // else only dismisses.
      if (!target.closest?.('[data-filter-popover-trigger]')) swallowFollowingMouseEvents();
    };
    // Escape works wherever focus is, and stops here so the page's own
    // Escape handlers (clearing a bulk selection) do not also run.
    const onKeyDown = (e: globalThis.KeyboardEvent) => {
      if (e.key !== 'Escape' || e.isComposing) return;
      e.preventDefault();
      e.stopPropagation();
      closeToTrigger();
    };
    window.addEventListener('scroll', follow, true);
    window.addEventListener('resize', follow);
    window.visualViewport?.addEventListener('resize', follow);
    document.addEventListener('pointerdown', onPointerDown, true);
    window.addEventListener('keydown', onKeyDown, true);
    return () => {
      cancelAnimationFrame(frame);
      window.removeEventListener('scroll', follow, true);
      window.removeEventListener('resize', follow);
      window.visualViewport?.removeEventListener('resize', follow);
      document.removeEventListener('pointerdown', onPointerDown, true);
      window.removeEventListener('keydown', onKeyDown, true);
    };
  }, [open, place, close, closeToTrigger]);

  // Keep the active option in view while arrowing through a long list.
  useEffect(() => {
    if (!open || activeIndex < 0) return;
    document.getElementById(optionId(activeIndex))?.scrollIntoView({ block: 'nearest' });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, activeIndex]);

  const toggle = (value: string) => {
    const next = new Set(selected);
    if (next.has(value)) next.delete(value);
    else next.add(value);
    onChange(next);
  };

  const onNavKeyDown = (e: KeyboardEvent<HTMLElement>) => {
    const last = visible.length - 1;
    if (['ArrowDown', 'ArrowUp', 'Home', 'End', 'Enter', ' '].includes(e.key)) setKeyboard(true);
    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault();
        setActive(activeIndex >= last ? 0 : activeIndex + 1);
        return;
      case 'ArrowUp':
        e.preventDefault();
        setActive(activeIndex <= 0 ? last : activeIndex - 1);
        return;
      case 'Home':
        // In the search field Home/End keep moving the caret once typed in.
        if (e.currentTarget === inputRef.current && query) return;
        e.preventDefault();
        setActive(0);
        return;
      case 'End':
        if (e.currentTarget === inputRef.current && query) return;
        e.preventDefault();
        setActive(last);
        return;
      case 'Enter':
        e.preventDefault();
        if (activeIndex >= 0) toggle(visible[activeIndex].value);
        return;
      case ' ':
        if (e.currentTarget === inputRef.current && query) return;
        e.preventDefault();
        if (activeIndex >= 0) toggle(visible[activeIndex].value);
        return;
    }
  };

  // The listbox has focus without a search field, and on touch (or after a
  // click on its scrollbar) even with one: it navigates the same way, and a
  // typed character moves to the search field, which then receives it.
  const onListKeyDown = (e: KeyboardEvent<HTMLElement>) => {
    if (searchable && e.key.length === 1 && e.key !== ' ' && !e.ctrlKey && !e.metaKey && !e.altKey) {
      inputRef.current?.focus({ preventScroll: true });
      return;
    }
    onNavKeyDown(e);
  };

  const onPanelKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.key !== 'Tab') return;
    const current = document.activeElement as HTMLElement | null;
    // Tab stops only: the listbox is out of the tab order while the search
    // field drives it, and the footer's Clear is skipped while disabled.
    const stops = Array.from(
      panelRef.current?.querySelectorAll<HTMLElement>('input, [role="listbox"], button') ?? []
    ).filter((el) => el === current || (el.tabIndex >= 0 && !(el as HTMLButtonElement).disabled));
    const index = current ? stops.indexOf(current) : -1;
    if (e.shiftKey && index <= 0) {
      e.preventDefault();
      closeToTrigger();
    } else if (!e.shiftKey && index === stops.length - 1) {
      // Focus the chip and let the browser's Tab carry on from there: to the
      // chip's × when a filter is set, else whatever follows the chip.
      closeToTrigger();
    }
  };

  const byValue = useMemo(() => new Map(options.map((o) => [o.value, o])), [options]);
  // Summarise in option order (most common first), not click order; values
  // no option carries (a stale URL tag) go last.
  const selectedList = [
    ...options.filter((o) => selected.has(o.value)).map((o) => o.value),
    ...Array.from(selected).filter((v) => !byValue.has(v)),
  ];
  const hasSelection = selectedList.length > 0;
  const selectedLabels = selectedList.map((v) => byValue.get(v)?.label ?? v);
  const single =
    !summarizeAsCount && selectedList.length === 1 && selectedLabels[0].length <= SUMMARY_MAX_CHARS
      ? { label: selectedLabels[0], dotClassName: byValue.get(selectedList[0])?.dotClassName }
      : undefined;

  const searching = query.trim() !== '';
  const footerText = [
    searching
      ? `${visible.length} of ${options.length} ${nounPlural}`
      : hasSelection
        ? null
        : `${options.length} ${options.length === 1 ? noun : nounPlural}`,
    hasSelection ? `${selectedList.length} selected` : null,
  ]
    .filter(Boolean)
    .join(' · ');

  const fadeMask =
    edges.top || edges.bottom
      ? `linear-gradient(to bottom, ${edges.top ? 'transparent' : '#000'} 0, #000 ${FADE}px, #000 calc(100% - ${FADE}px), ${
          edges.bottom ? 'transparent' : '#000'
        } 100%)`
      : undefined;

  return (
    <div ref={wrapRef} className={`inline-flex items-center ${className}`}>
      <FilterChip
        ref={triggerRef}
        data-filter-popover-trigger=""
        selected={hasSelection}
        insetFocus={hasSelection}
        icon={icon}
        onClick={() => (open ? close() : openPopover())}
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-controls={open ? dialogId : undefined}
        title={hasSelection ? `${label}: ${selectedLabels.join(', ')}` : undefined}
        aria-label={
          accessibleLabel
            ? hasSelection
              ? `${accessibleLabel}: ${selectedLabels.join(', ')}`
              : accessibleLabel
            : undefined
        }
        className={`${size === 'md' ? '!h-8' : ''} ${hasSelection ? 'rounded-r-none border-r-0 pr-2' : ''}`.trim()}
      >
        <span className="flex items-center gap-1">
          {single ? (
            // One short value: show it whole. The chip's icon names the
            // filter, so the "Type:" prefix is for screen readers (and the
            // tooltip).
            <>
              <span className="sr-only">{label}: </span>
              {single.dotClassName && (
                <span className={`h-1.5 w-1.5 shrink-0 rounded-full ${single.dotClassName}`} aria-hidden="true" />
              )}
              {single.label}
            </>
          ) : hasSelection ? (
            // Several (or one long one): the filter's name and a count.
            <>
              {label}
              <FilterChipCount count={selectedList.length} selected />
              <span className="sr-only"> selected: {selectedLabels.join(', ')}</span>
            </>
          ) : (
            label
          )}
          <ChevronDown
            className={`h-3 w-3 shrink-0 transition-transform duration-fast ${open ? 'rotate-180' : ''}`}
            strokeWidth={1.75}
            aria-hidden="true"
          />
        </span>
      </FilterChip>
      {hasSelection && (
        <button
          type="button"
          aria-label={clearLabel}
          title={clearLabel}
          onClick={() => {
            onChange(new Set());
            // This button unmounts with the selection: keep focus on the chip.
            if (open) close();
            triggerRef.current?.focus();
          }}
          // The chip's right half: same fill and outline, split by a hairline.
          className={`relative inline-flex ${size === 'md' ? 'h-8 w-8' : 'h-7 w-7'} shrink-0 items-center justify-center rounded-r-full border border-l-0 pr-0.5 transition-colors duration-fast before:absolute before:inset-y-1.5 before:left-0 before:w-px before:bg-current before:opacity-25 before:content-[''] ${FILTER_CHIP_SELECTED} ${FOCUS_RING_INSET}`}
        >
          <X className="h-3 w-3" strokeWidth={2} aria-hidden="true" />
        </button>
      )}
      {open &&
        createPortal(
          <div
            ref={panelRef}
            id={dialogId}
            role="dialog"
            aria-label={popupLabel}
            aria-describedby={hint ? hintId : undefined}
            {...{ [POPOVER_LAYER_ATTR]: '' }}
            className={`glass-overlay fixed z-[60] flex flex-col overflow-hidden !rounded-xl ${
              pos ? `animate-pop-in ${pos.up ? 'origin-bottom-left' : 'origin-top-left'}` : ''
            }`}
            style={{
              top: pos?.top ?? 0,
              left: pos?.left ?? 0,
              width: pos?.width ?? PANEL_WIDTH,
              maxHeight: pos?.maxHeight ?? PANEL_MAX_HEIGHT,
              height: pos?.height,
              visibility: pos ? 'visible' : 'hidden',
            }}
            // A click on a non-focusable part (footer text, padding) must not
            // take focus off the search field, or the keys stop working.
            onMouseDown={(e) => {
              if (!(e.target as Element).closest('input, button')) e.preventDefault();
            }}
            onKeyDown={onPanelKeyDown}
            onBlur={(e) => {
              // Focus moved somewhere else on the page: close. A null target
              // (window switch) keeps it open.
              const next = e.relatedTarget as Node | null;
              if (!next || panelRef.current?.contains(next) || wrapRef.current?.contains(next)) return;
              close();
            }}
          >
            {hint && (
              <p id={hintId} className="shrink-0 border-b border-divider px-2.5 py-1.5 text-xs text-slate-500">
                {hint}
              </p>
            )}
            {searchable && (
              <div className="relative shrink-0 border-b border-divider p-2">
                <Search
                  className="pointer-events-none absolute left-4 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-slate-500"
                  strokeWidth={1.75}
                  aria-hidden="true"
                />
                <input
                  ref={inputRef}
                  type="text"
                  role="combobox"
                  aria-expanded="true"
                  aria-controls={listId}
                  aria-autocomplete="list"
                  aria-activedescendant={activeIndex >= 0 ? optionId(activeIndex) : undefined}
                  aria-label={`Search ${nounPlural}`}
                  placeholder={`Search ${nounPlural}…`}
                  autoComplete="off"
                  spellCheck={false}
                  value={query}
                  onChange={(e) => {
                    setQuery(e.target.value);
                    setActive(0);
                  }}
                  onKeyDown={onNavKeyDown}
                  className="input input-sm h-8 py-0 !pl-8"
                />
              </div>
            )}
            <div className="flex min-h-0 flex-1 flex-col">
              <div
                ref={listRef}
                id={listId}
                role="listbox"
                aria-multiselectable="true"
                aria-label={label}
                // In the tab order only without a search field; with one, the
                // input owns focus and points into the list.
                tabIndex={searchable ? -1 : 0}
                aria-activedescendant={activeIndex >= 0 ? optionId(activeIndex) : undefined}
                onKeyDown={onListKeyDown}
                onScroll={measureEdges}
                // Fades mark the ends that scroll; scroll-padding keeps the
                // arrowed-to row clear of them.
                style={{ maskImage: fadeMask, WebkitMaskImage: fadeMask }}
                className={`min-h-0 flex-1 scroll-py-7 overflow-y-auto overscroll-contain focus:outline-none ${
                  visible.length ? 'p-1' : ''
                }`}
              >
                {visible.map((option, index) => {
                  const isSelected = selected.has(option.value);
                  const isActive = index === activeIndex;
                  const empty = option.count === 0 && !isSelected;
                  return (
                    <div
                      key={option.value}
                      id={optionId(index)}
                      role="option"
                      aria-selected={isSelected}
                      onMouseMove={() => {
                        if (index !== activeIndex) setActive(index);
                        if (keyboard) setKeyboard(false);
                      }}
                      onClick={() => {
                        setActive(index);
                        toggle(option.value);
                      }}
                      // The keyboard row gets a neutral marker bar, not a
                      // second orange ring next to the focused search field.
                      className={`flex min-h-[32px] cursor-pointer select-none items-center gap-2.5 rounded-lg px-2.5 text-ui ${
                        empty ? 'text-slate-500' : 'text-slate-300'
                      } ${isActive ? 'bg-well-hover text-white' : ''} ${
                        isActive && keyboard ? 'shadow-[inset_2px_0_0_currentColor]' : ''
                      }`}
                    >
                      <CheckBox checked={isSelected} />
                      {option.dotClassName && (
                        <span className={`h-1.5 w-1.5 shrink-0 rounded-full ${option.dotClassName}`} aria-hidden="true" />
                      )}
                      <span className="min-w-0 flex-1 truncate">{option.label}</span>
                      {typeof option.count === 'number' && (
                        <span className="shrink-0 text-xs tabular-nums text-slate-500">
                          {option.count}
                          <span className="sr-only"> {option.count === 1 ? countUnit : `${countUnit}s`}</span>
                        </span>
                      )}
                    </div>
                  );
                })}
              </div>
              {visible.length === 0 && (
                // Outside the listbox (only options belong in it); the
                // footer's live "0 of N" announces it.
                <p className="px-2.5 py-6 text-center text-xs text-slate-500">
                  No {nounPlural} match “{query.trim()}”
                </p>
              )}
            </div>
            <div className="flex shrink-0 items-center justify-between gap-2 border-t border-divider px-2.5 py-1.5">
              <span className="text-xs tabular-nums text-slate-500" aria-live="polite">
                {footerText}
              </span>
              <Button
                variant="subtle"
                size="xs"
                disabled={!hasSelection}
                onClick={() => {
                  onChange(new Set());
                  (inputRef.current ?? listRef.current)?.focus({ preventScroll: true });
                }}
              >
                Clear
              </Button>
            </div>
          </div>,
          document.body
        )}
    </div>
  );
}

// Decorative tick box: the option's aria-selected carries the state, so this
// draws Checkbox's checked/unchecked look without a nested focusable input.
function CheckBox({ checked }: { checked: boolean }) {
  return (
    <span
      aria-hidden="true"
      className={`relative inline-flex h-3.5 w-3.5 shrink-0 rounded-[4px] border transition-[background-color,border-color] duration-fast ease-standard ${
        checked ? 'border-white bg-white' : 'border-[var(--input-edge,var(--control-edge-strong))]'
      }`}
    >
      {checked && (
        <svg
          className={`${CHECK_GLYPH_CLASS} h-2 w-2 animate-pop-in`}
          fill="none"
          viewBox="0 0 24 24"
          stroke="currentColor"
          strokeWidth={3.5}
        >
          <path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" />
        </svg>
      )}
    </span>
  );
}
