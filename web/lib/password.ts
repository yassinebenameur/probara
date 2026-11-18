// The password rule as the API applies it (shared/auth.ValidatePassword): at
// least 12 characters and at most 72 bytes of UTF-8, the most bcrypt can
// hash (an accented or non-Latin letter takes two to four bytes). Checked
// here only to answer before a round trip; the API stays the authority.
export const MIN_PASSWORD_LENGTH = 12;
export const MAX_PASSWORD_BYTES = 72;

export const PASSWORD_HINT = `At least ${MIN_PASSWORD_LENGTH} characters, at most ${MAX_PASSWORD_BYTES} bytes (fewer characters with accented or non-Latin letters).`;

/** Characters as the API counts them (code points, not UTF-16 units). */
function characterCount(value: string): number {
  return Array.from(value).length;
}

/** UTF-8 bytes, which is what bcrypt limits. */
export function passwordBytes(value: string): number {
  return new TextEncoder().encode(value).length;
}

/** Why a new password (and its confirmation) cannot be submitted, or ''. */
export function passwordProblem(password: string, confirm: string): string {
  if (!password) return 'Enter a new password.';
  if (characterCount(password) < MIN_PASSWORD_LENGTH) {
    return `The password must be at least ${MIN_PASSWORD_LENGTH} characters.`;
  }
  if (passwordBytes(password) > MAX_PASSWORD_BYTES) {
    return `The password is too long: at most ${MAX_PASSWORD_BYTES} bytes, which is fewer characters when it uses accented or non-Latin letters.`;
  }
  if (password !== confirm) return 'The passwords do not match.';
  return '';
}
