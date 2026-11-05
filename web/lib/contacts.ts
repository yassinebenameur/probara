// Verified personal contacts (/v1/account/contacts) and the workspace's
// personal SMS channel (/v1/people-settings). Types and the pure helpers the
// Account page uses; the requests live in lib/api.ts.

export type ContactKind = 'email' | 'sms';

export interface UserContact {
  id: string;
  kind: ContactKind;
  address: string;
  label: string;
  verified: boolean;
  verified_at?: string;
  /** Set while a verification code is outstanding. */
  code_expires_at?: string;
  code_sent_at?: string;
  created_at: string;
}

export interface ContactTransports {
  /** SMTP is configured on the installation. */
  email: boolean;
  /** The selected workspace has a personal SMS channel. */
  sms: boolean;
}

export interface ContactListResponse {
  items: UserContact[];
  transports: ContactTransports;
}

export interface CreateContactRequest {
  kind: ContactKind;
  address: string;
  label?: string;
}

export interface PeopleSettings {
  personal_sms_channel_id: string | null;
  sms_channel_options: { id: string; name: string; type: string; is_active: boolean }[];
}

export const CONTACT_KIND_LABELS: Record<ContactKind, string> = {
  email: 'Email',
  sms: 'SMS',
};

export const CONTACT_PLACEHOLDERS: Record<ContactKind, string> = {
  email: 'you@example.com',
  sms: '+15557654321',
};

/** Seconds between two codes (or two tests) to one contact; the API's cooldown. */
export const CODE_RESEND_SECONDS = 60;

/**
 * Client-side check before a create; the API stays the authority. Returns
 * the error to show, or null.
 */
export function contactAddressError(kind: ContactKind, address: string): string | null {
  const value = address.trim();
  if (!value) return 'Enter an address.';
  if (kind === 'email') {
    return /^[^\s@<>]+@[^\s@<>]+\.[^\s@<>]+$/.test(value) ? null : 'Enter a plain email address (you@example.com).';
  }
  const digits = value.replace(/[\s().-]/g, '');
  return /^\+[1-9][0-9]{6,14}$/.test(digits)
    ? null
    : 'Enter the number in international format, starting with + and the country code.';
}

/** Whether the contact's kind can be verified right now. */
export function transportReady(kind: ContactKind, transports: ContactTransports | null): boolean {
  if (!transports) return false;
  return kind === 'email' ? transports.email : transports.sms;
}

/** Why a kind cannot be verified, for the add form and the row. */
export function transportHint(kind: ContactKind): string {
  return kind === 'email'
    ? 'Email is not set up on this installation (SMTP), so email contacts cannot be verified yet.'
    : 'This workspace has no SMS channel for people. A workspace admin chooses one in Settings › Notifications.';
}

/** Seconds left before another code can be sent (0 when it can). */
export function resendWait(contact: Pick<UserContact, 'code_sent_at'>, now: Date = new Date()): number {
  if (!contact.code_sent_at) return 0;
  const sent = new Date(contact.code_sent_at).getTime();
  if (Number.isNaN(sent)) return 0;
  return Math.max(0, Math.ceil(CODE_RESEND_SECONDS - (now.getTime() - sent) / 1000));
}

/** A code is outstanding and not yet expired. */
export function codePending(contact: Pick<UserContact, 'code_expires_at'>, now: Date = new Date()): boolean {
  if (!contact.code_expires_at) return false;
  return new Date(contact.code_expires_at).getTime() > now.getTime();
}
