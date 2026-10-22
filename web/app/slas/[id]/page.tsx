'use client';

import { useCallback, useEffect, useMemo, useState } from 'react';
import { useParams } from 'next/navigation';
import { ChevronLeft, ChevronRight, Download, FileBraces, FileSpreadsheet, FileText, Pencil, Stamp } from 'lucide-react';
import {
  downloadIssuedSlaReport, downloadSlaReport, getIssuedSlaReports, getSla, getSlaReport, issueSlaReport,
} from '@/lib/api';
import {
  SLA_AGGREGATION_LABELS, SLA_PERIOD_LABELS, formatSlaPct, formatSlaPeriodRange, formatTarget, slaTone,
  type Sla, type SlaExportFormat, type SlaReport, type SlaReportQuery, type SlaReportRef,
} from '@/lib/sla';
import { formatDateTime } from '@/lib/format';
import { sortRows, type SortState, type SortValue } from '@/lib/sort';
import useDocumentTitle from '@/lib/useDocumentTitle';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import IconButton from '@/components/ui/IconButton';
import PageHeader from '@/components/ui/PageHeader';
import Page from '@/components/ui/Page';
import Tabs, { TabPanel, useUrlTab } from '@/components/ui/Tabs';
import SortableHeader, { useUrlSort } from '@/components/ui/SortableHeader';
import EmptyState from '@/components/ui/EmptyState';
import { useToast } from '@/components/ui/ToastProvider';
import { useCanWrite } from '@/components/providers/CurrentUserProvider';
import { SlaFormModal } from '@/components/slas/SlaFormModal';
import { SlaReportView } from '@/components/slas/SlaReportView';
import { SlaStatusPill, TONE_TEXT, saveDownload } from '@/components/slas/SlaVisuals';

const TAB_IDS = ['report', 'issued'] as const;
type TabId = (typeof TAB_IDS)[number];
const TABS: readonly { id: TabId; label: string }[] = [
  { id: 'report', label: 'Report' },
  { id: 'issued', label: 'Issued reports' },
];

const FORMATS: { format: SlaExportFormat; label: string; icon: React.ReactNode }[] = [
  { format: 'pdf', label: 'PDF', icon: <FileText strokeWidth={1.75} /> },
  { format: 'csv', label: 'CSV', icon: <FileSpreadsheet strokeWidth={1.75} /> },
  { format: 'json', label: 'JSON', icon: <FileBraces strokeWidth={1.75} /> },
];

const ISSUED_SORT_KEYS = ['period', 'availability', 'issued'] as const;
type IssuedSortKey = (typeof ISSUED_SORT_KEYS)[number];
const ISSUED_SORT_DEFAULT: SortState<IssuedSortKey> = { key: 'period', dir: 'desc' };
// Newest first; Availability ascending is worst first.
const ISSUED_DEFAULT_DIR = { period: 'desc', issued: 'desc' } as const;
const ISSUED_SORT_VALUE: Record<IssuedSortKey, (r: SlaReportRef) => SortValue> = {
  period: (r) => Date.parse(r.period_start),
  availability: (r) => r.availability_pct,
  issued: (r) => Date.parse(r.issued_at),
};

function errorText(err: unknown, fallback: string): string {
  if (err instanceof Error) return err.message;
  return (err as { message?: string })?.message || fallback;
}

export default function SlaDetailPage() {
  const params = useParams<{ id: string }>();
  const slaId = Array.isArray(params?.id) ? params.id[0] : params?.id;
  const { showToast } = useToast();
  const canWrite = useCanWrite();
  const [tab, setTab] = useUrlTab(TAB_IDS);
  const [sla, setSla] = useState<Sla | null>(null);
  const [report, setReport] = useState<SlaReport | null>(null);
  const [query, setQuery] = useState<SlaReportQuery>({});
  const [customFrom, setCustomFrom] = useState('');
  const [customTo, setCustomTo] = useState('');
  const [issued, setIssued] = useState<SlaReportRef[]>([]);
  const [loading, setLoading] = useState(true);
  const [reportLoading, setReportLoading] = useState(false);
  const [error, setError] = useState('');
  const [editing, setEditing] = useState(false);
  const [downloading, setDownloading] = useState<string | null>(null);
  const [issuedSort, setIssuedSort] = useUrlSort(ISSUED_SORT_KEYS, ISSUED_SORT_DEFAULT, ISSUED_DEFAULT_DIR);
  useDocumentTitle(sla?.name);
  const sortedIssued = useMemo(
    () => sortRows(issued, ISSUED_SORT_VALUE[issuedSort.key], issuedSort.dir, (r) => -Date.parse(r.period_start)),
    [issued, issuedSort]
  );
  const [issuing, setIssuing] = useState(false);

  const loadIssued = useCallback(async () => {
    if (!slaId) return;
    try {
      setIssued(await getIssuedSlaReports(slaId));
    } catch (err) {
      console.error('Failed to load issued reports:', err);
    }
  }, [slaId]);

  useEffect(() => {
    if (!slaId) return;
    let cancelled = false;
    (async () => {
      try {
        setLoading(true);
        const loaded = await getSla(slaId);
        if (!cancelled) setSla(loaded);
      } catch (err) {
        if (!cancelled) setError(errorText(err, 'Failed to load SLA'));
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    loadIssued();
    return () => {
      cancelled = true;
    };
  }, [slaId, loadIssued]);

  useEffect(() => {
    if (!slaId || !sla) return;
    let cancelled = false;
    (async () => {
      try {
        setReportLoading(true);
        const r = await getSlaReport(slaId, query);
        if (!cancelled) {
          setReport(r);
          setError('');
        }
      } catch (err) {
        if (!cancelled) setError(errorText(err, 'Failed to build report'));
      } finally {
        if (!cancelled) setReportLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [slaId, sla, query]);

  const download = async (format: SlaExportFormat, reportId?: string) => {
    if (!slaId) return;
    setDownloading(`${reportId ?? 'live'}-${format}`);
    try {
      saveDownload(reportId ? await downloadIssuedSlaReport(reportId, format) : await downloadSlaReport(slaId, query, format));
    } catch (err) {
      showToast(errorText(err, 'Download failed'), 'error');
    } finally {
      setDownloading(null);
    }
  };

  // The last closed period: the running report's previous key, or the shown
  // period itself when it is closed.
  const issuablePeriod = report && !report.period.is_custom
    ? report.period.is_closed ? report.period.key : report.period.previous_key
    : undefined;
  const alreadyIssued = issued.some((r) => r.period_key === issuablePeriod);
  // Readable when the shown period is the one issued; the previous period's
  // dates are not in the response, so it keeps its key.
  const issuablePeriodLabel =
    report && issuablePeriod && report.period.is_closed && sla
      ? `${formatSlaPeriodRange(report.period.start, report.period.end, sla.timezone)} (${issuablePeriod})`
      : issuablePeriod;

  const issue = async () => {
    if (!slaId || !issuablePeriod) return;
    setIssuing(true);
    try {
      await issueSlaReport(slaId, issuablePeriod);
      showToast(`Report for ${issuablePeriod} issued`, 'success');
      await loadIssued();
      setTab('issued');
    } catch (err) {
      showToast(errorText(err, 'Failed to issue report'), 'error');
    } finally {
      setIssuing(false);
    }
  };

  if (loading) {
    return (
      <div className="glass mx-auto w-full max-w-[100rem] p-8 text-center text-sm text-slate-500" role="status">
        Loading…
      </div>
    );
  }
  if (!sla) {
    return <Callout tone="danger">{error || 'SLA not found'}</Callout>;
  }

  const period = report?.period;

  return (
    <Page width="detail">
      <PageHeader
        breadcrumb={[{ label: 'SLAs', href: '/slas' }, { label: sla.name }]}
        title={sla.name}
        subtitle={[
          `${formatTarget(sla.target_pct)} target`,
          `${SLA_PERIOD_LABELS[sla.period]} periods in ${sla.timezone}`,
          SLA_AGGREGATION_LABELS[sla.aggregation],
          sla.degraded_counts_as_down ? 'Degraded counts as down' : null,
        ]}
        action={
          canWrite ? (
            <Button variant="ghost" size="sm" icon={<Pencil strokeWidth={1.75} />} onClick={() => setEditing(true)}>
              Edit
            </Button>
          ) : undefined
        }
      />

      <div className="flex flex-wrap items-center justify-between gap-3">
        <Tabs tabs={TABS} active={tab} onChange={setTab} idBase="sla" />
        {tab === 'report' && (
          <div className="flex flex-wrap items-center gap-1.5">
            {FORMATS.map((f) => (
              <Button key={f.format} variant="ghost" size="xs" icon={<Download strokeWidth={1.75} />}
                loading={downloading === `live-${f.format}`} disabled={!report} onClick={() => download(f.format)}>
                {f.label}
              </Button>
            ))}
            {canWrite && (
              <Button variant="ghost" size="xs" icon={<Stamp strokeWidth={1.75} />} loading={issuing}
                disabled={!issuablePeriod || alreadyIssued} onClick={issue}
                title={
                  !issuablePeriod
                    ? 'No closed period to issue yet'
                    : alreadyIssued
                      ? `${issuablePeriodLabel} is already issued`
                      : `Freeze ${issuablePeriodLabel} as an immutable report`
                }>
                Issue report
              </Button>
            )}
          </div>
        )}
      </div>

      <TabPanel idBase="sla" id={tab}>
      {tab === 'report' ? (
        <div className="space-y-4">
          <div className="glass flex flex-wrap items-center gap-3 px-2.5 py-2">
            <div className="flex items-center gap-1">
              <IconButton size="sm" icon={<ChevronLeft strokeWidth={1.75} />} label="Previous period"
                disabled={!period?.previous_key} onClick={() => setQuery({ period: period?.previous_key })} />
              <span className="min-w-[9rem] text-center text-sm tabular-nums text-white" title={period?.key}>
                {period ? formatSlaPeriodRange(period.start, period.end, sla.timezone) : '…'}
              </span>
              <IconButton size="sm" icon={<ChevronRight strokeWidth={1.75} />} label="Next period"
                disabled={!period?.next_key} onClick={() => setQuery({ period: period?.next_key })} />
              {(query.period || query.from) && (
                <Button variant="subtle" size="xs" onClick={() => setQuery({})}>Current</Button>
              )}
            </div>
            {/* Below lg the range gets its own full-width row as a grid, so the
                two dates stay side by side instead of wrapping one at a time. */}
            <div className="grid w-full grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)_auto] items-center gap-1.5 text-xs text-slate-400 sm:grid-cols-[auto_minmax(0,1fr)_auto_minmax(0,1fr)_auto] lg:ml-auto lg:flex lg:w-auto lg:flex-wrap">
              <span className="col-span-4 px-1 sm:col-span-1 sm:px-0">Custom</span>
              <input type="date" value={customFrom} onChange={(e) => setCustomFrom(e.target.value)}
                className="input input-sm h-7 !py-0 tabular-nums lg:!w-40" aria-label="From date" />
              <span className="text-slate-500">–</span>
              <input type="date" value={customTo} onChange={(e) => setCustomTo(e.target.value)}
                className="input input-sm h-7 !py-0 tabular-nums lg:!w-40" aria-label="To date" />
              <Button variant="ghost" size="xs" disabled={!customFrom || !customTo}
                onClick={() => setQuery({ from: customFrom, to: customTo })}>
                Apply
              </Button>
            </div>
          </div>

          {error && <Callout tone="danger">{error}</Callout>}
          {/* SlaReportView draws its own sections as glass panels; this
              wrapper only dims them while a period reloads, so it must never
              become a surface itself (no blur inside blur). The dim goes on
              the panels' contents and the period caption, not on the wrapper:
              an ancestor with opacity < 1 is a backdrop root, and every panel
              under it would lose its frost for the length of the reload. */}
          {report ? (
            <div className={reportLoading
              ? '[&_.glass>*]:opacity-60 [&_.glass>*]:transition-opacity [&>div>div:first-child]:opacity-60 [&>div>div:first-child]:transition-opacity'
              : ''}>
              <SlaReportView report={report} />
            </div>
          ) : (
            reportLoading && <div className="glass p-8 text-center text-sm text-slate-500">Building report…</div>
          )}
        </div>
      ) : issued.length === 0 ? (
        <EmptyState
          icon={<Stamp strokeWidth={1.5} />}
          title="No issued reports"
          description="Issuing freezes a closed period's report. Live reports of past periods can change if maintenance windows are edited or history is deleted; issued ones never do."
        />
      ) : (
        <div className="glass overflow-hidden">
          <div className="overflow-x-auto">
            <table className="data-table min-w-[640px] text-sm">
              <thead>
                <tr>
                  <SortableHeader
                    label="Period"
                    sortKey="period"
                    sort={issuedSort}
                    onSort={setIssuedSort}
                    defaultDir={ISSUED_DEFAULT_DIR}
                    title="Most recent first"
                  />
                  <SortableHeader
                    label="Availability"
                    sortKey="availability"
                    sort={issuedSort}
                    onSort={setIssuedSort}
                    defaultDir={ISSUED_DEFAULT_DIR}
                    title="Lowest first"
                  />
                  <th>Target</th>
                  <th>Status</th>
                  <SortableHeader
                    label="Issued"
                    sortKey="issued"
                    sort={issuedSort}
                    onSort={setIssuedSort}
                    defaultDir={ISSUED_DEFAULT_DIR}
                    title="Most recent first"
                  />
                  <th>
                    <span className="sr-only">Downloads</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {sortedIssued.map((r) => (
                  <tr key={r.id}>
                    <td className="whitespace-nowrap tabular-nums text-white" title={r.period_key}>
                      {formatSlaPeriodRange(r.period_start, r.period_end, sla.timezone)}
                    </td>
                    <td className={`tabular-nums ${TONE_TEXT[slaTone(r.met)]}`}>{formatSlaPct(r.availability_pct)}</td>
                    <td className="tabular-nums text-slate-300">{formatTarget(r.target_pct)}</td>
                    <td><SlaStatusPill met={r.met} /></td>
                    <td className="whitespace-nowrap text-ui tabular-nums text-slate-400">
                      {formatDateTime(r.issued_at)}
                      <div className="text-xs text-slate-500">{r.issued_by}</div>
                    </td>
                    <td>
                      <div className="flex justify-end gap-0.5">
                        {FORMATS.map((f) => (
                          <IconButton key={f.format} icon={f.icon} label={`Download ${f.label}`}
                            loading={downloading === `${r.id}-${f.format}`} onClick={() => download(f.format, r.id)} />
                        ))}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
      </TabPanel>

      {editing && (
        <SlaFormModal
          sla={sla}
          onDone={(saved) => {
            setEditing(false);
            setSla(saved);
            showToast('SLA updated', 'success');
          }}
          onCancel={() => setEditing(false)}
        />
      )}
    </Page>
  );
}
