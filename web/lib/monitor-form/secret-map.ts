import { MASKED_SECRET } from '../types';

// Key/value rows of a config map that holds secrets: http `headers` /
// `secret_headers`, grpc `metadata` / `secret_metadata`, synthetic
// `secret_variables`. The server's merge contract (CLAUDE.md, "Monitor config
// secrets") shapes the submit: reads mask every secret value as "***"; a
// submitted secret map is edited per key ("***" keeps that key's stored
// value, a key left out is removed), while an absent map keeps them all — so
// a form that loaded stored secrets always submits the map, even empty.

/** One map row. `secret` rows go to the secret map. */
export type SecretKV = {
  key: string;
  value: string;
  /** Show a secret value as typed (UI only). */
  reveal?: boolean;
  /** The row's value is a secret (forced on for credential-named headers). */
  secret?: boolean;
  /**
   * Edit mode only: a stored value sits behind the mask. A blank value keeps
   * it, while the name still matches `originalKey`.
   */
  hasStoredValue?: boolean;
  originalKey?: string;
};

/** Rows for a plain map. */
export function plainRowsFromMap(values: Record<string, string> | undefined): SecretKV[] {
  return Object.entries(values || {}).map(([key, value]) => ({ key, value: String(value), reveal: false }));
}

/**
 * Rows for a loaded secret map. Only an edit has a stored value behind a
 * mask; on create (a clone) a masked row starts blank and must be re-entered,
 * since the API drops a mask it has nothing to resolve against.
 */
export function secretRowsFromMap(values: Record<string, string> | undefined, isEditMode: boolean): SecretKV[] {
  return Object.entries(values || {}).map(([key, value]) => {
    const masked = value === MASKED_SECRET;
    const stored = masked && isEditMode;
    return {
      key,
      value: masked ? '' : String(value),
      reveal: false,
      secret: true,
      hasStoredValue: stored,
      originalKey: stored ? key : undefined,
    };
  });
}

/** A row after its name is edited: renaming it away drops the stored value. */
export function renameSecretRow(row: SecretKV, key: string): SecretKV {
  const keepsStoredValue = Boolean(
    row.hasStoredValue && row.originalKey && row.originalKey.toLowerCase() === key.trim().toLowerCase()
  );
  return { ...row, key, hasStoredValue: keepsStoredValue };
}

/** A row after its secret toggle flips: a plain row cannot keep a stored secret. */
export function toggleSecretRow(row: SecretKV, secret: boolean): SecretKV {
  return secret ? { ...row, secret: true } : { ...row, secret: false, hasStoredValue: false };
}

export type ForceSecret = (name: string) => boolean;

const never: ForceSecret = () => false;

/** Whether a row submits to the secret map. */
export function isSecretRow(row: SecretKV, forceSecret: ForceSecret = never): boolean {
  return Boolean(row.secret) || forceSecret(row.key);
}

/** Whether a row shows a stored value it will keep when left blank. */
export function keepsStoredValue(row: SecretKV, forceSecret: ForceSecret = never): boolean {
  return Boolean(row.hasStoredValue) && isSecretRow(row, forceSecret);
}

/**
 * The two maps a row list submits. Blank rows are skipped; a secret row left
 * blank over a stored value submits "***" (keep), and one with nothing
 * stored is dropped.
 */
export function splitSecretRows(
  rows: SecretKV[],
  forceSecret: ForceSecret = never
): { plain: Record<string, string>; secret: Record<string, string> } {
  const plain: Record<string, string> = {};
  const secret: Record<string, string> = {};
  for (const row of rows) {
    const key = row.key.trim();
    if (!key) continue;
    if (isSecretRow(row, forceSecret)) {
      if (row.value) secret[key] = row.value;
      else if (keepsStoredValue(row, forceSecret)) secret[key] = MASKED_SECRET;
    } else if (row.value.trim()) {
      plain[key] = row.value;
    }
  }
  return { plain, secret };
}

const LINE_BREAK = /[\r\n]/;

/**
 * The first problem with a row list, or undefined. `noun` names the rows in
 * messages ("Headers", "Metadata entries", "Secret variables").
 */
export function secretRowsError(
  rows: SecretKV[],
  noun: string,
  forceSecret: ForceSecret = never
): string | undefined {
  const seen = new Set<string>();
  for (const row of rows) {
    const key = row.key.trim();
    const stored = keepsStoredValue(row, forceSecret);
    if (!key && !row.value.trim()) continue;
    if (!key || (!row.value.trim() && !stored)) return `${noun} must have both a name and a value`;
    if (LINE_BREAK.test(row.key) || LINE_BREAK.test(row.value)) return `${noun} cannot contain line breaks`;
    const folded = key.toLowerCase();
    if (seen.has(folded)) return `"${key}" is set more than once`;
    seen.add(folded);
  }
  return undefined;
}
