'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import { Search } from 'lucide-react';

import { getAlerts } from '@/lib/api';
import {
  ALERT_PICKER_PAGE_SIZE,
  ALERT_QUERY_MAX_LENGTH,
  type AlertPickerResults,
  mergeAlertPage,
  normalizeAlertQuery,
} from '@/lib/alert-picker';
import { alertSubject } from '@/lib/alert-toast';
import { formatDateTime, formatDateTimeFull, pluralize } from '@/lib/format';
import { ALERT_STATUS_LABEL, alertStatusTone } from '@/lib/status-tones';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import Dialog from '@/components/ui/Dialog';
import Pill from '@/components/ui/Pill';
import SelectCheckbox from '@/components/ui/SelectCheckbox';

const SEARCH_DEBOUNCE_MS = 250;

interface AttachAlertsDialogProps {
  onClose: () => void;
  /** Alerts already on the incident: listed, but checked and locked. */
  linkedAlertIds: ReadonlySet<string>;
  /** Attaches the picked alerts; the dialog closes when it resolves. */
  onAttach: (alertIds: string[]) => Promise<void>;
}

/**
 * Picks alerts to attach to an incident from the whole alert history, not
 * just the newest page: the list is the API's newest-first order, searched on
 * the server (`q=`: monitor or mesh location name, or an alert ID) and read a
 * page at a time with "Load more" (lib/alert-picker.ts). Mount it only while
 * open, so each opening starts a fresh search.
 */
export default function AttachAlertsDialog({ onClose, linkedAlertIds, onAttach }: AttachAlertsDialogProps) {
  const [term, setTerm] = useState('');
  const [query, setQuery] = useState('');
  const [results, setResults] = useState<AlertPickerResults | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [attaching, setAttaching] = useState(false);
  const requestRef = useRef(0);

  useEffect(() => {
    const next = normalizeAlertQuery(term);
    if (next === query) return;
    const timer = setTimeout(() => setQuery(next), SEARCH_DEBOUNCE_MS);
    return () => clearTimeout(timer);
  }, [term, query]);

  const loadPage = useCallback(async (q: string, page: number) => {
    const request = ++requestRef.current;
    setLoading(true);
    setError('');
    try {
      const response = await getAlerts({ page, page_size: ALERT_PICKER_PAGE_SIZE, ...(q && { q }) });
      if (request !== requestRef.current) return;
      setResults((previous) => mergeAlertPage(previous, page, response));
    } catch (err) {
      if (request !== requestRef.current) return;
      setError(err instanceof Error ? err.message : 'Failed to load alerts');
      if (page === 1) setResults(null);
    } finally {
      if (request === requestRef.current) setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadPage(query, 1);
  }, [query, loadPage]);

  const toggle = (id: string) =>
    setSelected((current) => {
      const next = new Set(current);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });

  // A retry after a part-way failure skips what is linked by now.
  const pending = Array.from(selected).filter((id) => !linkedAlertIds.has(id));

  const handleAttach = async () => {
    if (pending.length === 0) return;
    setAttaching(true);
    try {
      await onAttach(pending);
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to attach alerts');
    } finally {
      setAttaching(false);
    }
  };

  const items = results?.items ?? [];
  const total = results?.total ?? 0;
  const firstLoad = loading && !results;

  return (
    <Dialog
      onClose={onClose}
      busy={attaching}
      size="lg"
      title="Attach alerts"
      description="Search every alert in this workspace, newest first."
      footer={
        <>
          <Button type="button" variant="ghost" size="sm" onClick={onClose} disabled={attaching}>
            Cancel
          </Button>
          <Button
            type="button"
            variant="accent"
            size="sm"
            onClick={handleAttach}
            disabled={attaching || pending.length === 0}
            loading={attaching}
          >
            {pending.length > 1 ? `Attach ${pending.length} alerts` : 'Attach alert'}
          </Button>
        </>
      }
    >
      <div className="relative">
        <Search
          className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-slate-500"
          strokeWidth={1.75}
          aria-hidden="true"
        />
        <input
          type="search"
          value={term}
          onChange={(event) => setTerm(event.target.value)}
          maxLength={ALERT_QUERY_MAX_LENGTH}
          placeholder="Search by monitor, location or alert ID"
          // `.input` sets padding via shorthand and wins the cascade, so the
          // icon clearance has to be important or the placeholder sits under it.
          className="input !pl-9"
          aria-label="Search alerts"
          autoFocus
        />
      </div>

      <p className="text-xs text-slate-400" role="status">
        {firstLoad
          ? 'Loading alerts…'
          : results
            ? `${pluralize(total, query ? 'matching alert' : 'alert')}${pending.length > 0 ? `, ${pending.length} selected` : ''}`
            : ''}
      </p>

      {error && <Callout tone="danger">{error}</Callout>}

      <div className="glass-well relative max-h-[22rem] overflow-y-auto p-1.5">
        {firstLoad ? (
          <p className="py-6 text-center text-sm text-slate-500">Loading…</p>
        ) : items.length === 0 ? (
          <p className="py-6 text-center text-sm text-slate-500">
            {query ? 'No alerts match this search' : 'No alerts yet'}
          </p>
        ) : (
          <ul className="space-y-1">
            {items.map((alert) => {
              const linked = linkedAlertIds.has(alert.id);
              const isSelected = linked || selected.has(alert.id);
              const subject = alertSubject(alert);
              return (
                <li
                  key={alert.id}
                  className={`flex items-center gap-3 rounded-lg px-3 py-2 transition-colors ${
                    linked ? '' : selected.has(alert.id) ? 'bg-well-strong' : 'hover:bg-well'
                  }`}
                >
                  <SelectCheckbox
                    checked={isSelected}
                    disabled={linked}
                    onChange={() => toggle(alert.id)}
                    label={linked ? `${subject} is already linked` : `Select alert for ${subject}`}
                  />
                  {/* A bigger pointer target for the checkbox beside it; keyboard and
                      screen-reader users get the checkbox itself (one stop per row). */}
                  <button
                    type="button"
                    tabIndex={-1}
                    aria-hidden="true"
                    disabled={linked}
                    onClick={() => toggle(alert.id)}
                    className={`flex min-w-0 flex-1 items-center gap-3 text-left ${linked ? 'cursor-default' : 'cursor-pointer'}`}
                  >
                    <span className="min-w-0 flex-1">
                      <span className={`block truncate text-sm font-medium ${linked ? 'text-slate-400' : 'text-white'}`}>
                        {subject}
                      </span>
                      <span className="mt-0.5 flex flex-wrap items-center gap-2 text-xs tabular-nums text-slate-500">
                        <Pill size="xs" dot tone={alertStatusTone(alert.status)}>{ALERT_STATUS_LABEL[alert.status]}</Pill>
                        <span title={formatDateTimeFull(alert.triggered_at)}>{formatDateTime(alert.triggered_at)}</span>
                        {linked && <span>Linked</span>}
                      </span>
                    </span>
                  </button>
                </li>
              );
            })}
          </ul>
        )}
        {results?.hasMore && (
          <div className="flex justify-center pt-2">
            <Button
              type="button"
              variant="subtle"
              size="xs"
              onClick={() => void loadPage(query, results.page + 1)}
              loading={loading}
              disabled={loading}
            >
              Load more ({total - results.fetched} older)
            </Button>
          </div>
        )}
      </div>
    </Dialog>
  );
}
