// The password rule as the API applies it (shared/auth.ValidatePassword):
// 12 to 128 characters. Checked here only to answer before a round trip; the
// API stays the authority.
export const MIN_PASSWORD_LENGTH = 12;
export const MAX_PASSWORD_LENGTH = 128;

export const PASSWORD_HINT = `${MIN_PASSWORD_LENGTH} to ${MAX_PASSWORD_LENGTH} characters.`;

/** Why a new password (and its confirmation) cannot be submitted, or ''. */
export function passwordProblem(password: string, confirm: string): string {
  if (!password) return 'Enter a new password.';
  if (password.length < MIN_PASSWORD_LENGTH || password.length > MAX_PASSWORD_LENGTH) {
    return `The password must be ${MIN_PASSWORD_LENGTH} to ${MAX_PASSWORD_LENGTH} characters.`;
  }
  if (password !== confirm) return 'The passwords do not match.';
  return '';
}
