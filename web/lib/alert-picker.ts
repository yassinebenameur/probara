// The incident "attach alerts" picker reads the alert list a page at a time
// through the API's q= search, so every alert stays reachable, not just the
// newest page. These are its pure parts (node-tested).

import type { Alert, AlertListResponse } from './types';

export const ALERT_PICKER_PAGE_SIZE = 25;
/** The API refuses a longer q= (maxAlertQueryLength in handlers/alerts). */
export const ALERT_QUERY_MAX_LENGTH = 200;

/** The term sent as q=: trimmed and cut to the API's cap; '' means no search. */
export function normalizeAlertQuery(raw: string): string {
  return Array.from(raw.trim()).slice(0, ALERT_QUERY_MAX_LENGTH).join('').trim();
}

export interface AlertPickerResults {
  items: Alert[];
  /** The last page read (1-based). */
  page: number;
  /** Rows the API returned so far, repeats included: what the offset has covered. */
  fetched: number;
  /** The server's total for the current search. */
  total: number;
  /** False once the server has no further page. */
  hasMore: boolean;
}

/**
 * Folds one page into the results: page 1 starts over, later pages append.
 * Pages are offset-based, so an alert raised while the picker is open shifts
 * the next page and repeats a row already shown: keep each id once.
 */
export function mergeAlertPage(
  previous: AlertPickerResults | null,
  page: number,
  response: AlertListResponse
): AlertPickerResults {
  const batch = response.items || [];
  const base = page === 1 || !previous ? [] : previous.items;
  const seen = new Set(base.map((alert) => alert.id));
  const items = [...base];
  batch.forEach((alert) => {
    if (!seen.has(alert.id)) {
      seen.add(alert.id);
      items.push(alert);
    }
  });
  const fetched = (page === 1 || !previous ? 0 : previous.fetched) + batch.length;
  const total = response.total || 0;
  return { items, page, fetched, total, hasMore: batch.length > 0 && fetched < total };
}
