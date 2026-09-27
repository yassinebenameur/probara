// Shared building blocks for the inline monitor editors (HTTP, ping, DNS,
// synthetic API, synthetic browser). Everything in lib/monitor-form is pure
// and framework-free so it runs under `npm test` (node --test) without React.

/** Field-keyed validation messages; keys match the form fields that render them. */
export type FormErrors = Record<string, string>;

export type HeaderKV = { key: string; value: string; reveal?: boolean };
export type VariableKV = { key: string; value: string };

export const isSensitiveHeaderName = (name: string): boolean => {
  const n = name.trim().toLowerCase();
  if (!n) return false;
  return (
    n === 'authorization' ||
    n === 'cookie' ||
    n === 'x-api-key' ||
    n === 'api-key' ||
    n === 'x-auth-token' ||
    n.includes('token') ||
    n.includes('secret')
  );
};

export const emptyHeaderRow = (): HeaderKV => ({ key: '', value: '', reveal: false });
export const emptyVariableRow = (): VariableKV => ({ key: '', value: '' });

export const defaultHeaderRows = (): HeaderKV[] => [emptyHeaderRow()];

export const mapHeadersToRows = (headers?: Record<string, string>): HeaderKV[] => {
  if (!headers || Object.keys(headers).length === 0) return defaultHeaderRows();
  return Object.entries(headers).map(([key, value]) => ({ key, value, reveal: false }));
};

export const mapVariablesToRows = (variables?: Record<string, string>): VariableKV[] => {
  if (!variables || Object.keys(variables).length === 0) return [emptyVariableRow()];
  return Object.entries(variables).map(([key, value]) => ({ key, value: String(value) }));
};

/** Rows with both a name and a value; the value is sent untrimmed. */
export const headersFromRows = (rows: HeaderKV[]): Record<string, string> => {
  const headers: Record<string, string> = {};
  for (const row of rows.filter((h) => h.key.trim() && h.value.trim())) {
    headers[row.key.trim()] = row.value;
  }
  return headers;
};

/** A header row that is partly filled in (name without value or vice versa). */
export const hasIncompleteHeaderRow = (rows: HeaderKV[]): boolean =>
  rows
    .filter((h) => h.key.trim() || h.value.trim())
    .some((h) => !h.key.trim() || !h.value.trim());

/** Rows with a key; the value is sent untrimmed. */
export const variablesFromRows = (rows: VariableKV[]): Record<string, string> => {
  const variables: Record<string, string> = {};
  for (const row of rows) {
    const key = row.key.trim();
    if (key) variables[key] = row.value;
  }
  return variables;
};

/** A variable row with a value but no key. */
export const hasKeylessVariableRow = (rows: VariableKV[]): boolean =>
  rows.filter((v) => v.key.trim() || v.value.trim()).some((v) => !v.key.trim());

export const isHttpUrl = (url: string): boolean => url.startsWith('http://') || url.startsWith('https://');

/** Parses an optional positive-or-zero integer field; undefined when blank or not a number. */
export const parseOptionalInt = (input: string): number | undefined => {
  if (!input.trim()) return undefined;
  const value = parseInt(input, 10);
  return isNaN(value) ? undefined : value;
};

/** True when a non-blank integer field does not parse or is below `min`. */
export const isInvalidOptionalInt = (input: string, min: number): boolean => {
  if (!input.trim()) return false;
  const value = parseInt(input, 10);
  return isNaN(value) || value < min;
};

export const hasErrors = (errors: FormErrors): boolean => Object.keys(errors).length > 0;
