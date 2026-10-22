/**
 * The one sort helper for list pages. Columns sort through `compareValues`,
 * so every table agrees on how nulls, case, numbers and dates order:
 *
 * - null, undefined, NaN and invalid dates sort LAST in both directions
 *   ("—" never leads a descending column);
 * - strings compare naturally and case-insensitively ("web-9" < "web-10",
 *   "api" next to "API");
 * - numbers numerically, Dates by epoch, booleans false < true.
 *
 * Sort state lives in the URL as `?sort=<key>&dir=asc|desc` with defaults
 * omitted (`readSortParams` / `writeSortParams`); the header control is
 * components/ui/SortableHeader.tsx. Sort the full filtered set, then page;
 * a server-paged list passes the sort to the API instead.
 */

export type SortDir = 'asc' | 'desc';

export interface SortState<K extends string = string> {
  key: K;
  dir: SortDir;
}

export type SortValue = string | number | boolean | Date | null | undefined;

/** A key's default direction: one for all keys, a per-key table, or a function. */
export type DefaultDirFor<K extends string> = SortDir | Partial<Record<K, SortDir>> | ((key: K) => SortDir);

const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' });

// Missing values (and values that cannot be ordered) become null.
function normalize(value: SortValue): string | number | null {
  if (value === null || value === undefined) return null;
  if (value instanceof Date) {
    const t = value.getTime();
    return Number.isNaN(t) ? null : t;
  }
  if (typeof value === 'boolean') return value ? 1 : 0;
  if (typeof value === 'number') return Number.isNaN(value) ? null : value;
  return value;
}

/**
 * Compare two cell values in `dir`. Missing values always sort after present
 * ones, whatever the direction. A string compared with a number orders by
 * its string form.
 */
export function compareValues(a: SortValue, b: SortValue, dir: SortDir = 'asc'): number {
  const x = normalize(a);
  const y = normalize(b);
  if (x === null || y === null) {
    if (x === y) return 0;
    return x === null ? 1 : -1;
  }
  let result: number;
  if (typeof x === 'number' && typeof y === 'number') {
    result = x === y ? 0 : x < y ? -1 : 1;
  } else {
    result = collator.compare(String(x), String(y));
  }
  return dir === 'desc' ? -result : result;
}

/**
 * A sorted copy of `rows` (stable: equal rows keep their input order).
 * `tieBreak` orders rows the main key leaves equal, always ascending — pass
 * the row's name so a column of equal values reads alphabetically.
 */
export function sortRows<T>(
  rows: readonly T[],
  accessor: (row: T) => SortValue,
  dir: SortDir = 'asc',
  tieBreak?: (row: T) => SortValue
): T[] {
  const keyed = rows.map((row, index) => ({ row, index, value: accessor(row), tie: tieBreak?.(row) }));
  keyed.sort(
    (a, b) =>
      compareValues(a.value, b.value, dir) ||
      (tieBreak ? compareValues(a.tie, b.tie, 'asc') : 0) ||
      a.index - b.index
  );
  return keyed.map((entry) => entry.row);
}

/** The default direction for `key` (`asc` when nothing says otherwise). */
export function defaultDirOf<K extends string>(key: K, defaultDirFor?: DefaultDirFor<K>): SortDir {
  if (!defaultDirFor) return 'asc';
  if (typeof defaultDirFor === 'string') return defaultDirFor;
  if (typeof defaultDirFor === 'function') return defaultDirFor(key);
  return defaultDirFor[key] ?? 'asc';
}

/**
 * The state after a click on `key`'s header: the same key flips direction,
 * another key starts at its default direction (worst-first for a status
 * column, slowest-first for latency, A→Z for a name).
 */
export function nextSort<K extends string>(
  current: SortState<K>,
  key: K,
  defaultDirFor?: DefaultDirFor<K>
): SortState<K> {
  if (current.key === key) return { key, dir: current.dir === 'asc' ? 'desc' : 'asc' };
  return { key, dir: defaultDirOf(key, defaultDirFor) };
}

/**
 * Read `?sort=&dir=`. An unknown key falls back to `fallback`; a missing or
 * invalid dir falls back to the key's default direction.
 */
export function readSortParams<K extends string>(
  params: URLSearchParams,
  keys: readonly K[],
  fallback: SortState<K>,
  defaultDirFor?: DefaultDirFor<K>
): SortState<K> {
  const rawKey = params.get('sort');
  const key = rawKey && (keys as readonly string[]).includes(rawKey) ? (rawKey as K) : fallback.key;
  const rawDir = params.get('dir');
  if (rawDir === 'asc' || rawDir === 'desc') return { key, dir: rawDir };
  return { key, dir: key === fallback.key ? fallback.dir : defaultDirOf(key, defaultDirFor) };
}

/**
 * Write `sort` into `params` (mutates and returns them). The fallback key and
 * a key's default direction are left out, so a default view has a clean URL.
 */
export function writeSortParams<K extends string>(
  params: URLSearchParams,
  sort: SortState<K>,
  fallback: SortState<K>,
  defaultDirFor?: DefaultDirFor<K>
): URLSearchParams {
  params.delete('sort');
  params.delete('dir');
  if (sort.key !== fallback.key) params.set('sort', sort.key);
  const implied = sort.key === fallback.key ? fallback.dir : defaultDirOf(sort.key, defaultDirFor);
  if (sort.dir !== implied) params.set('dir', sort.dir);
  return params;
}
