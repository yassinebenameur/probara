// A monitor's config-as-code key (POST /monitors/apply). The format is the
// API's (api/internal/validation/monitor_key.go); uniqueness is the server's.

export const MONITOR_KEY_MAX_LENGTH = 64;
const MONITOR_KEY_PATTERN = /^[a-z0-9][a-z0-9._-]*$/;

/** The reason a non-empty key is refused, or undefined. Empty means no key. */
export function monitorKeyError(value: string): string | undefined {
  const key = value.trim();
  if (key === '') return undefined;
  if (key.length > MONITOR_KEY_MAX_LENGTH) return `At most ${MONITOR_KEY_MAX_LENGTH} characters.`;
  if (!MONITOR_KEY_PATTERN.test(key)) {
    return "Lowercase letters, digits, '.', '_' or '-', starting with a letter or digit.";
  }
  return undefined;
}

/**
 * The request field a save sends for the key: on create only a key that was
 * entered; on edit only a change, '' removing the key.
 */
export function monitorKeyPatch(original: string | undefined, value: string, isEdit: boolean): { key?: string } {
  const key = value.trim();
  if (!isEdit) return key ? { key } : {};
  return key === (original ?? '') ? {} : { key };
}
