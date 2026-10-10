'use client';

import { useEffect, useState } from 'react';
import { getAlertChannelPlugins } from '@/lib/api';

// Channel rows show the plugin's display name ("Slack", "Microsoft Teams"),
// not its type key ("slack", "teams"). Manifests are static per deployment,
// so one fetch per page load is shared by every list and picker.
let cache: Record<string, string> | null = null;
let inflight: Promise<Record<string, string>> | null = null;

function loadPluginNames(): Promise<Record<string, string>> {
  if (cache) return Promise.resolve(cache);
  if (!inflight) {
    inflight = getAlertChannelPlugins()
      .then((manifests) => {
        cache = Object.fromEntries((manifests || []).map((m) => [m.type, m.display_name || m.type]));
        return cache;
      })
      .catch(() => {
        inflight = null;
        return {};
      });
  }
  return inflight;
}

/** Fallback while manifests load (or when a type has none): `slack` → `Slack`. */
export function fallbackPluginName(type: string): string {
  return type ? type.charAt(0).toUpperCase() + type.slice(1) : type;
}

export function usePluginNames(): (type: string) => string {
  const [names, setNames] = useState<Record<string, string>>(cache ?? {});
  useEffect(() => {
    if (cache) return;
    let cancelled = false;
    loadPluginNames().then((loaded) => {
      if (!cancelled) setNames(loaded);
    });
    return () => {
      cancelled = true;
    };
  }, []);
  return (type: string) => names[type] ?? fallbackPluginName(type);
}
