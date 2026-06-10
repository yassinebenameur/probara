'use client';

import { useMemo, useReducer } from 'react';
import type { Monitor, MonitorType, StatusPage } from '@/lib/types';

export interface EditableMonitor {
  monitor_id: string;
  display_name: string;
}

export interface EditableSection {
  id: string;
  title: string;
  monitors: EditableMonitor[];
}

export interface Basics {
  slug: string;
  title: string;
  description: string;
  logo_url: string;
  primary_color: string;
  secondary_color: string;
}

export type StatusFilter = 'all' | 'active' | 'paused';

export interface Filters {
  search: string;
  tag: string | null;
  type: MonitorType | null;
  status: StatusFilter;
}

export interface FormState {
  basics: Basics;
  sections: EditableSection[];
  filters: Filters;
  selection: string[];
}

export const DEFAULT_PRIMARY = '#22d3ee';
export const DEFAULT_SECONDARY = '#64748b';
export const DEFAULT_SECTION_TITLE = 'Services';

export function newSectionId(): string {
  return `section-${Math.random().toString(36).slice(2, 10)}`;
}

function makeSection(title = 'New Section', id: string = newSectionId()): EditableSection {
  return { id, title, monitors: [] };
}

function emptyBasics(): Basics {
  return {
    slug: '',
    title: '',
    description: '',
    logo_url: '',
    primary_color: DEFAULT_PRIMARY,
    secondary_color: DEFAULT_SECONDARY,
  };
}

function basicsFromStatusPage(statusPage?: StatusPage): Basics {
  if (!statusPage) return emptyBasics();
  return {
    slug: statusPage.slug || '',
    title: statusPage.title || '',
    description: statusPage.description || '',
    logo_url: statusPage.logo_url || '',
    primary_color: statusPage.primary_color || DEFAULT_PRIMARY,
    secondary_color: statusPage.secondary_color || DEFAULT_SECONDARY,
  };
}

function sectionsFromStatusPage(statusPage?: StatusPage): EditableSection[] {
  if (statusPage?.sections?.length) {
    return statusPage.sections.map((section, index) => ({
      id: section.id || `section-${index}`,
      title: section.title || `Section ${index + 1}`,
      monitors: (section.monitors || []).map((monitor) => ({
        monitor_id: monitor.monitor_id,
        display_name: monitor.display_name || '',
      })),
    }));
  }
  if (statusPage?.monitor_ids?.length) {
    return [
      {
        id: 'legacy-services',
        title: DEFAULT_SECTION_TITLE,
        monitors: statusPage.monitor_ids.map((monitor_id) => ({ monitor_id, display_name: '' })),
      },
    ];
  }
  return [makeSection(DEFAULT_SECTION_TITLE)];
}

export function initialState(statusPage?: StatusPage): FormState {
  return {
    basics: basicsFromStatusPage(statusPage),
    sections: sectionsFromStatusPage(statusPage),
    filters: { search: '', tag: null, type: null, status: 'all' },
    selection: [],
  };
}

function removeMonitorEverywhere(sections: EditableSection[], monitorId: string): EditableSection[] {
  return sections.map((section) => ({
    ...section,
    monitors: section.monitors.filter((m) => m.monitor_id !== monitorId),
  }));
}

function clampIndex(index: number, length: number): number {
  if (length <= 0) return 0;
  if (index < 0) return 0;
  if (index > length) return length;
  return index;
}

export type Action =
  | { type: 'reset'; statusPage?: StatusPage }
  | { type: 'set_basic'; key: keyof Basics; value: string }
  | { type: 'set_filter'; key: keyof Filters; value: string | null }
  | { type: 'toggle_selection'; monitorId: string }
  | { type: 'set_selection'; ids: string[] }
  | { type: 'clear_selection' }
  | { type: 'add_section'; title?: string; id?: string }
  | { type: 'add_section_with_monitors'; id: string; title: string; monitorIds: string[] }
  | { type: 'rename_section'; sectionId: string; title: string }
  | { type: 'remove_section'; sectionId: string }
  | { type: 'reorder_section'; sectionId: string; direction: 'up' | 'down' }
  | { type: 'move_section_to'; sectionId: string; targetIndex: number }
  | { type: 'add_monitor_to_section'; sectionId: string; monitorIds: string[] }
  | { type: 'remove_monitor'; monitorId: string }
  | { type: 'move_monitor'; monitorId: string; targetSectionId: string; targetIndex?: number }
  | { type: 'reorder_monitor'; sectionId: string; monitorId: string; direction: 'up' | 'down' }
  | { type: 'set_display_name'; sectionId: string; monitorId: string; value: string };

export function reducer(state: FormState, action: Action): FormState {
  switch (action.type) {
    case 'reset':
      return initialState(action.statusPage);

    case 'set_basic':
      return { ...state, basics: { ...state.basics, [action.key]: action.value } };

    case 'set_filter':
      return { ...state, filters: { ...state.filters, [action.key]: action.value as never } };

    case 'toggle_selection': {
      const set = new Set(state.selection);
      if (set.has(action.monitorId)) set.delete(action.monitorId);
      else set.add(action.monitorId);
      return { ...state, selection: Array.from(set) };
    }

    case 'set_selection':
      return { ...state, selection: Array.from(new Set(action.ids)) };

    case 'clear_selection':
      return { ...state, selection: [] };

    case 'add_section': {
      const section = makeSection(action.title || 'New Section', action.id);
      return { ...state, sections: [...state.sections, section] };
    }

    case 'add_section_with_monitors': {
      const cleaned = action.monitorIds.reduce(
        (acc, id) => removeMonitorEverywhere(acc, id),
        state.sections,
      );
      const populated: EditableSection = {
        id: action.id,
        title: action.title || 'New Section',
        monitors: action.monitorIds.map((id) => ({ monitor_id: id, display_name: '' })),
      };
      return { ...state, sections: [...cleaned, populated] };
    }

    case 'rename_section':
      return {
        ...state,
        sections: state.sections.map((s) =>
          s.id === action.sectionId ? { ...s, title: action.title } : s,
        ),
      };

    case 'remove_section':
      return { ...state, sections: state.sections.filter((s) => s.id !== action.sectionId) };

    case 'reorder_section': {
      const index = state.sections.findIndex((s) => s.id === action.sectionId);
      if (index === -1) return state;
      const target = action.direction === 'up' ? index - 1 : index + 1;
      if (target < 0 || target >= state.sections.length) return state;
      const next = [...state.sections];
      [next[index], next[target]] = [next[target], next[index]];
      return { ...state, sections: next };
    }

    case 'move_section_to': {
      const sourceIndex = state.sections.findIndex((s) => s.id === action.sectionId);
      if (sourceIndex === -1) return state;
      const next = [...state.sections];
      const [moved] = next.splice(sourceIndex, 1);
      const insertIndex = sourceIndex < action.targetIndex ? action.targetIndex - 1 : action.targetIndex;
      next.splice(clampIndex(insertIndex, next.length), 0, moved);
      return { ...state, sections: next };
    }

    case 'add_monitor_to_section': {
      const idsToAdd = action.monitorIds;
      if (idsToAdd.length === 0) return state;
      // Remove from any existing section first (membership = section assignment)
      let next = state.sections;
      idsToAdd.forEach((id) => {
        next = removeMonitorEverywhere(next, id);
      });
      next = next.map((section) => {
        if (section.id !== action.sectionId) return section;
        const existing = new Set(section.monitors.map((m) => m.monitor_id));
        const appended = idsToAdd
          .filter((id) => !existing.has(id))
          .map((id) => ({ monitor_id: id, display_name: '' }));
        return { ...section, monitors: [...section.monitors, ...appended] };
      });
      return { ...state, sections: next };
    }

    case 'remove_monitor':
      return { ...state, sections: removeMonitorEverywhere(state.sections, action.monitorId) };

    case 'move_monitor': {
      const cleaned = removeMonitorEverywhere(state.sections, action.monitorId);
      const sourceSection = state.sections.find((s) =>
        s.monitors.some((m) => m.monitor_id === action.monitorId),
      );
      const preservedName =
        sourceSection?.monitors.find((m) => m.monitor_id === action.monitorId)?.display_name || '';
      const next = cleaned.map((section) => {
        if (section.id !== action.targetSectionId) return section;
        const insertAt = action.targetIndex === undefined ? section.monitors.length : action.targetIndex;
        const monitors = [...section.monitors];
        monitors.splice(clampIndex(insertAt, monitors.length), 0, {
          monitor_id: action.monitorId,
          display_name: preservedName,
        });
        return { ...section, monitors };
      });
      return { ...state, sections: next };
    }

    case 'reorder_monitor': {
      return {
        ...state,
        sections: state.sections.map((section) => {
          if (section.id !== action.sectionId) return section;
          const index = section.monitors.findIndex((m) => m.monitor_id === action.monitorId);
          if (index === -1) return section;
          const target = action.direction === 'up' ? index - 1 : index + 1;
          if (target < 0 || target >= section.monitors.length) return section;
          const monitors = [...section.monitors];
          [monitors[index], monitors[target]] = [monitors[target], monitors[index]];
          return { ...section, monitors };
        }),
      };
    }

    case 'set_display_name':
      return {
        ...state,
        sections: state.sections.map((section) => {
          if (section.id !== action.sectionId) return section;
          return {
            ...section,
            monitors: section.monitors.map((m) =>
              m.monitor_id === action.monitorId ? { ...m, display_name: action.value } : m,
            ),
          };
        }),
      };

    default:
      return state;
  }
}

export interface MonitorPlacement {
  sectionId: string;
  sectionTitle: string;
}

export interface DerivedState {
  monitorIdToSection: Map<string, MonitorPlacement>;
  membershipCount: number;
  availableTags: string[];
  availableTypes: MonitorType[];
}

export function deriveState(state: FormState, monitors: Monitor[]): DerivedState {
  const map = new Map<string, MonitorPlacement>();
  state.sections.forEach((section) => {
    section.monitors.forEach((m) => {
      map.set(m.monitor_id, { sectionId: section.id, sectionTitle: section.title });
    });
  });

  const tagSet = new Set<string>();
  const typeSet = new Set<MonitorType>();
  monitors.forEach((monitor) => {
    typeSet.add(monitor.type);
    (monitor.tags || []).forEach((tag) => tagSet.add(tag));
  });

  return {
    monitorIdToSection: map,
    membershipCount: map.size,
    availableTags: Array.from(tagSet).sort(),
    availableTypes: Array.from(typeSet).sort(),
  };
}

export function filterMonitors(monitors: Monitor[], filters: Filters): Monitor[] {
  const search = filters.search.trim().toLowerCase();
  const filtered = monitors.filter((monitor) => {
    if (filters.tag && !(monitor.tags || []).includes(filters.tag)) return false;
    if (filters.type && monitor.type !== filters.type) return false;
    if (filters.status === 'active' && !monitor.enabled) return false;
    if (filters.status === 'paused' && monitor.enabled) return false;
    if (search) {
      const haystack = [monitor.name, monitor.type, monitor.url || '', ...(monitor.tags || [])]
        .join(' ')
        .toLowerCase();
      if (!haystack.includes(search)) return false;
    }
    return true;
  });
  // Groups first, then everything alphabetically within each band.
  return filtered.sort((a, b) => {
    const groupRank = (a.type === 'group' ? 0 : 1) - (b.type === 'group' ? 0 : 1);
    if (groupRank !== 0) return groupRank;
    return a.name.localeCompare(b.name, undefined, { sensitivity: 'base' });
  });
}

export function useStatusPageFormState(statusPage?: StatusPage) {
  const [state, dispatch] = useReducer(reducer, statusPage, initialState);
  return { state, dispatch } as const;
}

export function useDerived(state: FormState, monitors: Monitor[]) {
  return useMemo(() => deriveState(state, monitors), [state, monitors]);
}

export function useFilteredMonitors(monitors: Monitor[], filters: Filters) {
  return useMemo(() => filterMonitors(monitors, filters), [monitors, filters]);
}
