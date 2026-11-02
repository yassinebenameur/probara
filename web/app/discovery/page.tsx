'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import { ArrowUpDown, Download, KeyRound, Pencil, Plus, Radar, RefreshCw, Search, Trash2 } from 'lucide-react';
import Button from '@/components/ui/Button';
import IconButton from '@/components/ui/IconButton';
import LiveStatus from '@/components/ui/LiveStatus';
import Page from '@/components/ui/Page';
import PageHeader from '@/components/ui/PageHeader';
import { RelativeTime } from '@/components/ui/RelativeTime';
import Select from '@/components/ui/Select';
import { useUrlSort } from '@/components/ui/SortableHeader';
import Pill from '@/components/ui/Pill';
import EmptyState from '@/components/ui/EmptyState';
import FilterChip from '@/components/ui/FilterChip';
import ChipGroup from '@/components/ui/ChipGroup';
import Callout from '@/components/ui/Callout';
import TableSkeleton from '@/components/ui/TableSkeleton';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import CopyableTarget from '@/components/ui/CopyableTarget';
import { useToast } from '@/components/ui/ToastProvider';
import { useCanWrite, useCurrentUser } from '@/components/providers/CurrentUserProvider';
import ServiceTable, { rowGaps } from '@/components/discovery/ServiceTable';
import ConnectClusterModal from '@/components/discovery/ConnectClusterModal';
import EditIntegrationModal from '@/components/discovery/EditIntegrationModal';
import { saveDownload } from '@/components/slas/SlaVisuals';
import ApproveModal from '@/components/discovery/ApproveModal';
import LinkMonitorModal from '@/components/discovery/LinkMonitorModal';
import { deleteDiscoveryIntegration, getAlertChannels, getDiscoveryCoverage, getDiscoveryIntegrations, getDiscoveryNamespaces, getLocations, rotateDiscoveryToken, unlinkDiscoveryMonitor } from '@/lib/api';
import {
  accessManifestCommands, collectorAccessManifest, collectorHealth, DISCOVERY_PAGE_SIZE, errorMessage, gapInfo, installCommands, isNotFound,
  namespacePill, namespaceRows, pageAfterShrink, pendingReasonInfo,
  type DiscoveryCoverage, type DiscoveryIntegration, type DiscoveryNamespace, type DiscoveryMonitor, type DiscoveryService, type DiscoverySort, type DiscoverySuggestion,
} from '@/lib/discovery';
import { pluralize } from '@/lib/format';
import type { SortDir, SortState } from '@/lib/sort';
import { TONE_TEXT, type StatusTone } from '@/lib/status-tones';
import type { AlertChannel, Location } from '@/lib/types';
import { useLiveRefresh } from '@/lib/useLiveRefresh';
import { useNow } from '@/lib/useNow';
import { flashStyle, useRowFlash, type FlashTone } from '@/lib/useRowFlash';

const REFRESH_MS = 30_000;

// Coverage is paged and sorted by the API, which orders each key one way
// only: namespace then name A→Z, or most gaps first. The header shows that
// order and a second click keeps it (there is no reverse to switch to).
const SORT_KEYS = ['name', 'gaps'] as const;
const SORT_DIR: Record<DiscoverySort, SortDir> = { name: 'asc', gaps: 'desc' };
const DEFAULT_SORT: SortState<DiscoverySort> = { key: 'name', dir: 'asc' };

type Modal =
  | { kind: 'connect' }
  | { kind: 'edit'; integration: DiscoveryIntegration }
  | { kind: 'install'; integration: DiscoveryIntegration & { token: string } }
  | { kind: 'approve'; service: DiscoveryService; suggestion: DiscoverySuggestion }
  | { kind: 'link'; service: DiscoveryService; suggestion?: DiscoverySuggestion; monitorId?: string };
type Confirm =
  | { kind: 'remove'; integration: DiscoveryIntegration }
  | { kind: 'rotate'; integration: DiscoveryIntegration }
  | { kind: 'unlink'; service: DiscoveryService; monitor: DiscoveryMonitor };

export default function DiscoveryPage() {
  const { showToast } = useToast();
  const writable = useCanWrite();
  // Read-only copy waits for identity, as useCanWrite reads false while it loads.
  const { loading: userLoading } = useCurrentUser();
  const readOnly = !writable && !userLoading;
  const now = useNow('minute');

  const [integrations, setIntegrations] = useState<DiscoveryIntegration[]>([]);
  const [loading, setLoading] = useState(true);
  const [pageError, setPageError] = useState('');
  const [selectedId, setSelectedId] = useState('');
  const [locations, setLocations] = useState<Location[]>([]);
  const [channels, setChannels] = useState<AlertChannel[]>([]);

  const [coverage, setCoverage] = useState<DiscoveryCoverage | null>(null);
  // Per-namespace state and counts, tagged with the cluster they belong to.
  const [namespaceState, setNamespaceState] = useState<{ id: string; items: DiscoveryNamespace[] } | null>(null);
  const [coverageError, setCoverageError] = useState('');
  const [page, setPage] = useState(1);
  const [search, setSearch] = useState('');
  const [q, setQ] = useState('');
  const [gapsOnly, setGapsOnly] = useState(false);
  const [urlSort, setUrlSort] = useUrlSort(SORT_KEYS, DEFAULT_SORT, SORT_DIR);
  const sort = urlSort.key;
  const sortState: SortState<DiscoverySort> = { key: sort, dir: SORT_DIR[sort] };
  const changeSort = (next: DiscoverySort) => { setUrlSort({ key: next, dir: SORT_DIR[next] }); setPage(1); };
  const coverageRequest = useRef(0);
  const [expanded, setExpanded] = useState<string | null>(null);

  const [modal, setModal] = useState<Modal | null>(null);
  const [confirm, setConfirm] = useState<Confirm | null>(null);
  const [confirming, setConfirming] = useState(false);

  const integration = integrations.find((i) => i.id === selectedId);

  const loadIntegrations = useCallback(async (prefer?: string) => {
    try {
      const items = await getDiscoveryIntegrations();
      setIntegrations(items);
      setSelectedId((old) => {
        const want = prefer ?? old;
        return items.some((i) => i.id === want) ? want : items[0]?.id || '';
      });
      setPageError('');
    } catch (e) {
      setPageError(errorMessage(e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadIntegrations();
    // Pickers only; a failure leaves them empty rather than blocking the page.
    getLocations({ page_size: 100 }).then((v) => setLocations(v.items)).catch(() => {});
    getAlertChannels({ page_size: 100 }).then((v) => setChannels(v.items)).catch(() => {});
  }, [loadIntegrations]);

  // Selection change: drop the previous cluster's data and errors.
  useEffect(() => {
    setCoverage(null); setCoverageError(''); setExpanded(null); setPage(1);
  }, [selectedId]);

  useEffect(() => {
    const t = window.setTimeout(() => { setQ(search.trim()); setPage(1); }, 300);
    return () => window.clearTimeout(t);
  }, [search]);

  // A visible load (first, manual, new page/filter/sort) reports its error in
  // place; a silent one (the poll, a stream event, a tab return) keeps the
  // rows and lets LiveStatus say the update failed. A response that a newer
  // request overtook is dropped.
  const loadCoverage = useCallback(async ({ silent }: { silent: boolean }) => {
    if (!selectedId) return true;
    const request = ++coverageRequest.current;
    // Namespace pills ride the same refresh (a pending namespace is promoted
    // by a scan); their failure only leaves the last known pills.
    const id = selectedId;
    const namespaces = getDiscoveryNamespaces(id).then((v) => v.items, () => null);
    try {
      const v = await getDiscoveryCoverage(selectedId, { page, q, gaps: gapsOnly, sort });
      if (request !== coverageRequest.current) return true;
      const items = await namespaces;
      if (items && request === coverageRequest.current) setNamespaceState({ id, items });
      // The page ran past the end (services covered while working through
      // "With gaps", a poll after deletions): move to the last page that has
      // rows, keeping the current rows until it loads, instead of showing an
      // empty state for a result set that is not empty.
      const last = pageAfterShrink(page, v.total, v.items.length, v.page_size);
      if (last !== null) { setPage(last); return true; }
      setCoverage(v); setCoverageError('');
      return true;
    } catch (e) {
      if (request !== coverageRequest.current) return true;
      // Removed elsewhere (or just now): refresh the list instead of reporting a stale ID.
      if (isNotFound(e)) { void loadIntegrations(); return true; }
      if (!silent) setCoverageError(errorMessage(e));
      return false;
    }
  }, [selectedId, page, q, gapsOnly, sort, loadIntegrations]);

  // Linked monitors' states ride the alert stream, so a monitor event reloads
  // coverage (debounced) as well as the 30s poll.
  const live = useLiveRefresh(loadCoverage, {
    intervalMs: REFRESH_MS,
    enabled: Boolean(selectedId),
    streamFilter: (event) => Boolean(event.alert.monitor_id),
  });
  const reloadCoverage = () => void live.refresh();
  const rowFlash = useRowFlash(coverage?.items, (s) => s.id, (s) => rowGaps(s).join(','));

  // Screen-reader news, politely, for the coverage changes the flash shows.
  const [coverageNews, setCoverageNews] = useState('');
  const announced = useRef(new Set<string>());
  useEffect(() => {
    const fresh = Array.from(rowFlash.flashing).filter((id) => !announced.current.has(id));
    announced.current = new Set(rowFlash.flashing);
    if (fresh.length === 0 || !coverage) return;
    const byId = new Map(coverage.items.map((s) => [s.id, s]));
    const parts = fresh
      .map((id) => byId.get(id))
      .filter((s): s is DiscoveryService => Boolean(s))
      .map((s) => {
        const gaps = rowGaps(s).length;
        return gaps === 0 ? `${s.name} is now covered` : `${s.name} now has ${pluralize(gaps, 'gap')}`;
      });
    if (parts.length > 0) setCoverageNews(parts.length > 3 ? `${parts.length} services changed coverage` : parts.join('. '));
  }, [rowFlash.flashing, coverage]);

  const refresh = () => { reloadCoverage(); void loadIntegrations(); };

  const runConfirm = async () => {
    if (!confirm) return;
    setConfirming(true);
    try {
      if (confirm.kind === 'remove') {
        await deleteDiscoveryIntegration(confirm.integration.id);
        const rest = integrations.filter((i) => i.id !== confirm.integration.id);
        setIntegrations(rest);
        setSelectedId(rest[0]?.id || '');
        showToast(`Removed ${confirm.integration.name}. Its monitors were kept.`, 'success');
      } else if (confirm.kind === 'rotate') {
        const { token } = await rotateDiscoveryToken(confirm.integration.id);
        setModal({ kind: 'install', integration: { ...confirm.integration, token } });
        void loadIntegrations();
      } else {
        await unlinkDiscoveryMonitor(confirm.service.id, confirm.monitor.id);
        // No toast: the monitor leaves the open row in place.
        reloadCoverage();
      }
      setConfirm(null);
    } catch (e) {
      showToast(errorMessage(e), 'error');
    } finally {
      setConfirming(false);
    }
  };

  const health = collectorHealth(coverage ? coverage.last_seen_at : integration?.last_seen_at, now);
  const lastSeen = coverage?.last_seen_at ?? integration?.last_seen_at ?? null;
  const filtered = Boolean(q || gapsOnly);
  const pageCount = coverage ? Math.max(1, Math.ceil(coverage.total / (coverage.page_size || DISCOVERY_PAGE_SIZE))) : 1;
  const commands = integration ? installCommands(integration) : null;
  const namespaceItems = namespaceState?.id === selectedId ? namespaceState.items : null;
  const rows = integration ? namespaceRows(integration, namespaceItems) : [];
  const pendingRows = rows.filter((r) => r.state === 'pending');
  const access = integration ? accessManifestCommands(integration) : null;
  // Token-free: Roles and RoleBindings for every configured namespace, so it
  // can be applied again whenever namespaces are added.
  const downloadAccess = () => {
    if (!integration || !access) return;
    const content = collectorAccessManifest(integration, rows.map((r) => r.namespace));
    saveDownload({ blob: new Blob([content], { type: 'application/yaml' }), filename: access.file });
  };

  const openConnect = () => setModal({ kind: 'connect' });
  const clearFilters = () => { setSearch(''); setQ(''); setGapsOnly(false); setPage(1); };
  const nextSort: DiscoverySort = sort === 'gaps' ? 'name' : 'gaps';
  const sortLabel = `Sorted by ${SORT_LABEL[sort].toLowerCase()}. Switch to ${SORT_LABEL[nextSort].toLowerCase()}`;

  return (
    <Page width="data">
      <PageHeader
        title="Discovery"
        subtitle="Kubernetes services and the monitors that cover them."
        titleInfoTip="Coverage checks that each discovered service has an enabled monitor with recent results and a reachable alert channel. It does not verify functional behaviour or notification delivery."
        action={integrations.length > 0 && <>
          {live.updatedAt !== null && (
            <LiveStatus
              updatedAt={live.updatedAt}
              refreshing={live.refreshing}
              failed={live.failed && !coverageError}
              onRefresh={refresh}
              refreshLabel="Refresh discovery"
              className="mr-1"
            />
          )}
          {writable && <Button variant="accent" size="sm" icon={<Plus strokeWidth={1.75} />} onClick={openConnect}>Connect cluster</Button>}
        </>}
      />

      {pageError && (
        <Callout tone="danger" action={<Button variant="ghost" size="xs" onClick={() => void loadIntegrations()}>Retry</Button>}>
          {pageError}
        </Callout>
      )}

      {loading ? (
        <TableSkeleton rows={4} columns={3} label="Loading discovery" />
      ) : integrations.length === 0 ? (
        !pageError && <EmptyState
          icon={<Radar strokeWidth={1.5} />}
          title="No clusters connected"
          description={readOnly
            ? 'A read-only collector lists Services and Ingresses and shows which have no monitoring. An editor can connect a cluster.'
            : 'A read-only collector lists Services and Ingresses in the namespaces you choose. Probara suggests checks for them and shows which services have no monitoring. Nothing is created without your review.'}
          action={writable ? { label: 'Connect cluster', onClick: openConnect, icon: <Plus strokeWidth={1.75} /> } : undefined}
        />
      ) : integration && (
        <>
          {/* Cluster identity on top, its coverage summary underneath: one panel. */}
          <div className="glass overflow-hidden">
            <div className="flex flex-wrap items-center gap-x-4 gap-y-2 px-5 py-3.5">
              {integrations.length > 1 ? (
                <Select aria-label="Cluster" className="input-sm !w-auto max-w-full truncate font-medium" value={selectedId} onChange={(e) => setSelectedId(e.target.value)}>
                  {integrations.map((i) => <option key={i.id} value={i.id}>{i.name}</option>)}
                </Select>
              ) : <span className="text-title font-semibold text-white">{integration.name}</span>}
              <HealthPill health={health} />
              <RelativeTime value={lastSeen} prefix="Last scan " fallback="No scan yet" className="text-xs text-slate-500" />
              <div className="flex min-w-0 basis-full flex-wrap items-center gap-1 sm:basis-auto sm:flex-1">
                {rows.map((row) => {
                  const view = namespacePill(row, Boolean(lastSeen));
                  return (
                    <Pill key={row.namespace} size="xs" tone={view.tone} className="whitespace-nowrap font-mono">
                      {row.namespace}
                      {view.count !== undefined && <span className="font-sans tabular-nums opacity-70">{view.count}<span className="sr-only"> {view.count === 1 ? 'service' : 'services'}</span></span>}
                      {view.detail && <span className="font-sans font-normal opacity-80">· {view.detail}</span>}
                    </Pill>
                  );
                })}
              </div>
              {writable && (
                <div className="flex items-center gap-0.5">
                  <IconButton icon={<KeyRound strokeWidth={1.75} />} label="Rotate credential and reinstall collector" onClick={() => setConfirm({ kind: 'rotate', integration })} />
                  <IconButton icon={<Pencil strokeWidth={1.75} />} label="Edit cluster" onClick={() => setModal({ kind: 'edit', integration })} />
                  <IconButton icon={<Trash2 strokeWidth={1.75} />} label="Remove cluster" danger onClick={() => setConfirm({ kind: 'remove', integration })} />
                </div>
              )}
            </div>
            {coverage?.summary && coverage.summary.services > 0 && <SummaryRow summary={coverage.summary} />}
          </div>

          {pendingRows.length > 0 && access && (
            // Live region: a scan can promote or skip a namespace while the page is open.
            <Callout
              tone="warning"
              live="polite"
              title={pendingRows.length === 1 ? `${pendingRows[0].namespace} is not scanned yet` : `${pendingRows.length} namespaces are not scanned yet`}
              action={<Button variant="ghost" size="xs" icon={<Download strokeWidth={1.75} />} onClick={downloadAccess}>Access manifest</Button>}
            >
              <ul className="space-y-0.5">
                {pendingRows.map((row) => (
                  <li key={row.namespace}>
                    <span className="font-mono text-white">{row.namespace}</span>: {row.reason ? pendingReasonInfo(row.reason).label : 'Waiting for the first scan'}
                    {row.checked_at && <> <span className="text-xs text-slate-500">(<RelativeTime value={row.checked_at} prefix="checked " />)</span></>}
                  </li>
                ))}
              </ul>
              <p className="mt-1.5">
                Apply the access manifest so the collector can read {pendingRows.length === 1 ? 'it' : 'them'}: <CopyableTarget value={access.apply} label="command" size="xs" href={null} className="align-middle" />. The other namespaces keep syncing meanwhile.
              </p>
            </Callout>
          )}

          {coverage?.inventory_stale && health === 'stale' && (
            // Live region: a poll can flip the collector to stale while the page is open.
            <Callout tone="warning" live="polite" title={lastSeen ? <>Last complete scan <RelativeTime value={lastSeen} /></> : 'No complete scan since setup'}>
              Gaps may be out of date and new approvals are paused.
              {commands && <> Check the collector: <CopyableTarget value={commands.logs} label="command" size="xs" href={null} className="align-middle" /></>}
            </Callout>
          )}

          {health === 'waiting' && coverage && coverage.total === 0 && !filtered ? (
            <EmptyState
              icon={<RefreshCw strokeWidth={1.5} />}
              title="Waiting for the first scan"
              description={readOnly
                ? 'The first scan runs as soon as the collector starts in the cluster.'
                : 'Apply the collector manifest to the cluster; the first scan runs as soon as it starts. If you no longer have the manifest, rotate the credential to download a new one.'}
              action={writable ? <Button variant="ghost" icon={<KeyRound strokeWidth={1.75} />} onClick={() => setConfirm({ kind: 'rotate', integration })}>Get a new manifest</Button> : undefined}
            />
          ) : (
            <div className="space-y-3">
              <div className="flex flex-col gap-2.5 sm:flex-row sm:flex-wrap sm:items-center sm:gap-3">
                <div className="relative w-full sm:w-64">
                  <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-slate-500" strokeWidth={1.75} aria-hidden="true" />
                  <input
                    type="search"
                    className="input input-sm h-8 py-0 !pl-8"
                    aria-label="Search services or namespaces"
                    placeholder="Search services or namespaces"
                    value={search}
                    onChange={(e) => setSearch(e.target.value)}
                  />
                </div>
                <ChipGroup ariaLabel="Coverage">
                  <FilterChip selected={!gapsOnly} count={coverage?.summary?.services} onClick={() => { setGapsOnly(false); setPage(1); }}>All</FilterChip>
                  <FilterChip selected={gapsOnly} count={coverage?.summary?.with_gaps} onClick={() => { setGapsOnly(true); setPage(1); }}>With gaps</FilterChip>
                </ChipGroup>
                {/* Below xl the Coverage column (and its sortable header) folds
                    under the service name, so the order needs its own control. */}
                <FilterChip
                  className="self-start sm:ml-auto sm:self-auto xl:hidden"
                  icon={<ArrowUpDown strokeWidth={1.75} />}
                  aria-label={sortLabel}
                  onClick={() => changeSort(nextSort)}
                >
                  {SORT_LABEL[sort]}
                </FilterChip>
              </div>

              {coverageError ? (
                <Callout tone="danger" action={<Button variant="ghost" size="xs" onClick={reloadCoverage} loading={live.loading}>Retry</Button>}>
                  {coverageError}
                </Callout>
              ) : !coverage ? (
                <TableSkeleton rows={5} columns={3} label="Loading services" />
              ) : coverage.total === 0 ? (
                filtered ? (
                  <EmptyState
                    icon={<Search strokeWidth={1.5} />}
                    title="No services match"
                    description="Try another search or show all services."
                    action={<Button variant="ghost" size="sm" onClick={clearFilters}>Clear filters</Button>}
                  />
                ) : (
                  <EmptyState
                    icon={<Radar strokeWidth={1.5} />}
                    title="Nothing discovered"
                    description={`The last scan found no Services or Ingresses in ${integration.namespaces.join(', ')}.`}
                  />
                )
              ) : (
                <div className="glass overflow-hidden">
                  <ServiceTable
                    items={coverage.items}
                    expanded={expanded}
                    onToggle={(id) => setExpanded(expanded === id ? null : id)}
                    writable={writable}
                    stale={coverage.inventory_stale}
                    onApprove={(service, suggestion) => setModal({ kind: 'approve', service, suggestion })}
                    onLink={(service, suggestion, monitorId) => setModal({ kind: 'link', service, suggestion, monitorId })}
                    onUnlink={(service, monitor) => setConfirm({ kind: 'unlink', service, monitor })}
                    sort={sortState}
                    onSort={(next) => changeSort(next.key)}
                    rowFlash={(service) => ({ className: rowFlash.flashClass(service.id), style: flashStyle(coverageTone(service)) })}
                  />
                  {coverage.total > (coverage.page_size || DISCOVERY_PAGE_SIZE) && (
                    <div className="flex items-center justify-between gap-3 border-t border-divider px-4 py-3 text-xs text-slate-500">
                      <span className="tabular-nums">Page {page} of {pageCount} ({pluralize(coverage.total, 'service')})</span>
                      <div className="flex items-center gap-2">
                        <Button variant="ghost" size="xs" disabled={page <= 1} onClick={() => setPage(page - 1)}>Previous</Button>
                        <Button variant="ghost" size="xs" disabled={page >= pageCount} onClick={() => setPage(page + 1)}>Next</Button>
                      </div>
                    </div>
                  )}
                </div>
              )}
            </div>
          )}
        </>
      )}

      <p role="status" className="sr-only">{coverageNews}</p>

      {modal?.kind === 'connect' && (
        <ConnectClusterModal
          locations={locations}
          onCreated={(created) => void loadIntegrations(created.id)}
          onClose={() => { setModal(null); reloadCoverage(); }}
        />
      )}
      {modal?.kind === 'edit' && (
        <EditIntegrationModal
          integration={modal.integration}
          locations={locations}
          onSaved={(updated) => {
            setIntegrations((list) => list.map((i) => (i.id === updated.id ? { ...i, ...updated } : i)));
            // Pills fall back to the saved integration until the reload brings counts.
            setNamespaceState(null);
            reloadCoverage();
          }}
          onClose={() => setModal(null)}
        />
      )}
      {modal?.kind === 'install' && (
        <ConnectClusterModal install={modal.integration} locations={locations} onCreated={() => {}} onClose={() => setModal(null)} />
      )}
      {modal?.kind === 'approve' && (
        <ApproveModal
          service={modal.service}
          suggestion={modal.suggestion}
          candidates={modal.service.candidates[modal.suggestion.key] || []}
          locations={locations}
          channels={channels}
          defaultLocationId={integration?.location_id ?? null}
          stale={Boolean(coverage?.inventory_stale)}
          // No toast: the open row shows the new monitor in place.
          onCreated={() => { setModal(null); setExpanded(modal.service.id); reloadCoverage(); }}
          onLinkInstead={(monitorId) => setModal({ kind: 'link', service: modal.service, suggestion: modal.suggestion, monitorId })}
          onClose={() => setModal(null)}
        />
      )}
      {modal?.kind === 'link' && (
        <LinkMonitorModal
          service={modal.service}
          suggestion={modal.suggestion}
          candidates={modal.suggestion ? modal.service.candidates[modal.suggestion.key] || [] : []}
          initialMonitorId={modal.monitorId}
          onLinked={() => { setModal(null); setExpanded(modal.service.id); reloadCoverage(); }}
          onClose={() => setModal(null)}
        />
      )}

      {/* ConfirmDialog portals itself (components/ui/ConfirmDialog.tsx). */}
      <ConfirmDialog
        open={confirm !== null}
        loading={confirming}
        tone={confirm?.kind === 'rotate' ? 'default' : 'danger'}
        title={confirm?.kind === 'remove' ? `Remove ${confirm.integration.name}?` : confirm?.kind === 'rotate' ? 'Rotate the collector credential?' : confirm?.kind === 'unlink' ? `Unlink ${confirm.monitor.name}?` : ''}
        description={confirm?.kind === 'remove'
          ? 'Its inventory and monitor links are deleted. Monitors and their history are kept. Delete the collector from the cluster separately.'
          : confirm?.kind === 'rotate'
            ? 'The installed collector stops syncing until you apply the new manifest.'
            : confirm?.kind === 'unlink' ? `The monitor keeps running; it just no longer counts toward ${confirm.service.name}.` : undefined}
        confirmLabel={confirm?.kind === 'remove' ? 'Remove' : confirm?.kind === 'rotate' ? 'Rotate and reinstall' : 'Unlink'}
        onConfirm={() => void runConfirm()}
        onCancel={() => setConfirm(null)}
      />
    </Page>
  );
}

const SORT_LABEL: Record<DiscoverySort, string> = { name: 'Namespace, name', gaps: 'Most gaps' };

// A row that just changed flashes in its new worst gap's tone (green once covered).
function coverageTone(service: DiscoveryService): FlashTone {
  const tones = rowGaps(service).map((g) => gapInfo(g).tone);
  if (tones.length === 0) return 'success';
  if (tones.includes('danger')) return 'danger';
  if (tones.includes('warning')) return 'warning';
  return 'neutral';
}

function HealthPill({ health }: { health: ReturnType<typeof collectorHealth> }) {
  if (health === 'syncing') return <Pill size="xs" tone="success" dot>Syncing</Pill>;
  if (health === 'stale') return <Pill size="xs" tone="warning" dot>Stale</Pill>;
  return <Pill size="xs" dot>Waiting for first scan</Pill>;
}

// KPI row in the cluster panel, laid out like SlaReportView's tiles. A count
// takes its status tone only when non-zero; zero stays neutral. Counts of one
// gap take that gap's tone, so a tile and the row pills it counts agree:
// `missing` is resource_missing only, and services still within the grace
// period (neutral pills) are noted under it when the API reports them.
function SummaryRow({ summary }: { summary: NonNullable<DiscoveryCoverage['summary']> }) {
  const grace = summary.missing_grace ?? 0;
  const tiles: { label: string; value: number; tone?: StatusTone; note?: string }[] = [
    { label: 'Services', value: summary.services },
    { label: 'Fully covered', value: summary.covered, tone: 'success' },
    { label: 'With gaps', value: summary.with_gaps, tone: 'warning' },
    { label: 'Unmonitored', value: summary.unmonitored, tone: gapInfo('no_enabled_monitor').tone },
    { label: 'Missing', value: summary.missing, tone: gapInfo('resource_missing').tone, note: grace > 0 ? `+${grace} in grace period` : undefined },
  ];
  return (
    <dl className="grid grid-cols-2 gap-4 border-t border-divider px-5 py-4 sm:grid-cols-3 lg:grid-cols-5">
      {tiles.map((t) => (
        <div key={t.label}>
          <dt className="text-xs text-slate-400">{t.label}</dt>
          <dd className={`mt-1 text-metric font-semibold tabular-nums ${t.tone && t.value > 0 ? TONE_TEXT[t.tone] : 'text-white'}`}>{t.value}</dd>
          {t.note && <dd className="mt-0.5 text-xs tabular-nums text-slate-500">{t.note}</dd>}
        </div>
      ))}
    </dl>
  );
}
