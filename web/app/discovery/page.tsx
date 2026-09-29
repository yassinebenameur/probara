'use client';

import { useCallback, useEffect, useState } from 'react';
import { KeyRound, Plus, RefreshCw, Search, Trash2, Workflow } from 'lucide-react';
import Button from '@/components/ui/Button';
import IconButton from '@/components/ui/IconButton';
import PageHeader from '@/components/ui/PageHeader';
import Panel from '@/components/ui/Panel';
import Pill from '@/components/ui/Pill';
import EmptyState from '@/components/ui/EmptyState';
import FilterChip from '@/components/ui/FilterChip';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import ModalPortal from '@/components/ui/ModalPortal';
import CopyableTarget from '@/components/ui/CopyableTarget';
import { useToast } from '@/components/ui/ToastProvider';
import { useCurrentUser } from '@/components/providers/CurrentUserProvider';
import ServiceTable from '@/components/discovery/ServiceTable';
import ConnectClusterModal from '@/components/discovery/ConnectClusterModal';
import ApproveModal, { Notice } from '@/components/discovery/ApproveModal';
import LinkMonitorModal from '@/components/discovery/LinkMonitorModal';
import { deleteDiscoveryIntegration, getAlertChannels, getDiscoveryCoverage, getDiscoveryIntegrations, getLocations, rotateDiscoveryToken, unlinkDiscoveryMonitor } from '@/lib/api';
import {
  collectorHealth, DISCOVERY_PAGE_SIZE, errorMessage, installCommands, isNotFound,
  type DiscoveryCoverage, type DiscoveryIntegration, type DiscoveryMonitor, type DiscoveryService, type DiscoverySort, type DiscoverySuggestion,
} from '@/lib/discovery';
import { formatTimeAgo } from '@/lib/monitor-utils';
import type { AlertChannel, Location } from '@/lib/types';

const POLL_MS = 30_000;

type Modal =
  | { kind: 'connect' }
  | { kind: 'install'; integration: DiscoveryIntegration & { token: string } }
  | { kind: 'approve'; service: DiscoveryService; suggestion: DiscoverySuggestion }
  | { kind: 'link'; service: DiscoveryService; suggestion?: DiscoverySuggestion; monitorId?: string };
type Confirm =
  | { kind: 'remove'; integration: DiscoveryIntegration }
  | { kind: 'rotate'; integration: DiscoveryIntegration }
  | { kind: 'unlink'; service: DiscoveryService; monitor: DiscoveryMonitor };

export default function DiscoveryPage() {
  const { showToast } = useToast();
  const { canWrite, loading: identityLoading } = useCurrentUser();
  const writable = canWrite && !identityLoading;

  const [integrations, setIntegrations] = useState<DiscoveryIntegration[]>([]);
  const [loading, setLoading] = useState(true);
  const [pageError, setPageError] = useState('');
  const [selectedId, setSelectedId] = useState('');
  const [locations, setLocations] = useState<Location[]>([]);
  const [channels, setChannels] = useState<AlertChannel[]>([]);

  const [coverage, setCoverage] = useState<DiscoveryCoverage | null>(null);
  const [coverageError, setCoverageError] = useState('');
  const [fetching, setFetching] = useState(false);
  const [tick, setTick] = useState(0);
  const [page, setPage] = useState(1);
  const [search, setSearch] = useState('');
  const [q, setQ] = useState('');
  const [gapsOnly, setGapsOnly] = useState(false);
  const [sort, setSort] = useState<DiscoverySort>('name');
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

  useEffect(() => {
    if (!selectedId) return;
    let active = true;
    setFetching(true);
    getDiscoveryCoverage(selectedId, { page, q, gaps: gapsOnly, sort })
      .then((v) => { if (active) { setCoverage(v); setCoverageError(''); } })
      .catch((e) => {
        if (!active) return;
        // Removed elsewhere (or just now): refresh the list instead of reporting a stale ID.
        if (isNotFound(e)) void loadIntegrations();
        else setCoverageError(errorMessage(e));
      })
      .finally(() => { if (active) setFetching(false); });
    return () => { active = false; };
  }, [selectedId, page, q, gapsOnly, sort, tick, loadIntegrations]);

  useEffect(() => {
    const timer = window.setInterval(() => { if (document.visibilityState === 'visible') setTick((v) => v + 1); }, POLL_MS);
    return () => window.clearInterval(timer);
  }, []);

  const refresh = () => { setTick((v) => v + 1); void loadIntegrations(); };

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
        showToast(`Unlinked ${confirm.monitor.name}`, 'success');
        setTick((v) => v + 1);
      }
      setConfirm(null);
    } catch (e) {
      showToast(errorMessage(e), 'error');
    } finally {
      setConfirming(false);
    }
  };

  const health = collectorHealth(coverage ? coverage.last_seen_at : integration?.last_seen_at);
  const lastSeen = coverage?.last_seen_at ?? integration?.last_seen_at ?? null;
  const filtered = Boolean(q || gapsOnly);
  const pageCount = coverage ? Math.max(1, Math.ceil(coverage.total / (coverage.page_size || DISCOVERY_PAGE_SIZE))) : 1;
  const commands = integration ? installCommands(integration) : null;

  const connectAction = writable && (
    <Button variant="accent" icon={<Plus strokeWidth={1.75} />} onClick={() => setModal({ kind: 'connect' })}>Connect cluster</Button>
  );

  return (
    <div className="space-y-4">
      <PageHeader
        title="Discovery"
        subtitle="Kubernetes services and the monitors that cover them."
        titleInfoTip="Coverage checks that each discovered service has an enabled monitor with recent results and a reachable alert channel. It does not verify functional behaviour or notification delivery."
        action={integrations.length > 0 && <>
          <Button variant="ghost" icon={<RefreshCw strokeWidth={1.75} className={fetching ? 'animate-spin' : ''} />} onClick={refresh}>Refresh</Button>
          {connectAction}
        </>}
      />

      {pageError && (
        <div className="flex items-center justify-between gap-3 rounded-lg border border-rose-500/20 bg-rose-500/10 px-4 py-3">
          <p className="text-sm text-rose-300">{pageError}</p>
          <Button variant="danger" size="xs" onClick={() => void loadIntegrations()}>Retry</Button>
        </div>
      )}

      {loading ? (
        <Panel><p className="text-sm text-slate-500">Loading discovery…</p></Panel>
      ) : integrations.length === 0 ? (
        !pageError && <EmptyState
          icon={<Workflow strokeWidth={1.5} />}
          title="Connect your first cluster"
          description="A read-only collector lists Services and Ingresses in the namespaces you choose. Probara suggests checks for them and shows which services have no monitoring. Nothing is created without your review."
          action={connectAction || undefined}
        />
      ) : integration && (
        <>
          {/* Cluster bar */}
          <div className="flex flex-wrap items-center gap-x-4 gap-y-2 rounded-xl border border-white/[0.06] bg-slate-900/50 px-4 py-3">
            {integrations.length > 1 ? (
              <select aria-label="Cluster" className="input !w-auto !py-1.5 font-medium" value={selectedId} onChange={(e) => setSelectedId(e.target.value)}>
                {integrations.map((i) => <option key={i.id} value={i.id}>{i.name}</option>)}
              </select>
            ) : <span className="text-sm font-medium text-white">{integration.name}</span>}
            <HealthPill health={health} />
            <span className="text-xs text-slate-500">{lastSeen ? `Last scan ${formatTimeAgo(lastSeen)}` : 'No scan yet'}</span>
            <div className="flex min-w-0 basis-full flex-wrap items-center gap-1 sm:basis-auto sm:flex-1">
              {integration.namespaces.map((ns) => <Pill key={ns} size="xs" className="whitespace-nowrap font-mono">{ns}</Pill>)}
            </div>
            {writable && (
              <div className="flex items-center gap-0.5">
                <IconButton icon={<KeyRound strokeWidth={1.75} />} label="Rotate credential and reinstall collector" onClick={() => setConfirm({ kind: 'rotate', integration })} />
                <IconButton icon={<Trash2 strokeWidth={1.75} />} label="Remove cluster" danger onClick={() => setConfirm({ kind: 'remove', integration })} />
              </div>
            )}
          </div>

          {coverage?.inventory_stale && health === 'stale' && (
            <Notice tone="warning">
              No complete scan since {lastSeen ? formatTimeAgo(lastSeen) : 'setup'}. Gaps may be out of date and new approvals are paused.
              {commands && <> Check the collector: <CopyableTarget value={commands.logs} label="command" size="xs" href={null} className="align-middle" textClassName="text-amber-100" /></>}
            </Notice>
          )}

          {coverage?.summary && coverage.summary.services > 0 && <SummaryStrip summary={coverage.summary} />}

          {health === 'waiting' && coverage && coverage.total === 0 && !filtered ? (
              <EmptyState
                icon={<RefreshCw strokeWidth={1.5} />}
                title="Waiting for the first scan"
                description={`Apply the collector manifest to the cluster; the first scan runs as soon as it starts. If you no longer have the manifest, rotate the credential to download a new one.`}
                action={writable ? <Button variant="ghost" icon={<KeyRound strokeWidth={1.75} />} onClick={() => setConfirm({ kind: 'rotate', integration })}>Get a new manifest</Button> : undefined}
              />
            ) : (
              <Panel allowOverflow>
                <div className="mb-4 flex flex-wrap items-center gap-2">
                  <div className="relative min-w-[12rem] flex-1 sm:max-w-xs">
                    <Search className="pointer-events-none absolute left-3 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-slate-500" aria-hidden="true" />
                    <input className="input !py-1.5 !pl-8" aria-label="Search services" placeholder="Search services or namespaces" value={search} onChange={(e) => setSearch(e.target.value)} />
                  </div>
                  <FilterChip selected={!gapsOnly} count={coverage?.summary?.services} onClick={() => { setGapsOnly(false); setPage(1); }}>All</FilterChip>
                  <FilterChip selected={gapsOnly} count={coverage?.summary?.with_gaps} onClick={() => { setGapsOnly(true); setPage(1); }}>With gaps</FilterChip>
                  <select aria-label="Sort services" className="input !w-auto !py-1.5 text-xs sm:ml-auto" value={sort} onChange={(e) => { setSort(e.target.value as DiscoverySort); setPage(1); }}>
                    <option value="name">Sort by namespace / name</option>
                    <option value="gaps">Sort by most gaps</option>
                  </select>
                </div>

                {coverageError ? (
                  <div className="flex items-center justify-between gap-3 py-6">
                    <p className="text-sm text-rose-300">{coverageError}</p>
                    <Button variant="ghost" size="xs" onClick={() => setTick((v) => v + 1)}>Retry</Button>
                  </div>
                ) : !coverage ? (
                  <p className="py-6 text-sm text-slate-500">Loading services…</p>
                ) : coverage.items.length === 0 ? (
                  filtered ? (
                    <InlineEmpty title="No services match" description="Try another search or show all services." action={<Button variant="ghost" size="xs" onClick={() => { setSearch(''); setQ(''); setGapsOnly(false); setPage(1); }}>Clear filters</Button>} />
                  ) : (
                    <InlineEmpty title="Nothing discovered" description={`The last scan found no Services or Ingresses in ${integration.namespaces.join(', ')}.`} />
                  )
                ) : (
                  <>
                    <ServiceTable
                      items={coverage.items}
                      expanded={expanded}
                      onToggle={(id) => setExpanded(expanded === id ? null : id)}
                      writable={writable}
                      stale={coverage.inventory_stale}
                      onApprove={(service, suggestion) => setModal({ kind: 'approve', service, suggestion })}
                      onLink={(service, suggestion, monitorId) => setModal({ kind: 'link', service, suggestion, monitorId })}
                      onUnlink={(service, monitor) => setConfirm({ kind: 'unlink', service, monitor })}
                    />
                    {coverage.total > (coverage.page_size || DISCOVERY_PAGE_SIZE) && (
                      <div className="mt-4 flex items-center justify-between text-xs text-slate-500">
                        <span>Page {page} of {pageCount} · {coverage.total} services</span>
                        <div className="flex gap-2">
                          <Button variant="ghost" size="xs" disabled={page <= 1} onClick={() => setPage(page - 1)}>Previous</Button>
                          <Button variant="ghost" size="xs" disabled={page >= pageCount} onClick={() => setPage(page + 1)}>Next</Button>
                        </div>
                      </div>
                    )}
                  </>
                )}
              </Panel>
            )}
        </>
      )}

      {modal?.kind === 'connect' && (
        <ConnectClusterModal
          locations={locations}
          onCreated={(created) => void loadIntegrations(created.id)}
          onClose={() => { setModal(null); setTick((v) => v + 1); }}
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
          onCreated={(_, name) => { setModal(null); setExpanded(modal.service.id); setTick((v) => v + 1); showToast(`Created ${name} and linked it to ${modal.service.name}`, 'success'); }}
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
          onLinked={(name) => { setModal(null); setExpanded(modal.service.id); setTick((v) => v + 1); showToast(`Linked ${name} to ${modal.service.name}`, 'success'); }}
          onClose={() => setModal(null)}
        />
      )}

      <ModalPortal>
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
      </ModalPortal>
    </div>
  );
}

function HealthPill({ health }: { health: ReturnType<typeof collectorHealth> }) {
  if (health === 'syncing') return <Pill size="xs" tone="success" dot>Syncing</Pill>;
  if (health === 'stale') return <Pill size="xs" tone="warning" dot>Stale</Pill>;
  return <Pill size="xs" dot>Waiting for first scan</Pill>;
}

function SummaryStrip({ summary }: { summary: NonNullable<DiscoveryCoverage['summary']> }) {
  const tiles = [
    { label: 'Services', value: summary.services, tone: 'text-white' },
    { label: 'Fully covered', value: summary.covered, tone: summary.covered > 0 ? 'text-emerald-300' : 'text-white' },
    { label: 'With gaps', value: summary.with_gaps, tone: summary.with_gaps > 0 ? 'text-amber-300' : 'text-white' },
    { label: 'Unmonitored', value: summary.unmonitored, tone: summary.unmonitored > 0 ? 'text-amber-300' : 'text-white' },
    { label: 'Missing', value: summary.missing, tone: summary.missing > 0 ? 'text-rose-300' : 'text-white' },
  ];
  return (
    <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5">
      {tiles.map((t) => (
        <div key={t.label} className="rounded-lg border border-white/[0.06] bg-slate-950/35 px-4 py-3">
          <p className="text-xs font-medium text-slate-500">{t.label}</p>
          <p className={`mt-1 text-2xl font-semibold tabular-nums ${t.tone}`}>{t.value}</p>
        </div>
      ))}
    </div>
  );
}

function InlineEmpty({ title, description, action }: { title: string; description: string; action?: React.ReactNode }) {
  return (
    <div className="py-10 text-center">
      <p className="text-sm font-medium text-slate-200">{title}</p>
      <p className="mt-1 text-sm text-slate-500">{description}</p>
      {action && <div className="mt-4">{action}</div>}
    </div>
  );
}
