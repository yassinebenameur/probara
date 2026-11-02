import { pluralize } from './format';
import type { StatusTone } from './status-tones';
import type { MonitorState } from './types';

/** Why the collector skipped a pending namespace on its last scan. */
export type PendingReason = 'forbidden' | 'too_large' | 'inventory_limit' | 'deadline' | 'error';
export interface DiscoveryIntegration {
  id: string; name: string;
  /** Active namespaces: every scan covers them (stored order). */
  namespaces: string[];
  /** Where the collector runs (ServiceAccount, Secret, Deployment); always active. */
  home_namespace: string;
  /** Added but not yet scanned successfully; promoted to `namespaces` by the first scan that reads them. */
  pending_namespaces: string[];
  pending_status: Record<string, { reason: PendingReason; checked_at: string }>;
  location_id: string | null;
  last_seen_at: string | null; revision: number; token?: string;
}
/** One row of GET /discovery/integrations/{id}/namespaces: home first, then active, then pending. */
export interface DiscoveryNamespace {
  namespace: string; state: 'active' | 'pending'; home: boolean;
  reason?: PendingReason | string; checked_at?: string;
  /** Discovered services in the namespace, and how many of them have a monitor link. */
  services: number; linked_services: number;
}
/** PATCH body: a delta, so concurrent edits merge. `location_id` absent keeps it, null clears it. */
export interface DiscoveryIntegrationPatch {
  name?: string; location_id?: string | null; add_namespaces?: string[]; remove_namespaces?: string[];
}
export interface DiscoverySuggestion {
  key: string; type: 'http' | 'tcp'; target: string; port?: number; internal: boolean; reason: string;
}
export interface DiscoveryMonitor {
  id: string; name: string; type: string; enabled: boolean; state: MonitorState;
  fresh: boolean; routed: boolean; suggestion_key: string | null;
}
export interface DiscoveryResource {
  uid: string; kind: string; name: string; missing_since: string | null; suggestions: DiscoverySuggestion[];
}
export interface DiscoveryService {
  id: string; name: string; namespace: string;
  resources: DiscoveryResource[];
  monitors: DiscoveryMonitor[]; gaps: string[]; candidates: Record<string, DiscoveryMonitor[]>;
}
export interface DiscoverySummary {
  services: number; covered: number; with_gaps: number; unmonitored: number;
  /** Services with a resource missing past the grace period (`resource_missing`). */
  missing: number;
  /** Services whose only missing resources are still within the grace period, when the API reports it. */
  missing_grace?: number;
}
export interface DiscoveryCoverage {
  last_seen_at: string | null; inventory_stale: boolean;
  items: DiscoveryService[];
  /** Count after the q/gaps filters. */
  total: number; page: number; page_size?: number;
  /** Over every service of the integration, unfiltered. */
  summary?: DiscoverySummary;
}
export type DiscoverySort = 'name' | 'gaps';
export interface DiscoveryCoverageQuery { page: number; q?: string; gaps?: boolean; sort?: DiscoverySort }

export const DISCOVERY_PAGE_SIZE = 50;

/**
 * Where to go when a page came back empty because the result set shrank under
 * it (rows covered while working through "With gaps", a search narrowed): the
 * last page that still has rows, or null when the response needs no move.
 */
export function pageAfterShrink(page: number, total: number, itemCount: number, pageSize = DISCOVERY_PAGE_SIZE): number | null {
  if (itemCount > 0 || total <= 0 || page <= 1) return null;
  const last = Math.max(1, Math.ceil(total / (pageSize || DISCOVERY_PAGE_SIZE)));
  return last < page ? last : null;
}
/** Mirrors the API's inventory staleness bound. */
export const INVENTORY_STALE_MS = 15 * 60 * 1000;

export function coverageQueryString(query: DiscoveryCoverageQuery): string {
  const params = new URLSearchParams({ page: String(query.page) });
  const q = query.q?.trim();
  if (q) params.set('q', q);
  if (query.gaps) params.set('gaps', 'true');
  if (query.sort && query.sort !== 'name') params.set('sort', query.sort);
  return params.toString();
}

// Gap tones are the shared Pill/status vocabulary (lib/status-tones.ts), narrowed
// to the three a coverage gap can carry; colour classes come from there, never here.
export type GapTone = Extract<StatusTone, 'danger' | 'warning' | 'neutral'>;
export const coverageGaps: Record<string, { short: string; label: string; tone: GapTone }> = {
  inventory_stale: { short: 'Stale inventory', label: 'Inventory has not refreshed in 15 minutes', tone: 'neutral' },
  resource_missing_grace: { short: 'Missing (grace)', label: 'Resource missing — within the 15-minute grace period', tone: 'neutral' },
  resource_missing: { short: 'Missing', label: 'Resource no longer discovered', tone: 'danger' },
  no_enabled_monitor: { short: 'Unmonitored', label: 'No enabled monitor linked', tone: 'warning' },
  freshness_unavailable: { short: 'Group only', label: 'Group freshness is not assessed; link its individual checks', tone: 'neutral' },
  stale_checks: { short: 'No recent results', label: 'A linked monitor has no recent results', tone: 'warning' },
  unrouted: { short: 'No alert route', label: 'A linked monitor has no reachable alert channel', tone: 'warning' },
};
export function gapInfo(code: string) {
  return coverageGaps[code] || { short: code, label: code, tone: 'neutral' as GapTone };
}

export type CollectorHealth = 'waiting' | 'syncing' | 'stale';
export function collectorHealth(lastSeenAt: string | null | undefined, now = Date.now()): CollectorHealth {
  if (!lastSeenAt) return 'waiting';
  return now - new Date(lastSeenAt).getTime() > INVENTORY_STALE_MS ? 'stale' : 'syncing';
}

/** Present, de-duplicated suggestions of a service with the resource they came from. */
export function serviceSuggestions(service: DiscoveryService): { suggestion: DiscoverySuggestion; resource: DiscoveryResource }[] {
  const seen = new Set<string>();
  const out: { suggestion: DiscoverySuggestion; resource: DiscoveryResource }[] = [];
  for (const resource of service.resources) {
    if (resource.missing_since) continue;
    for (const suggestion of resource.suggestions) {
      if (seen.has(suggestion.key)) continue;
      seen.add(suggestion.key);
      out.push({ suggestion, resource });
    }
  }
  return out;
}
/**
 * The linked monitor that covers a suggestion: the one linked for its key, or
 * else one linked to the service without a key (the row-level Link) that
 * checks the same target, i.e. is among the suggestion's candidates.
 */
export function suggestionCover(service: Pick<DiscoveryService, 'monitors' | 'candidates'>, key: string): DiscoveryMonitor | undefined {
  const keyed = service.monitors.find((m) => m.suggestion_key === key);
  if (keyed) return keyed;
  const candidates = service.candidates[key] || [];
  return service.monitors.find((m) => m.suggestion_key === null && candidates.some((c) => c.id === m.id));
}
/** Present suggestions no linked monitor covers yet ("N to review"). */
export function pendingSuggestionCount(service: DiscoveryService): number {
  return serviceSuggestions(service).filter(({ suggestion }) => !suggestionCover(service, suggestion.key)).length;
}
/**
 * Whether linking `monitorId` is a no-op the dialog should refuse: it is
 * already linked to the service, unless a suggestion is being attached to a
 * link that has none yet (the API then sets the link's suggestion key).
 */
export function linkBlocked(service: Pick<DiscoveryService, 'monitors'>, monitorId: string, suggestionKey?: string): boolean {
  const link = service.monitors.find((m) => m.id === monitorId);
  if (!link) return false;
  return !(suggestionKey && link.suggestion_key === null);
}
export function suggestionTarget(s: DiscoverySuggestion): string {
  return s.port ? `${s.target}:${s.port}` : s.target;
}

const NAMESPACE = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/;
export function validNamespace(ns: string): boolean { return ns.length > 0 && ns.length <= 63 && NAMESPACE.test(ns); }
/** The API's per-integration namespace limit. */
export const MAX_NAMESPACES = 20;
/**
 * The namespace field's error: invalid names and/or too many, or undefined when
 * the list is acceptable. Only committed chips are name-checked; the draft
 * still being typed counts toward the limit but is not flagged (`kube-` is on
 * its way to `kube-system`). Callers still refuse an invalid draft on submit.
 */
export function namespaceFieldError(committed: string[], draft: string[] = []): string | undefined {
  const problems: string[] = [];
  const invalid = committed.filter((ns) => !validNamespace(ns));
  if (invalid.length) problems.push(`Not a valid namespace: ${invalid.join(', ')}`);
  const total = new Set([...committed, ...draft]).size;
  if (total > MAX_NAMESPACES) problems.push(`At most ${MAX_NAMESPACES} namespaces — remove ${total - MAX_NAMESPACES}`);
  return problems.length ? problems.join('. ') : undefined;
}

/** An expected HTTP status as typed: a whole number from 100 to 599, else null. */
export function parseExpectedStatus(text: string): number | null {
  const t = text.trim();
  if (!/^\d+$/.test(t)) return null;
  const n = Number(t);
  return n >= 100 && n <= 599 ? n : null;
}
export interface ApproveFormValues {
  name: string; status: string; locationId: string; mode: 'default' | 'custom'; channelIds: string[];
}
/** The approve form's inline errors, keyed by its field names; {} when it can be submitted. */
export function approveFormErrors(values: ApproveFormValues, suggestion: Pick<DiscoverySuggestion, 'type' | 'internal'>): Partial<Record<keyof ApproveFormValues, string>> {
  const errors: Partial<Record<keyof ApproveFormValues, string>> = {};
  if (!values.name.trim()) errors.name = 'Enter a monitor name';
  if (suggestion.type === 'http' && parseExpectedStatus(values.status) === null) errors.status = 'Enter a status between 100 and 599';
  if (suggestion.internal && !values.locationId) errors.locationId = 'Choose a private location that can reach the cluster';
  if (values.mode === 'custom' && values.channelIds.length === 0) errors.channelIds = 'Select at least one channel, or use the workspace defaults';
  return errors;
}
/** Splits free text (commas, whitespace) into namespaces, keeping order and dropping repeats. */
export function parseNamespaces(text: string): string[] {
  return [...new Set(text.split(/[\s,]+/).map((n) => n.trim().toLowerCase()).filter(Boolean))];
}

export function collectorName(integration: Pick<DiscoveryIntegration, 'id'>): string {
  return `probara-discovery-${integration.id.slice(0, 8)}`;
}
export function installCommands(integration: DiscoveryIntegration, context = 'your-cluster') {
  const name = collectorName(integration);
  return {
    file: `${name}.yaml`,
    apply: `kubectl --context ${context} apply -f ${name}.yaml`,
    logs: `kubectl --context ${context} -n ${integration.home_namespace} logs deployment/${name}`,
  };
}

/** Every namespace the collector is asked to read: active first, then pending, without repeats. */
export function configuredNamespaces(integration: Pick<DiscoveryIntegration, 'namespaces' | 'pending_namespaces'>): string[] {
  return [...new Set([...integration.namespaces, ...(integration.pending_namespaces ?? [])])];
}
/** The namespace list with the home namespace first and present: the edit form cannot drop it. */
export function withHomeLocked(home: string, namespaces: string[]): string[] {
  return [home, ...namespaces.filter((ns) => ns !== home)];
}
/**
 * Where the chip at index sits among the chip input's remove buttons, which
 * skip non-removable (locked) chips: after removing it, the button at this
 * position belongs to the chip that took its place.
 */
export function removeButtonIndex(value: string[], index: number, removable: (namespace: string) => boolean): number {
  return value.slice(0, index).filter(removable).length;
}
/**
 * What an edited namespace list adds and removes relative to the stored one
 * (active and pending alike). The home namespace is never removed: removing it
 * is refused by the API, and the form locks its chip.
 */
export function namespaceDelta(integration: Pick<DiscoveryIntegration, 'namespaces' | 'pending_namespaces' | 'home_namespace'>, edited: string[]): { add: string[]; remove: string[] } {
  const current = configuredNamespaces(integration);
  const next = new Set(withHomeLocked(integration.home_namespace, edited));
  const have = new Set(current);
  return {
    add: [...next].filter((ns) => !have.has(ns)),
    remove: current.filter((ns) => !next.has(ns)),
  };
}
export interface IntegrationEditValues { name: string; locationId: string; namespaces: string[] }
/**
 * The PATCH body for an edit: only what changed (a field left out is kept by
 * the API), or null when nothing did. An empty location clears it.
 */
export function integrationPatch(integration: DiscoveryIntegration, values: IntegrationEditValues): DiscoveryIntegrationPatch | null {
  const patch: DiscoveryIntegrationPatch = {};
  const name = values.name.trim();
  if (name !== integration.name) patch.name = name;
  const location = values.locationId || null;
  if (location !== integration.location_id) patch.location_id = location;
  const { add, remove } = namespaceDelta(integration, values.namespaces);
  if (add.length) patch.add_namespaces = add;
  if (remove.length) patch.remove_namespaces = remove;
  return Object.keys(patch).length ? patch : null;
}
/** The edit form's inline errors; {} when it can be submitted. */
export function integrationEditErrors(values: IntegrationEditValues, draft: string[] = []): { name?: string; namespaces?: string } {
  const errors: { name?: string; namespaces?: string } = {};
  if (!values.name.trim()) errors.name = 'Enter a cluster name';
  const namespaces = namespaceFieldError(values.namespaces, draft)
    ?? (draft.some((ns) => !validNamespace(ns)) ? `Not a valid namespace: ${draft.filter((ns) => !validNamespace(ns)).join(', ')}` : undefined);
  if (namespaces) errors.namespaces = namespaces;
  return errors;
}

/** The review step's sentence for an added namespace. */
export const ADDED_NAMESPACE_TEXT = 'Scanned once the collector can read it. Apply the access manifest. Nothing else changes until then.';
/**
 * The review step's sentence for a removed namespace, from its counts in
 * GET …/namespaces (undefined when they could not be loaded).
 */
export function removedNamespaceText(namespace: string, counts?: Pick<DiscoveryNamespace, 'services' | 'linked_services'>): string {
  const untouched = 'Monitors and history are not touched.';
  if (!counts) return `Its services leave coverage now. They and their monitor links are deleted after 7 days unless you add ${namespace} back. ${untouched}`;
  if (counts.services === 0) return `No discovered services leave coverage. ${untouched}`;
  const one = counts.services === 1;
  // linked_services counts services with at least one link, not links: one
  // service can be linked to several monitors.
  const linked = Math.min(counts.linked_services, counts.services);
  const subject = linked === 0 ? (one ? 'It is' : 'They are')
    : linked === counts.services ? `${one ? 'It and its' : 'They and their'} monitor links are`
      : `They and the monitor links of ${linked} of them are`;
  return `${pluralize(counts.services, 'service')} ${one ? 'leaves' : 'leave'} coverage now. ${subject} deleted after 7 days unless you add ${namespace} back. ${untouched}`;
}

export const pendingReasons: Record<PendingReason, { short: string; label: string; tone: GapTone }> = {
  forbidden: { short: 'Can’t read', label: 'Can’t read: the namespace doesn’t exist or the access manifest isn’t applied', tone: 'warning' },
  too_large: { short: 'Too large', label: 'Too large to scan: over the per-scan limit of 2,000 resources, or a 4 MiB list page or upload', tone: 'warning' },
  inventory_limit: { short: 'Inventory full', label: 'Not added: its resources would exceed the cluster’s 10,000 stored resources', tone: 'warning' },
  deadline: { short: 'Not reached', label: 'Not reached: the scan ran out of time before this namespace', tone: 'neutral' },
  error: { short: 'Scan failed', label: 'Scan failed: the collector could not list it; check its logs', tone: 'warning' },
};
export function pendingReasonInfo(reason: string) {
  return pendingReasons[reason as PendingReason] || { short: reason, label: reason, tone: 'neutral' as GapTone };
}
/**
 * The namespace rows to show: the API's list when loaded, else the same
 * shape from the integration itself (no counts), in the API's order: home,
 * then active in stored order, then pending.
 */
export function namespaceRows(integration: DiscoveryIntegration, items?: DiscoveryNamespace[] | null): (Omit<DiscoveryNamespace, 'services' | 'linked_services'> & Partial<Pick<DiscoveryNamespace, 'services' | 'linked_services'>>)[] {
  if (items) return items;
  const home = integration.home_namespace;
  const active = withHomeLocked(home, integration.namespaces).map((namespace) => ({ namespace, state: 'active' as const, home: namespace === home }));
  const pending = (integration.pending_namespaces ?? []).filter((ns) => !integration.namespaces.includes(ns)).map((namespace) => {
    const status = integration.pending_status?.[namespace];
    return { namespace, state: 'pending' as const, home: false, reason: status?.reason, checked_at: status?.checked_at };
  });
  return [...active, ...pending];
}
/**
 * A namespace pill's tone and short detail: active with its service count (an
 * active namespace with none after a scan stands out: a typo under broad RBAC
 * reads the same), pending waiting for its first scan, or skipped with why.
 */
export function namespacePill(row: { state: 'active' | 'pending'; reason?: string; services?: number }, scanned: boolean): { tone: GapTone; detail?: string; count?: number } {
  if (row.state === 'pending') {
    if (!row.reason) return { tone: 'neutral', detail: 'waiting for first scan' };
    const info = pendingReasonInfo(row.reason);
    return { tone: info.tone, detail: info.short.toLowerCase() };
  }
  if (row.services === undefined) return { tone: 'neutral' };
  if (row.services === 0 && scanned) return { tone: 'warning', detail: 'no services' };
  return { tone: 'neutral', count: row.services };
}

/** The published collector repository (docker-build.yml); its tags are the release versions. */
export const COLLECTOR_IMAGE_REPOSITORY = 'ghcr.io/yassinebenameur/probara-discovery-collector';
/** The published repository, waiting for the release version tag. */
export const COLLECTOR_IMAGE_PREFILL = `${COLLECTOR_IMAGE_REPOSITORY}:`;
/**
 * The image field's starting value from the one remembered by the last
 * download. A published image goes back to the bare repository, so an upgrade
 * never re-offers the previous release's tag; a custom image is kept.
 */
export function initialCollectorImage(saved: string): string {
  const ref = saved.trim();
  if (!ref || ref === COLLECTOR_IMAGE_REPOSITORY || ref.startsWith(COLLECTOR_IMAGE_PREFILL) || ref.startsWith(`${COLLECTOR_IMAGE_REPOSITORY}@`)) return COLLECTOR_IMAGE_PREFILL;
  return ref;
}

/** Validates the install form; returns an error per field, or {} when a manifest can be built. */
export function manifestInputErrors(origin: string, image: string): { origin?: string; image?: string } {
  const errors: { origin?: string; image?: string } = {};
  try {
    const url = new URL(origin);
    if (url.protocol !== 'https:' || url.username || url.password || url.search || url.hash || url.pathname !== '/') throw new Error();
  } catch {
    errors.origin = 'Enter an HTTPS origin without a path, e.g. https://probara.example.com';
  }
  const ref = image.trim();
  if (!ref || /\s/.test(ref)) errors.image = 'Enter an image reference your cluster can pull';
  else if (/[:@]$/.test(ref)) errors.image = 'Add the image tag: this release’s version, such as 2.0.0';
  return errors;
}

// Read access to one namespace: a Role and a RoleBinding to the collector's
// ServiceAccount, which lives in the home namespace.
function accessDocuments(integration: Pick<DiscoveryIntegration, 'id' | 'home_namespace'>, namespaces: string[]): object[] {
  const name = collectorName(integration);
  return namespaces.flatMap((ns) => [
    { apiVersion: 'rbac.authorization.k8s.io/v1', kind: 'Role', metadata: { name, namespace: ns }, rules: [
      { apiGroups: [''], resources: ['services'], verbs: ['list'] },
      { apiGroups: ['networking.k8s.io'], resources: ['ingresses'], verbs: ['list'] },
    ] },
    { apiVersion: 'rbac.authorization.k8s.io/v1', kind: 'RoleBinding', metadata: { name, namespace: ns }, subjects: [{ kind: 'ServiceAccount', name, namespace: integration.home_namespace }], roleRef: { apiGroup: 'rbac.authorization.k8s.io', kind: 'Role', name } },
  ]);
}
function yamlDocuments(documents: object[]): string {
  return documents.map((d) => JSON.stringify(d, null, 2)).join('\n---\n') + '\n';
}

/**
 * Only the Roles and RoleBindings for `namespaces`: no Secret and no token, so
 * it can be downloaded and re-applied any number of times after a namespace
 * edit without rotating the collector credential.
 */
export function collectorAccessManifest(integration: Pick<DiscoveryIntegration, 'id' | 'home_namespace'>, namespaces: string[]): string {
  return yamlDocuments(accessDocuments(integration, namespaces));
}
export function accessManifestCommands(integration: Pick<DiscoveryIntegration, 'id'>, context = 'your-cluster') {
  const file = `${collectorName(integration)}-access.yaml`;
  return { file, apply: `kubectl --context ${context} apply -f ${file}` };
}
/** Deletes the collector's Role and RoleBinding from removed namespaces, one line each; '' for none. */
export function accessCleanupCommand(integration: Pick<DiscoveryIntegration, 'id'>, removed: string[], context = 'your-cluster'): string {
  const name = collectorName(integration);
  return removed.map((ns) => `kubectl --context ${context} -n ${ns} delete role,rolebinding ${name} --ignore-not-found`).join('\n');
}

// JSON documents are valid YAML. Values are serialized, never interpolated into
// executable shell commands or YAML scalars. No secret is retained in storage.
// A full (re)install grants access to pending namespaces too, so they are read
// from the first scan.
export function collectorManifest(integration: DiscoveryIntegration, origin: string, image: string, token: string): string {
  const invalid = manifestInputErrors(origin, image);
  if (invalid.origin) throw new Error('Enter an HTTPS API origin, without a path.');
  if (invalid.image) throw new Error('Enter a collector image available to your cluster, with its tag.');
  const url = new URL(origin);
  const name = collectorName(integration);
  const metadata = { name, namespace: integration.home_namespace };
  const documents: object[] = [
    { apiVersion: 'v1', kind: 'ServiceAccount', metadata },
    { apiVersion: 'v1', kind: 'Secret', metadata, type: 'Opaque', stringData: { token } },
    ...accessDocuments(integration, configuredNamespaces(integration)),
  ];
  documents.push({ apiVersion: 'apps/v1', kind: 'Deployment', metadata, spec: {
    replicas: 1, strategy: { type: 'Recreate' }, selector: { matchLabels: { app: name } }, template: {
      metadata: { labels: { app: name } }, spec: {
        serviceAccountName: name, securityContext: { runAsNonRoot: true, runAsUser: 65532, runAsGroup: 65532, fsGroup: 65532, seccompProfile: { type: 'RuntimeDefault' } },
        containers: [{ name: 'collector', image: image.trim(), env: [
          { name: 'PROBARA_URL', value: url.origin }, { name: 'PROBARA_DISCOVERY_ID', value: integration.id },
        ], resources: { requests: { cpu: '25m', memory: '32Mi' }, limits: { cpu: '250m', memory: '128Mi' } }, securityContext: { readOnlyRootFilesystem: true, allowPrivilegeEscalation: false, capabilities: { drop: ['ALL'] } }, volumeMounts: [{ name: 'credential', mountPath: '/var/run/probara', readOnly: true }] }],
        volumes: [{ name: 'credential', secret: { secretName: name, defaultMode: 288 } }],
      },
    },
  } });
  return yamlDocuments(documents);
}

/** apiRequest throws `{ error, message }` objects for API errors and Error for transport failures. */
export function errorMessage(e: unknown): string {
  if (e instanceof Error) return e.message;
  if (e && typeof e === 'object' && typeof (e as { message?: unknown }).message === 'string') return (e as { message: string }).message;
  return 'The request failed. Please retry.';
}
export function isNotFound(e: unknown): boolean {
  return Boolean(e && typeof e === 'object' && (e as { error?: unknown }).error === 'not_found');
}
export function isDuplicateTarget(e: unknown): boolean {
  return /existing monitor checks this target/i.test(errorMessage(e));
}
