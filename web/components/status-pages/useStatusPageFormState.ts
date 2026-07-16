'use client';

import { useMemo, useReducer } from 'react';
import type { Monitor, StatusPage } from '@/lib/types';

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

export interface FormState {
  basics: Basics;
  sections: EditableSection[];
}

export const DEFAULT_PRIMARY = '#22d3ee';
export const DEFAULT_SECONDARY = '#64748b';
export const DEFAULT_SECTION_TITLE = 'Services';

export function newSectionId(): string {
  return `section-${Math.random().toString(36).slice(2, 10)}`;
}

function makeSection(title = 'New section', id: string = newSectionId()): EditableSection {
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
  | { type: 'add_section'; title?: string; id?: string }
  | { type: 'rename_section'; sectionId: string; title: string }
  | { type: 'remove_section'; sectionId: string }
  | { type: 'reorder_section'; sectionId: string; direction: 'up' | 'down' }
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

    case 'add_section': {
      const section = makeSection(action.title || 'New section', action.id);
      return { ...state, sections: [...state.sections, section] };
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
}

export function deriveState(state: FormState): DerivedState {
  const map = new Map<string, MonitorPlacement>();
  state.sections.forEach((section) => {
    section.monitors.forEach((m) => {
      map.set(m.monitor_id, { sectionId: section.id, sectionTitle: section.title });
    });
  });
  return { monitorIdToSection: map, membershipCount: map.size };
}

/** Case-insensitive substring match over name, type, url and tags. */
export function monitorMatches(monitor: Monitor, needle: string): boolean {
  if (!needle) return true;
  const haystack = [monitor.name, monitor.type, monitor.url || '', ...(monitor.tags || [])]
    .join(' ')
    .toLowerCase();
  return haystack.includes(needle);
}

/** Groups first, then alphabetical within each band. */
export function sortMonitors(monitors: Monitor[]): Monitor[] {
  return [...monitors].sort((a, b) => {
    const groupRank = (a.type === 'group' ? 0 : 1) - (b.type === 'group' ? 0 : 1);
    if (groupRank !== 0) return groupRank;
    return a.name.localeCompare(b.name, undefined, { sensitivity: 'base' });
  });
}

export function useStatusPageFormState(statusPage?: StatusPage) {
  const [state, dispatch] = useReducer(reducer, statusPage, initialState);
  return { state, dispatch } as const;
}

export function useDerived(state: FormState) {
  return useMemo(() => deriveState(state), [state]);
}
