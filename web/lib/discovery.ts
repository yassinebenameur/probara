import type { StatusTone } from './status-tones';
import type { MonitorState } from './types';

export interface DiscoveryIntegration {
  id: string; name: string; namespaces: string[]; location_id: string | null;
  last_seen_at: string | null; revision: number; token?: string;
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
export interface DiscoverySummary { services: number; covered: number; with_gaps: number; unmonitored: number; missing: number }
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
export function suggestionTarget(s: DiscoverySuggestion): string {
  return s.port ? `${s.target}:${s.port}` : s.target;
}

const NAMESPACE = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/;
export function validNamespace(ns: string): boolean { return ns.length > 0 && ns.length <= 63 && NAMESPACE.test(ns); }
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
    logs: `kubectl --context ${context} -n ${integration.namespaces[0]} logs deployment/${name}`,
  };
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
  if (!image.trim() || /\s/.test(image.trim())) errors.image = 'Enter an image reference your cluster can pull';
  return errors;
}

// JSON documents are valid YAML. Values are serialized, never interpolated into
// executable shell commands or YAML scalars. No secret is retained in storage.
export function collectorManifest(integration: DiscoveryIntegration, origin: string, image: string, token: string): string {
  const url = new URL(origin);
  if (url.protocol !== 'https:' || url.username || url.password || url.search || url.hash || url.pathname !== '/') throw new Error('Enter an HTTPS API origin, without a path.');
  if (!image.trim() || /\s/.test(image.trim())) throw new Error('Enter a collector image available to your cluster.');
  const name = collectorName(integration);
  const namespace = integration.namespaces[0];
  const metadata = { name, namespace };
  const documents: object[] = [
    { apiVersion: 'v1', kind: 'ServiceAccount', metadata },
    { apiVersion: 'v1', kind: 'Secret', metadata, type: 'Opaque', stringData: { token } },
  ];
  for (const ns of integration.namespaces) {
    documents.push(
      { apiVersion: 'rbac.authorization.k8s.io/v1', kind: 'Role', metadata: { name, namespace: ns }, rules: [
        { apiGroups: [''], resources: ['services'], verbs: ['list'] },
        { apiGroups: ['networking.k8s.io'], resources: ['ingresses'], verbs: ['list'] },
      ] },
      { apiVersion: 'rbac.authorization.k8s.io/v1', kind: 'RoleBinding', metadata: { name, namespace: ns }, subjects: [{ kind: 'ServiceAccount', name, namespace }], roleRef: { apiGroup: 'rbac.authorization.k8s.io', kind: 'Role', name } },
    );
  }
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
  return documents.map((d) => JSON.stringify(d, null, 2)).join('\n---\n') + '\n';
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
