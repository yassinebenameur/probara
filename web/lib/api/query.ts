export type QueryValue = string | number | boolean | null | undefined | readonly string[];

/**
 * Appends a query string to `path`. `undefined`, `null` and `''` are left out
 * (an unset filter is not sent); `false` and `0` are real values and are sent;
 * an array repeats its key once per element (`tag=a&tag=b`).
 */
export function withQuery(path: string, params?: Record<string, QueryValue>): string {
  const query = new URLSearchParams();
  for (const [key, value] of Object.entries(params ?? {})) {
    if (value === undefined || value === null || value === '') continue;
    if (typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean') {
      query.append(key, String(value));
    } else {
      for (const item of value) query.append(key, item);
    }
  }
  const queryString = query.toString();
  return queryString ? `${path}?${queryString}` : path;
}
