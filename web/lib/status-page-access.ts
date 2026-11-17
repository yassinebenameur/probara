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
    description:
      'Anyone with the link can view it; search engines are asked not to index it. A hidden link, not access control.',
  },
  {
    value: 'password',
    label: 'Password',
    description: 'Visitors enter a password; kiosk tokens open it too. Changing the password signs visitors out.',
  },
  {
    value: 'private',
    label: 'Private',
    description: 'Only kiosk tokens open it. Anyone else is told the page does not exist.',
  },
];

/** The list label for a page's visibility, or null for public pages. */
export function visibilityLabel(visibility: StatusPageVisibility | undefined): string | null {
  if (!visibility || visibility === 'public') return null;
  return VISIBILITY_OPTIONS.find((o) => o.value === visibility)?.label ?? visibility;
}

/** Password and private pages need a credential (and the session secret). */
export function isGatedVisibility(visibility: StatusPageVisibility | undefined): boolean {
  return visibility === 'password' || visibility === 'private';
}

/** Mirrors shared/pageaccess (bcrypt reads at most 72 bytes). */
export const MIN_PAGE_PASSWORD_LENGTH = 8;
export const MAX_PAGE_PASSWORD_BYTES = 72;

export interface PageAccessDraft {
  visibility: StatusPageVisibility;
  /** New password; '' keeps the stored one. Never prefilled. */
  password: string;
  /** Remove the stored password on save. */
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
 * The URL a kiosk display opens once: the page's public URL with the token.
 * The status page swaps the token for a session cookie and reloads without
 * it. Any operator-only query (the customizer's edit=1) is dropped.
 */
export function kioskUrl(publicUrl: string, token: string, origin: string): string {
  const url = new URL(publicUrl, origin);
  url.searchParams.delete('edit');
  url.searchParams.set('token', token);
  return url.toString();
}
