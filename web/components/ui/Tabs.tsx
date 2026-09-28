'use client';

import { useEffect, useState } from 'react';

interface TabsProps<T extends string> {
  tabs: readonly { id: T; label: string }[];
  active: T;
  onChange: (id: T) => void;
}

export default function Tabs<T extends string>({ tabs, active, onChange }: TabsProps<T>) {
  return (
    <div className="flex w-fit items-center gap-1 rounded-lg border border-white/[0.06] bg-slate-900/50 p-1">
      {tabs.map((tab) => (
        <button
          key={tab.id}
          type="button"
          onClick={() => onChange(tab.id)}
          className={`rounded-md px-4 py-1.5 text-xs font-medium transition-colors ${
            active === tab.id ? 'bg-white/[0.08] text-white' : 'text-slate-400 hover:text-white'
          }`}
        >
          {tab.label}
        </button>
      ))}
    </div>
  );
}

// Tab selection kept in `?tab=` so a tab can be linked to (and survives a
// reload). The first id is the default and is left out of the URL.
export function useUrlTab<T extends string>(ids: readonly T[]): [T, (id: T) => void] {
  const [tab, setTab] = useState<T>(ids[0]);

  useEffect(() => {
    const requested = new URLSearchParams(window.location.search).get('tab');
    if (requested && (ids as readonly string[]).includes(requested)) setTab(requested as T);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const select = (id: T) => {
    setTab(id);
    const url = new URL(window.location.href);
    if (id === ids[0]) url.searchParams.delete('tab');
    else url.searchParams.set('tab', id);
    window.history.replaceState(null, '', url);
  };

  return [tab, select];
}
