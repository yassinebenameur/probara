// Display helpers for the audit log. The API stores each event's actor label
// at write time, and request-derived events (everything but sign-ins) are
// written without one, so the page resolves a readable name from what else it
// knows: other events by the same actor, and any users it has loaded.

import type { AuditEvent } from './types';

export interface AuditActor {
  /** What the cell shows. */
  label: string;
  /** Full identity for the tooltip (the actor's UUID when there is one). */
  title?: string;
  kind: 'user' | 'key' | 'anonymous';
}

/** actor_id → label, from every event on the page that carries one. */
export function knownActorLabels(events: AuditEvent[]): Map<string, string> {
  const labels = new Map<string, string>();
  for (const event of events) {
    if (event.actor_id && event.actor_label && !labels.has(event.actor_id)) {
      labels.set(event.actor_id, event.actor_label);
    }
  }
  return labels;
}

export function auditActor(event: AuditEvent, names: ReadonlyMap<string, string> = new Map()): AuditActor {
  const id = event.actor_id;
  if (event.actor_type === 'api_key') {
    return { label: event.actor_label || 'API key', title: id ? `API key ${id}` : undefined, kind: 'key' };
  }
  if (event.actor_type === 'anonymous') {
    return { label: event.actor_label || 'Anonymous', kind: 'anonymous' };
  }
  const name = event.actor_label || (id ? names.get(id) : undefined);
  if (name) return { label: name, title: id, kind: 'user' };
  if (id) return { label: `User ${id.slice(0, 8)}`, title: id, kind: 'user' };
  return { label: '—', kind: 'user' };
}

/**
 * The client address without the source port the API sometimes keeps
 * (`[::1]:55907` → `::1`, `10.0.0.5:51234` → `10.0.0.5`). A bare IPv6
 * address has several colons and is left alone.
 */
export function auditIp(ip: string | null | undefined): string {
  const value = (ip ?? '').trim();
  if (!value) return '';
  const bracketed = value.match(/^\[([^\]]+)\](?::\d+)?$/);
  if (bracketed) return bracketed[1];
  const v4WithPort = value.match(/^(\d{1,3}(?:\.\d{1,3}){3}):\d+$/);
  if (v4WithPort) return v4WithPort[1];
  return value;
}
