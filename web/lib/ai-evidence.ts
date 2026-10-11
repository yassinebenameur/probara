// Analysis metadata cites monitors by ID (the model never sees names, and
// stored text must survive renames); the API resolves current names on read
// into `monitor_names`. These helpers put the names back for display.

const UUID = /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/g;

/** Replaces each monitor ID in a note with its name; other IDs (mesh alerts, locations, purged monitors) stay. */
export function withMonitorNames(text: string, names?: Record<string, string>): string {
  if (!names) return text;
  return text.replace(UUID, (id) => names[id] ?? id);
}

/** "<monitor id>/<facet>" as "<monitor name> · <facet>", or the raw ref when the monitor is unknown. */
export function evidenceRefLabel(ref: string, monitorRef: string | undefined, names?: Record<string, string>): string {
  const name = monitorRef ? names?.[monitorRef] : undefined;
  if (!name || !monitorRef || !ref.startsWith(monitorRef)) return ref;
  const facet = ref.slice(monitorRef.length).replace(/^\//, '');
  return facet ? `${name} · ${facet}` : name;
}
