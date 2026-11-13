'use client';

import { Suspense, useState, useMemo } from 'react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { Plus, Download, Upload, Search, Activity, X } from 'lucide-react';
import RowMenu, { RowMenuItem } from '@/components/ui/RowMenu';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import Page from '@/components/ui/Page';
import LiveStatus from '@/components/ui/LiveStatus';
import { type FilterOption } from '@/components/ui/FilterPopover';
import PageHeader from '@/components/ui/PageHeader';
import SharedEmptyState from '@/components/ui/EmptyState';
import TableSkeleton from '@/components/ui/TableSkeleton';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import { useToast } from '@/components/ui/ToastProvider';
import { BulkAlertingModal } from '@/components/monitors/BulkAlertingModal';
import { getTagColor } from '@/components/monitors/TagPill';
import { Monitor } from '@/lib/types';
import { deleteMonitor, toggleMonitorEnabled, exportMonitors, snoozeMonitor } from '@/lib/api';
import { MONITOR_STATUS_FILTERS, MonitorSortContext, sortMonitorList, sortMonitorsByName, countMonitorStatuses, groupHasMatchingMember, monitorMatchesFilters, type MonitorListFilters } from '@/lib/monitor-list';
import { errorMessage } from '@/lib/errors';
import { useCanWrite } from '@/components/providers/CurrentUserProvider';
import { getEffectiveMonitorStatus } from '@/lib/monitor-utils';
import { monitorTypeLabel } from '@/lib/monitor-type-labels';
import { formatDateTime } from '@/lib/format';
import { ROW_LAYOUTS, RowLayoutContext, leadStyle, SelectableContext, RowFlashContext, handleRowArrowKeys } from '@/components/monitors/list/row-layout';
import { MonitorRow } from '@/components/monitors/list/MonitorRow';
import { GroupCard } from '@/components/monitors/list/GroupCard';
import { DetailPanel } from '@/components/monitors/list/DetailPanel';
import { MonitorListUrlSync, ListHeader } from '@/components/monitors/list/ListHeader';
import { useMonitorResults } from '@/components/monitors/list/useMonitorResults';
import { useMonitorListFilters } from '@/components/monitors/list/useMonitorListFilters';
import { useMonitorSelection } from '@/components/monitors/list/useMonitorSelection';
import { useMonitorBulkGroups } from '@/components/monitors/list/useMonitorBulkGroups';
import { useMonitorListAnnouncements } from '@/components/monitors/list/useMonitorListAnnouncements';
import MonitorBulkBar from '@/components/monitors/list/MonitorBulkBar';
import MonitorListToolbar from '@/components/monitors/list/MonitorListToolbar';

export default function MonitorsPage() {
  const canWrite = useCanWrite();
  const filters = useMonitorListFilters();
  const { searchTerm, selectedTypes, statusFilter, unroutedOnly, sortMode, setSortMode, selectedTags, hasActiveFilters, clearFilters } =
    filters;
  const { monitors, setMonitors, loading, error, checkResultsMap, resultsReady, groupMembersMap, live, reloadAll } =
    useMonitorResults();
  const [selectedMonitorId, setSelectedMonitorId] = useState<string | null>(null);
  const { showToast } = useToast();
  const [pendingDelete, setPendingDelete] = useState<Monitor | null>(null);
  const [confirmBulkDelete, setConfirmBulkDelete] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [exporting, setExporting] = useState(false);
  const [showBulkAlerting, setShowBulkAlerting] = useState(false);
  const router = useRouter();

  // Below lg the detail panel is not rendered, so selecting a row would do
  // nothing visible — open the monitor's page instead.
  const selectMonitor = (id: string) => {
    // On touch screens (no hover), a tap toggles the row while a selection
    // exists, the usual mobile multi-select; with a pointer the boxes do it.
    if (selectionMode && typeof window !== 'undefined' && window.matchMedia('(hover: none)').matches) {
      toggleMonitorSelection(id);
      return;
    }
    if (typeof window !== 'undefined' && !window.matchMedia('(min-width: 1024px)').matches) {
      router.push(`/monitors/${id}`);
      return;
    }
    // The preview panel is opt-in: clicking the previewed row again closes it.
    setSelectedMonitorId((current) => (current === id ? null : id));
  };

  const handleExport = async () => {
    try {
      setExporting(true);
      const { blob, filename } = await exportMonitors();
      const url = window.URL.createObjectURL(blob);
      const link = document.createElement('a');
      link.href = url;
      link.download = filename;
      document.body.appendChild(link);
      link.click();
      link.remove();
      window.URL.revokeObjectURL(url);
      showToast('Monitor export downloaded', 'success');
    } catch (err) {
      showToast(errorMessage(err, 'Failed to export monitors'), 'error');
    } finally {
      setExporting(false);
    }
  };

  const requestDeleteById = (id: string) => {
    const target = monitors.find((m) => m.id === id);
    if (target) setPendingDelete(target);
  };

  const handleDelete = async () => {
    if (!pendingDelete) return;
    const id = pendingDelete.id;
    setDeleting(true);
    try {
      await deleteMonitor(id);
      setMonitors(monitors.filter((m) => m.id !== id));
      if (selectedMonitorId === id) {
        setSelectedMonitorId(null);
      }
      showToast('Monitor deleted', 'success');
      setPendingDelete(null);
    } catch (err) {
      showToast(errorMessage(err, 'Failed to delete'), 'error');
    } finally {
      setDeleting(false);
    }
  };

  const handleToggleEnabled = async (monitor: Monitor) => {
    try {
      const updated = await toggleMonitorEnabled(monitor.id, !monitor.enabled);
      setMonitors((prev) => prev.map((item) => (item.id === updated.id ? updated : item)));
      showToast({
        message: `${updated.name} ${updated.enabled ? 'resumed' : 'paused'}`,
        type: 'success',
        group: `toggle-${updated.id}`,
        action: { label: 'Undo', onClick: () => void handleToggleEnabled(updated) },
      });
    } catch (err) {
      showToast(errorMessage(err, 'Failed to update monitor'), 'error');
    }
  };

  const handleSnooze = async (monitor: Monitor, durationMinutes: number) => {
    try {
      const window = await snoozeMonitor(monitor.id, { duration_minutes: durationMinutes });
      const updated: Monitor = { ...monitor, in_maintenance: true, maintenance_until: window.ends_at };
      setMonitors((prev) => prev.map((item) => (item.id === updated.id ? updated : item)));
      showToast(`Alerts snoozed until ${formatDateTime(window.ends_at)}`, 'success');
    } catch (err) {
      showToast(errorMessage(err, 'Failed to snooze monitor'), 'error');
    }
  };

  // Collect all unique tags from monitors, plus any selected through the URL
  // that no monitor carries, so they can still be cleared.
  const allTags = useMemo(() => {
    const tagSet = new Set<string>(selectedTags);
    monitors.forEach((monitor) => {
      if (monitor.tags) {
        monitor.tags.forEach((tag) => tagSet.add(tag));
      }
    });
    return Array.from(tagSet).sort((a, b) => a.localeCompare(b, undefined, { sensitivity: 'base', numeric: true }));
  }, [monitors, selectedTags]);

  // Types present in the workspace, with counts, most common first.
  // Derived from data so new monitor types appear here with no UI changes.
  const typeCounts = useMemo(() => {
    const counts = new Map<string, number>();
    monitors.forEach((monitor) => {
      counts.set(monitor.type, (counts.get(monitor.type) || 0) + 1);
    });
    return Array.from(counts.entries()).sort(
      (a, b) => b[1] - a[1] || a[0].localeCompare(b[0])
    );
  }, [monitors]);

  // Monitors whose alerts would notify nobody (server-computed per monitor in
  // shared/alertrouting). Drives the filter chip and, with it, the dashboard's
  // deep link.
  const unroutedCount = useMemo(
    () => monitors.filter((monitor) => monitor.enabled && monitor.alert_routing?.reachable === false).length,
    [monitors],
  );

  // Collect all monitor IDs that are members of a group
  const groupMemberIds = useMemo(() => {
    const memberIds = new Set<string>();
    Object.values(groupMembersMap).forEach((members) => {
      members.forEach((m) => memberIds.add(m.id));
    });
    return memberIds;
  }, [groupMembersMap]);

  const statusOf = (monitor: Monitor) => getEffectiveMonitorStatus(monitor, checkResultsMap[monitor.id] || []);

  // Search covers what the row shows: name, target (URL or host) and tags.
  const query = searchTerm.trim().toLowerCase();
  const listFilters: MonitorListFilters = {
    query,
    types: selectedTypes,
    status: statusFilter,
    tags: selectedTags,
    unroutedOnly,
    statusOf,
  };
  // `ignore` leaves one filter out: a filter popover counts its options
  // against everything else that is set.
  const matchesFilters = (monitor: Monitor, ignore?: 'type' | 'tags') =>
    monitorMatchesFilters(monitor, listFilters, ignore);

  // Options for the Type and Tags filter popovers. Each count is the
  // monitors that option would add under the other filters (Datadog,
  // GitHub), so "global 11" never leads to an empty list; the order stays
  // the workspace order so rows do not move while ticking. A URL-selected
  // tag no monitor carries shows 0.
  const typeOptions: FilterOption[] = (() => {
    const counts = new Map<string, number>();
    monitors.forEach((monitor) => {
      if (matchesFilters(monitor, 'type')) counts.set(monitor.type, (counts.get(monitor.type) || 0) + 1);
    });
    return typeCounts.map(([type]) => ({ value: type, label: monitorTypeLabel(type), count: counts.get(type) ?? 0 }));
  })();
  const tagOptions: FilterOption[] = (() => {
    const counts = new Map<string, number>();
    monitors.forEach((monitor) => {
      if (matchesFilters(monitor, 'tags')) monitor.tags?.forEach((tag) => counts.set(tag, (counts.get(tag) || 0) + 1));
    });
    return allTags.map((tag) => ({ value: tag, label: tag, count: counts.get(tag) ?? 0, dotClassName: getTagColor(tag).dot }));
  })();

  // A group also matches when any of its (nested) members match, so searching
  // for a member surfaces the group it lives in.
  const groupHasMatch = (groupId: string) =>
    groupHasMatchingMember(
      groupId,
      (id) => groupMembersMap[id] || [],
      (member) => matchesFilters(member)
    );

  // One sort over groups and monitors together (lib/monitor-list.ts): a
  // group ranks by its worst member under the status sort and is never
  // pinned. Until the first results pass lands every monitor reads "no
  // data", so a results-based sort keeps name order (monitors arrive
  // name-sorted) and re-ranks once instead of jumping with every chunk.
  const sortContext: MonitorSortContext = {
    statusOf,
    resultsOf: (monitor) => checkResultsMap[monitor.id] || [],
    membersOf: (groupId) => groupMembersMap[groupId] || [],
  };
  const ranking = !resultsReady && sortMode.key !== 'name';
  const orderMonitors = (items: Monitor[]) =>
    ranking ? sortMonitorsByName(items) : sortMonitorList(items, sortMode, sortContext);

  // Filter monitors (members of a group only show under their group), then
  // sort the whole filtered set.
  const topLevel = monitors.filter((monitor) => {
    if (groupMemberIds.has(monitor.id)) return false;
    if (matchesFilters(monitor)) return true;
    return monitor.type === 'group' && groupHasMatch(monitor.id);
  });
  const filteredMonitors = orderMonitors(topLevel);
  const hasGroups = filteredMonitors.some((m) => m.type === 'group');

  // No separate select mode: a selection exists as soon as one box is ticked
  // (boxes reveal on row hover), and the floating action bar follows it.
  const {
    selected: selectedMonitorIds,
    active: selectionMode,
    toggle: toggleMonitorSelection,
    toggleAll: toggleSelectAllFiltered,
    deselectAll,
    clear: clearSelection,
    allSelected: allFilteredSelected,
    someSelected: someFilteredSelected,
  } = useMonitorSelection(
    filteredMonitors.map((m) => m.id),
    () => groups.resetGroupInputs()
  );

  const selectedMonitor = monitors.find((m) => m.id === selectedMonitorId) || null;
  const selectedMonitors = monitors.filter((m) => selectedMonitorIds.has(m.id));
  const groups = useMonitorBulkGroups({
    monitors,
    selectedMonitors,
    selectedMonitorIds,
    groupMembersMap,
    deselectAll,
    reloadAll,
  });

  const handleBulkDelete = async () => {
    if (selectedMonitors.length === 0) return;
    setDeleting(true);
    try {
      await Promise.all(selectedMonitors.map((m) => deleteMonitor(m.id)));
      setMonitors(monitors.filter((m) => !selectedMonitorIds.has(m.id)));
      deselectAll();
      showToast('Selected monitors deleted', 'success');
      setConfirmBulkDelete(false);
    } catch (err) {
      showToast(errorMessage(err, 'Failed to delete selected monitors'), 'error');
    } finally {
      setDeleting(false);
    }
  };

  // Status counts for the filter chips. These count every monitor, group
  // members included, under the same rule the status filter applies.
  const statusCounts = countMonitorStatuses(monitors, statusOf);
  // Up and Down always show; the chosen states only when something is in them.
  const visibleStatusFilters = MONITOR_STATUS_FILTERS.filter(
    (id) => id === 'all' || id === 'up' || id === 'down' || statusCounts[id] > 0 || statusFilter === id
  );

  const isEmptyWorkspace = !loading && !error && monitors.length === 0;

  const { rowFlash, statusNews, listNews } = useMonitorListAnnouncements({
    monitors,
    checkResultsMap,
    resultsReady,
    shownCount: filteredMonitors.length,
    filters,
  });

  const rowLayout = ROW_LAYOUTS[selectedMonitor ? 'panel' : 'list'];
  const listStyle = leadStyle(canWrite, hasGroups);

  return (
    <Page width="data" className={canWrite && selectionMode ? 'pb-20' : undefined}>
      <Suspense fallback={null}>
        <MonitorListUrlSync onParams={filters.applyUrlParams} />
      </Suspense>
      <p role="status" className="sr-only">{listNews}</p>
      <p role="status" className="sr-only">{statusNews}</p>
      {/* Header */}
      <PageHeader
        title="Monitors"
        action={
          <div className="flex items-center gap-2">
            {!isEmptyWorkspace && (
              <LiveStatus
                updatedAt={live.updatedAt}
                refreshing={live.refreshing}
                failed={live.failed && monitors.length > 0}
                onRefresh={() => void reloadAll()}
                refreshLabel="Refresh monitors"
                className="mr-1 hidden md:inline-flex"
              />
            )}
            <Button
              variant="ghost"
              size="sm"
              className="hidden sm:inline-flex"
              icon={<Download strokeWidth={1.75} />}
              onClick={handleExport}
              disabled={exporting}
              loading={exporting}
            >
              {exporting ? 'Exporting…' : 'Export'}
            </Button>
            {canWrite && (
              <Button variant="ghost" size="sm" className="hidden sm:inline-flex" icon={<Upload strokeWidth={1.75} />} asChild>
                <Link href="/monitors/import">Import</Link>
              </Button>
            )}
            {canWrite && !isEmptyWorkspace && (
              <Button variant="accent" size="sm" icon={<Plus strokeWidth={1.75} />} asChild>
                <Link href="/monitors/new">Add monitor</Link>
              </Button>
            )}
            {/* Phones: one primary button; export and import behind ⋮. */}
            <div className="sm:hidden">
              <RowMenu label="More actions">
                <RowMenuItem onSelect={handleExport} icon={<Download strokeWidth={1.75} />}>
                  {exporting ? 'Exporting…' : 'Export'}
                </RowMenuItem>
                {canWrite && (
                  <RowMenuItem href="/monitors/import" icon={<Upload strokeWidth={1.75} />}>Import</RowMenuItem>
                )}
              </RowMenu>
            </div>
          </div>
        }
      />

      {/* Filters */}
      {!isEmptyWorkspace && (
        <MonitorListToolbar
          filters={filters}
          visibleStatusFilters={visibleStatusFilters}
          statusCounts={statusCounts}
          typeOptions={typeOptions}
          tagOptions={tagOptions}
          hasTags={allTags.length > 0}
          unroutedCount={unroutedCount}
          showSort={filteredMonitors.length > 0}
          rowLayout={rowLayout}
        />
      )}

      {/* Error */}
      {error && (
        <Callout
          tone="danger"
          action={
            <Button variant="danger" size="xs" onClick={() => void reloadAll()} loading={live.loading}>
              Retry
            </Button>
          }
        >
          {error}
        </Callout>
      )}

      {/* Content */}
      {/* Skeleton on the first load only; a refetch keeps the rows on screen. */}
      {loading && monitors.length === 0 ? (
        <TableSkeleton rows={6} columns={4} label="Loading monitors" />
      ) : monitors.length === 0 ? (
        !error && (
          <SharedEmptyState
            icon={<Activity strokeWidth={1.5} />}
            title="No monitors yet"
            description={
              canWrite
                ? 'Add a monitor to start checking an endpoint, host or service.'
                : 'Monitors appear here once someone with write access adds one.'
            }
            action={canWrite ? { label: 'Add monitor', href: '/monitors/new', icon: <Plus strokeWidth={1.75} /> } : undefined}
          />
        )
      ) : (
        <RowLayoutContext.Provider value={rowLayout}>
        <RowFlashContext.Provider value={rowFlash.flashClass}>
        <div
          className={`grid gap-6 ${
            selectedMonitor
              ? 'lg:grid-cols-[minmax(0,1fr)_320px] 3xl:grid-cols-[minmax(0,1fr)_400px] 4xl:grid-cols-[minmax(0,1fr)_480px]'
              : ''
          }`}
        >
          {/* Monitor List */}
          <div className="glass min-w-0 self-start overflow-hidden" style={listStyle}>
            {filteredMonitors.length === 0 ? (
              <SharedEmptyState
                variant="inline"
                icon={<Search strokeWidth={1.5} />}
                title="No monitors match these filters"
                description={query ? `Nothing matches “${searchTerm.trim()}” with the current filters.` : undefined}
                action={
                  hasActiveFilters ? (
                    <Button variant="ghost" size="sm" icon={<X strokeWidth={1.75} />} onClick={clearFilters}>
                      Clear filters
                    </Button>
                  ) : undefined
                }
              />
            ) : (
              <SelectableContext.Provider value={canWrite}>
              <div
                role="table"
                aria-label="Monitors"
                aria-busy={ranking || undefined}
                onKeyDown={handleRowArrowKeys}
                className="group/list"
              >
              <ListHeader
                sort={sortMode}
                onSortChange={setSortMode}
                ranking={ranking}
                selection={
                  canWrite
                    ? {
                        checked: allFilteredSelected,
                        indeterminate: someFilteredSelected,
                        pinned: selectionMode,
                        onChange: toggleSelectAllFiltered,
                      }
                    : undefined
                }
              />
              <div role="rowgroup" className="divide-y divide-divider">
                {filteredMonitors.map((monitor) => (
                  monitor.type === 'group' ? (
                    <GroupCard
                      key={monitor.id}
                      monitor={monitor}
                      results={checkResultsMap[monitor.id] || []}
                      members={groupMembersMap[monitor.id] || []}
                      groupMembersMap={groupMembersMap}
                      memberResults={checkResultsMap}
                      isSelected={monitor.id === selectedMonitorId}
                      isChecked={selectedMonitorIds.has(monitor.id)}
                      onToggleSelect={() => toggleMonitorSelection(monitor.id)}
                      onToggleMonitorSelection={toggleMonitorSelection}
                      onDeleteMonitor={requestDeleteById}
                      onToggleEnabled={handleToggleEnabled}
                      onSnooze={handleSnooze}
                      selectionMode={selectionMode}
                      onClick={() => selectMonitor(monitor.id)}
                      onDelete={() => requestDeleteById(monitor.id)}
                      onSelectMonitor={selectMonitor}
                      selectedMonitorId={selectedMonitorId}
                      selectedMonitorIds={selectedMonitorIds}
                      orderMembers={orderMonitors}
                      autoExpand={hasActiveFilters && groupHasMatch(monitor.id)}
                      memberMatcher={hasActiveFilters ? matchesFilters : undefined}
                      groupHasMatch={groupHasMatch}
                    />
                  ) : (
                    <MonitorRow
                      key={monitor.id}
                      monitor={monitor}
                      results={checkResultsMap[monitor.id] || []}
                      isSelected={monitor.id === selectedMonitorId}
                      isChecked={selectedMonitorIds.has(monitor.id)}
                      onToggleSelect={() => toggleMonitorSelection(monitor.id)}
                      selectionMode={selectionMode}
                      onClick={() => selectMonitor(monitor.id)}
                      onDelete={() => requestDeleteById(monitor.id)}
                      onToggleEnabled={handleToggleEnabled}
                      onSnooze={handleSnooze}
                    />
                  )
                ))}
              </div>
              </div>
              </SelectableContext.Provider>
            )}
          </div>

          {/* Detail Panel */}
          {selectedMonitor && <div className="hidden lg:block">
            <div className="dashboard-scroll sticky top-6 max-h-[calc(100vh-3rem)] overflow-y-auto overscroll-contain rounded-2xl ![scrollbar-gutter:auto]">
              <DetailPanel
                monitor={selectedMonitor}
                results={selectedMonitor ? checkResultsMap[selectedMonitor.id] || [] : []}
                members={selectedMonitor ? groupMembersMap[selectedMonitor.id] || [] : []}
                memberResults={checkResultsMap}
                onSelectMember={setSelectedMonitorId}
                onClose={() => setSelectedMonitorId(null)}
              />
            </div>
          </div>}
        </div>
        </RowFlashContext.Provider>
        </RowLayoutContext.Provider>
      )}

      {canWrite && selectionMode && (
        <MonitorBulkBar
          selectedCount={selectedMonitorIds.size}
          allSelected={allFilteredSelected}
          shownCount={filteredMonitors.length}
          onToggleAll={toggleSelectAllFiltered}
          groupNameInput={groups.groupNameInput}
          onGroupNameChange={groups.setGroupNameInput}
          canCreateGroup={groups.canCreateGroup}
          onCreateGroup={groups.handleCreateGroup}
          targetGroupId={groups.targetGroupId}
          onTargetGroupChange={groups.setTargetGroupId}
          availableGroups={groups.availableGroups}
          canMoveToGroup={groups.canMoveToGroup}
          onMoveToGroup={groups.handleMoveToGroup}
          onEditAlerting={() => setShowBulkAlerting(true)}
          onDelete={() => setConfirmBulkDelete(true)}
          onClear={clearSelection}
        />
      )}

      {showBulkAlerting && (
        <BulkAlertingModal
          monitorIds={Array.from(selectedMonitorIds)}
          onDone={() => {
            setShowBulkAlerting(false);
            deselectAll();
            showToast('Alerting settings updated', 'success');
            void reloadAll();
          }}
          onCancel={() => setShowBulkAlerting(false)}
        />
      )}

      <ConfirmDialog
        open={pendingDelete !== null}
        title={pendingDelete?.type === 'group' ? 'Delete group' : 'Delete monitor'}
        description={
          pendingDelete
            ? `“${pendingDelete.name}” will be removed along with its check history. This cannot be undone.`
            : undefined
        }
        confirmLabel="Delete"
        loading={deleting}
        onConfirm={handleDelete}
        onCancel={() => !deleting && setPendingDelete(null)}
      />

      <ConfirmDialog
        open={confirmBulkDelete}
        title={`Delete ${selectedMonitors.length} selected monitor${selectedMonitors.length === 1 ? '' : 's'}`}
        description="The selected monitors and their check history will be removed. This cannot be undone."
        confirmLabel="Delete selected"
        loading={deleting}
        onConfirm={handleBulkDelete}
        onCancel={() => !deleting && setConfirmBulkDelete(false)}
      />
    </Page>
  );
}
