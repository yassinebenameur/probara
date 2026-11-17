// Status Page types
export interface StatusPageSectionMonitor {
  monitor_id: string;
  display_name?: string;
  position?: number;
}

export interface StatusPageSection {
  id?: string;
  title: string;
  position?: number;
  monitors?: StatusPageSectionMonitor[];
  created_at?: string;
  updated_at?: string;
}

export interface StatusPageSettings {
  show_monitor_tags?: boolean;
  show_monitor_url?: boolean;
  show_monitor_uptime?: boolean;
  show_monitor_tls?: boolean;
  show_latency_charts?: boolean;
  show_agent_metrics?: boolean;
  show_global_uptime?: boolean;
  show_footer?: boolean;
  footer_text?: string;
  default_theme?: string;
  allow_theme_toggle?: boolean;
  /**
   * Offer visitors a browser-notification opt-in on the public page. Off by
   * default, and inert unless the deployment configures a VAPID keypair
   * (STATUS_PAGE_VAPID_PUBLIC_KEY / _PRIVATE_KEY).
   */
  enable_push_notifications?: boolean;
  custom_css?: string;
  custom_head_html?: string;
  custom_footer_html?: string;
  /**
   * Public page Content-Security-Policy mode. '' (automatic) is compatible
   * while the page has custom CSS, custom head/footer HTML or a published
   * custom template, strict otherwise. Strict runs only scripts carrying
   * nonce="{{cspNonce}}".
   */
  csp_mode?: StatusPageCSPMode;
  /** Overrides the public page's <title> and og:title (up to 120 characters). '' clears it. */
  seo_title?: string;
  /** Overrides the search and link-preview description (up to 300 characters). '' clears it. */
  seo_description?: string;
  /** Let search engines index the page. On by default; off sends noindex. */
  allow_search_indexing?: boolean;
}

export type StatusPageCSPMode = '' | 'strict' | 'compatible' | 'off';

export interface StatusPage {
  id: string;
  tenant_id: string;
  slug: string;
  public_url?: string;
  title: string;
  description?: string;
  logo_url?: string;
  primary_color?: string;
  secondary_color?: string;
  monitor_ids?: string[];
  sections?: StatusPageSection[];
  settings?: StatusPageSettings;
  created_at: string;
  updated_at: string;
  /** Who may view the page; see lib/status-page-access.ts. */
  visibility?: StatusPageVisibility;
  /** A visitor password is stored. The password itself is write-only. */
  has_password?: boolean;
}

export interface StatusPageTemplateVersion {
  version?: number;
  status: 'draft' | 'published' | 'archived';
  size_bytes: number;
  created_at: string;
  updated_at: string;
  published_at?: string;
}

export interface StatusPageTemplateState {
  has_custom: boolean;
  published_version?: number;
  draft_source?: string;
  draft_updated_at?: string;
  versions: StatusPageTemplateVersion[];
  preview_token?: string;
  max_size_bytes: number;
}

export interface StatusPageLibraryTemplate {
  id: string;
  name: string;
  description?: string;
  source?: string; // omitted in list responses
  size_bytes: number;
  created_at: string;
  updated_at: string;
}

export interface StatusPageLibraryTemplateListResponse {
  items: StatusPageLibraryTemplate[];
  total: number;
}

export interface CreateStatusPageRequest {
  slug: string;
  title: string;
  description?: string;
  logo_url?: string;
  primary_color?: string;
  secondary_color?: string;
  monitor_ids?: string[];
  monitor_display_names?: Record<string, string>;
  sections?: StatusPageSection[];
  settings?: StatusPageSettings;
  visibility?: StatusPageVisibility;
  /** Visitor password (required for password visibility). */
  password?: string;
}

export interface UpdateStatusPageRequest {
  slug?: string;
  title?: string;
  description?: string;
  logo_url?: string;
  primary_color?: string;
  secondary_color?: string;
  monitor_ids?: string[];
  monitor_display_names?: Record<string, string>;
  sections?: StatusPageSection[];
  settings?: StatusPageSettings;
  visibility?: StatusPageVisibility;
  /** Write-only: omitted keeps the stored password, '' removes it. */
  password?: string;
}

export interface StatusPageListResponse {
  items: StatusPage[];
  page: number;
  page_size: number;
  total: number;
}

/** Who may view a status page (status_pages.visibility, shared/pageaccess). */
export type StatusPageVisibility = 'public' | 'unlisted' | 'password' | 'private';

/** A kiosk token of a status page; its value is shown once, at creation. */
export interface StatusPageAccessToken {
  id: string;
  label: string;
  created_by_user_id?: string;
  created_by_api_key_id?: string;
  /** Operator username or API key name; empty once the creator is gone. */
  created_by?: string;
  created_at: string;
  revoked_at?: string;
}

/** The one response that carries a kiosk token's value. */
export interface CreatedStatusPageAccessToken extends StatusPageAccessToken {
  token: string;
}

export interface StatusPageAccessTokenListResponse {
  items: StatusPageAccessToken[];
}
