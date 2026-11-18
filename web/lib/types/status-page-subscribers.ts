// Status page email subscribers (API: /status-pages/{id}/subscribers).

/** pending until the address follows its confirmation link; only confirmed ones are mailed. */
export type StatusPageSubscriberState = 'pending' | 'confirmed';

export interface StatusPageSubscriber {
  id: string;
  email: string;
  state: StatusPageSubscriberState;
  created_at: string;
  requested_at: string;
  confirmed_at?: string;
}

export interface StatusPageSubscriberListResponse {
  items: StatusPageSubscriber[];
  page: number;
  page_size: number;
  total: number;
  counts: { confirmed: number; pending: number };
  /** Most subscribers (pending and confirmed) a page may have. */
  limit: number;
  /** False when the API cannot mail subscribers; mail_problems says why. */
  mail_ready: boolean;
  mail_problems: string[];
}

/** What a page's subscribers look like to any member (no addresses). */
export interface StatusPageSubscriberSummary {
  counts: { confirmed: number; pending: number };
  limit: number;
  mail_ready: boolean;
  mail_problems: string[];
  /** The page's latest notices, newest first. */
  recent: StatusPageNotificationOutcome[];
}

/** One notice and what its deliveries came to. */
export interface StatusPageNotificationOutcome {
  id: string;
  kind: string;
  created_at: string;
  subject?: string;
  status: 'pending' | 'planned' | 'skipped';
  skip_reason?: string;
  recipients: number;
  sent: number;
  waiting: number;
  failed: number;
  uncertain: number;
  skipped: number;
  last_error?: string;
}

export interface RemoveStatusPageSubscribersRequest {
  all?: boolean;
  domain?: string;
}
