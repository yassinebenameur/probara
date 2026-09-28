'use client';

import { useEffect, useState } from 'react';

interface TabsProps<T extends string> {
  tabs: readonly { id: T; label: string }[];
  active: T;
  onChange: (id: T) => void;
}

export default function Tabs<T extends string>({ tabs, active, onChange }: TabsProps<T>) {
  return (
    <div role="tablist" className="flex w-fit max-w-full items-center gap-0.5 overflow-x-auto rounded-xl bg-well p-1 shadow-[inset_0_0_0_1px_var(--glass-edge)]">
      {tabs.map((tab) => (
        <button
          key={tab.id}
          type="button"
          role="tab"
          aria-selected={active === tab.id}
          onClick={() => onChange(tab.id)}
          className={`whitespace-nowrap rounded-lg px-3.5 py-1.5 text-[13px] font-medium transition-colors ${
            active === tab.id
              ? 'bg-glass-strong text-white shadow-glass'
              : 'text-slate-400 hover:text-slate-100'
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
