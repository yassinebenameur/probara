import type { StatusPageVisibility } from './types';

/**
 * Status page visibility as the operator UI presents it. The server-side
 * contract is shared/pageaccess; these are display strings only.
 */
export const VISIBILITY_OPTIONS: readonly {
  value: StatusPageVisibility;
  label: string;
  description: string;
}[] = [
  { value: 'public', label: 'Public', description: 'Anyone can view the page, and search engines may index it.' },
  {
    value: 'unlisted',
    label: 'Unlisted',
    description: 'Anyone with the link can view it; search engines are told not to index it. Not access control.',
  },
  {
    value: 'password',
    label: 'Password',
    description: 'Visitors enter a password; kiosk tokens also open it. Changing the password signs everyone out.',
  },
  {
    value: 'private',
    label: 'Private',
    description: 'Only kiosk tokens open it. Without one the page answers as if it did not exist.',
  },
];

/** Gated pages need a credential (and the status-page session secret). */
export function isGatedVisibility(visibility: StatusPageVisibility): boolean {
  return visibility === 'password' || visibility === 'private';
}

export const MIN_PAGE_PASSWORD_LENGTH = 8;
const MAX_PAGE_PASSWORD_BYTES = 72;

export interface PageAccessDraft {
  visibility: StatusPageVisibility;
  /** New password; '' keeps the stored one. */
  password: string;
  /** Remove the stored password. */
  clearPassword: boolean;
}

/** Validation message for the access fields, or undefined when they are fine. */
export function pageAccessError(draft: PageAccessDraft, hasStoredPassword: boolean): string | undefined {
  if (draft.password) {
    if ([...draft.password].length < MIN_PAGE_PASSWORD_LENGTH) {
      return `Use at least ${MIN_PAGE_PASSWORD_LENGTH} characters`;
    }
    if (new TextEncoder().encode(draft.password).length > MAX_PAGE_PASSWORD_BYTES) {
      return `Use at most ${MAX_PAGE_PASSWORD_BYTES} bytes`;
    }
  }
  const willHavePassword = draft.password !== '' || (hasStoredPassword && !draft.clearPassword);
  if (draft.visibility === 'password' && !willHavePassword) {
    return 'Set a password for a password-protected page';
  }
  return undefined;
}

/**
 * The access part of a create/update request. The password is write-only:
 * omitted keeps the stored one, '' removes it, a value replaces it.
 */
export function pageAccessPayload(draft: PageAccessDraft): { visibility: StatusPageVisibility; password?: string } {
  if (draft.password) return { visibility: draft.visibility, password: draft.password };
  if (draft.clearPassword) return { visibility: draft.visibility, password: '' };
  return { visibility: draft.visibility };
}

/**
 * The URL a kiosk display opens: the page's public URL with the token. Any
 * operator-only query (the customizer's edit=1) is dropped.
 */
export function kioskUrl(publicUrl: string, token: string, origin: string): string {
  const url = new URL(publicUrl, origin);
  url.searchParams.delete('edit');
  url.searchParams.set('token', token);
  return url.toString();
}
