'use client';

import { useCallback, useEffect, useState } from 'react';
import { useParams } from 'next/navigation';
import { ChevronLeft, ChevronRight, Download, FileBraces, FileSpreadsheet, FileText, Pencil, Stamp } from 'lucide-react';
import {
  downloadIssuedSlaReport, downloadSlaReport, getIssuedSlaReports, getSla, getSlaReport, issueSlaReport,
} from '@/lib/api';
import {
  SLA_PERIOD_LABELS, formatSlaPct, formatTarget, slaTone,
  type Sla, type SlaExportFormat, type SlaReport, type SlaReportQuery, type SlaReportRef,
} from '@/lib/sla';
import { formatDateTime } from '@/lib/format';
import Button from '@/components/ui/Button';
import IconButton from '@/components/ui/IconButton';
import PageHeader from '@/components/ui/PageHeader';
import Tabs, { useUrlTab } from '@/components/ui/Tabs';
import EmptyState from '@/components/ui/EmptyState';
import { useToast } from '@/components/ui/ToastProvider';
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

function errorText(err: unknown, fallback: string): string {
  if (err instanceof Error) return err.message;
  return (err as { message?: string })?.message || fallback;
}

export default function SlaDetailPage() {
  const params = useParams<{ id: string }>();
  const slaId = Array.isArray(params?.id) ? params.id[0] : params?.id;
  const { showToast } = useToast();
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

  if (loading) return <div className="py-12 text-center text-sm text-slate-500">Loading…</div>;
  if (!sla) {
    return (
      <div className="rounded-lg border border-rose-500/30 bg-rose-500/10 px-4 py-3 text-sm text-rose-300">
        {error || 'SLA not found'}
      </div>
    );
  }

  const period = report?.period;

  return (
    <div className="space-y-5">
      <PageHeader
        breadcrumb={[{ label: 'SLAs', href: '/slas' }, { label: sla.name }]}
        title={sla.name}
        subtitle={`${formatTarget(sla.target_pct)} target · ${SLA_PERIOD_LABELS[sla.period].toLowerCase()} periods in ${sla.timezone} · ${
          sla.aggregation === 'serial' ? 'serial (all must be up)' : 'mean of monitors'}${
          sla.degraded_counts_as_down ? ' · degraded counts as down' : ''}`}
        action={
          <Button variant="ghost" size="sm" icon={<Pencil strokeWidth={1.75} />} onClick={() => setEditing(true)}>
            Edit
          </Button>
        }
      />

      <div className="flex flex-wrap items-center justify-between gap-3">
        <Tabs tabs={TABS} active={tab} onChange={setTab} />
        {tab === 'report' && (
          <div className="flex flex-wrap items-center gap-1.5">
            {FORMATS.map((f) => (
              <Button key={f.format} variant="ghost" size="xs" icon={<Download strokeWidth={1.75} />}
                loading={downloading === `live-${f.format}`} disabled={!report} onClick={() => download(f.format)}>
                {f.label}
              </Button>
            ))}
            <Button variant="ghost" size="xs" icon={<Stamp strokeWidth={1.75} />} loading={issuing}
              disabled={!issuablePeriod || alreadyIssued} onClick={issue}
              title={alreadyIssued ? `${issuablePeriod} is already issued` : 'Freeze this closed period as an immutable report'}>
              {issuablePeriod ? `Issue ${issuablePeriod}` : 'Issue report'}
            </Button>
          </div>
        )}
      </div>

      {tab === 'report' ? (
        <div className="space-y-4">
          <div className="flex flex-wrap items-center gap-3 rounded-lg border border-white/[0.06] bg-slate-900/40 px-3 py-2">
            <div className="flex items-center gap-1">
              <IconButton icon={<ChevronLeft strokeWidth={1.75} />} label="Previous period"
                disabled={!period?.previous_key} onClick={() => setQuery({ period: period?.previous_key })} />
              <span className="min-w-[7rem] text-center font-mono text-sm text-white">
                {period?.is_custom ? 'Custom range' : period?.key ?? '…'}
              </span>
              <IconButton icon={<ChevronRight strokeWidth={1.75} />} label="Next period"
                disabled={!period?.next_key} onClick={() => setQuery({ period: period?.next_key })} />
              {(query.period || query.from) && (
                <Button variant="subtle" size="xs" onClick={() => setQuery({})}>Current</Button>
              )}
            </div>
            <div className="ml-auto flex flex-wrap items-center gap-1.5 text-xs text-slate-500">
              <span>Custom</span>
              <input type="date" value={customFrom} onChange={(e) => setCustomFrom(e.target.value)}
                className="input input-sm !w-40" aria-label="From date" />
              <span>–</span>
              <input type="date" value={customTo} onChange={(e) => setCustomTo(e.target.value)}
                className="input input-sm !w-40" aria-label="To date" />
              <Button variant="ghost" size="xs" disabled={!customFrom || !customTo}
                onClick={() => setQuery({ from: customFrom, to: customTo })}>
                Apply
              </Button>
            </div>
          </div>

          {error && (
            <div className="rounded-lg border border-rose-500/30 bg-rose-500/10 px-4 py-3 text-sm text-rose-300">{error}</div>
          )}
          {report ? (
            <div className={reportLoading ? 'opacity-60 transition-opacity' : ''}>
              <SlaReportView report={report} />
            </div>
          ) : (
            reportLoading && <div className="py-12 text-center text-sm text-slate-500">Building report…</div>
          )}
        </div>
      ) : issued.length === 0 ? (
        <EmptyState
          icon={<Stamp strokeWidth={1.5} />}
          title="No issued reports"
          description="Issuing freezes a closed period's report. Live reports of past periods can change if maintenance windows are edited or history is deleted; issued ones never do."
        />
      ) : (
        <div className="overflow-x-auto rounded-xl border border-white/[0.06] bg-slate-900/40">
          <table className="w-full min-w-[640px] text-sm">
            <thead>
              <tr className="border-b border-white/[0.06] text-left text-[11px] uppercase tracking-wider text-slate-500">
                <th className="px-4 py-2.5 font-medium">Period</th>
                <th className="px-4 py-2.5 font-medium">Availability</th>
                <th className="px-4 py-2.5 font-medium">Target</th>
                <th className="px-4 py-2.5 font-medium">Status</th>
                <th className="px-4 py-2.5 font-medium">Issued</th>
                <th className="px-4 py-2.5" />
              </tr>
            </thead>
            <tbody>
              {issued.map((r) => (
                <tr key={r.id} className="border-b border-white/[0.04] last:border-0">
                  <td className="px-4 py-3 font-mono text-white">{r.period_key}</td>
                  <td className={`px-4 py-3 font-mono ${TONE_TEXT[slaTone(r.met)]}`}>{formatSlaPct(r.availability_pct)}</td>
                  <td className="px-4 py-3 font-mono text-slate-300">{formatTarget(r.target_pct)}</td>
                  <td className="px-4 py-3"><SlaStatusPill met={r.met} /></td>
                  <td className="px-4 py-3 text-xs text-slate-400">
                    {formatDateTime(r.issued_at)}
                    <div className="text-slate-500">{r.issued_by}</div>
                  </td>
                  <td className="px-4 py-3">
                    <div className="flex justify-end gap-1">
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
      )}

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
    </div>
  );
}
