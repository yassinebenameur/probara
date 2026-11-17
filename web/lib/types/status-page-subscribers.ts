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
