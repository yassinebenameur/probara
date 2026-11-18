import { clearApiKey, getApiKey } from '../auth';
import { ApiRequestError, apiErrorFromBody } from '../errors';
import {
  WORKSPACE_NOT_FOUND,
  clearSelectedTenantId,
  emitTenantChange,
  emitTenantsChanged,
  getSelectedTenantId,
  setSelectedTenantId,
} from '../tenant';
import type { TenantListResponse } from '../types';

export { withQuery } from './query';

const API_BASE_URL = process.env.NEXT_PUBLIC_API_URL || '/api';

export function getApiUrl(path: string): string {
  // If API_BASE_URL is relative, use it as-is
  if (API_BASE_URL.startsWith('/')) {
    return `${API_BASE_URL}${path}`;
  }
  // If absolute, ensure path starts with /
  return `${API_BASE_URL}${path.startsWith('/') ? path : `/${path}`}`;
}

/** The headers that authenticate a raw fetch in API-key mode (cookies cover session mode). */
export function apiKeyHeaders(): Record<string, string> {
  const apiKey = getApiKey();
  return apiKey ? { Authorization: `Bearer ${apiKey}` } : {};
}

/**
 * The error for a non-2xx response: the server's JSON `{error, message}` body
 * when it sent one (the same ApiRequestError apiRequest throws), else `fallback`.
 */
export async function errorFromResponse(response: Response, fallback: string): Promise<Error> {
  const contentType = response.headers.get('content-type');
  if (contentType && contentType.includes('application/json')) {
    try {
      return apiErrorFromBody(await response.json());
    } catch {
      // Not the JSON it claimed to be: fall through to the status message.
    }
  }
  return new Error(fallback);
}

// The refresh endpoint answered 429 every time (the address's failed-sign-in
// budget is spent, typically someone else behind the same NAT). That is not
// a lost session: keep it, fail this call, and the next call tries again.
function refreshRateLimitedError(): ApiRequestError {
  return new ApiRequestError(
    'rate_limited',
    'Too many requests from this network right now. Your session is kept; retry in a moment.'
  );
}

// A 401 that survived the refresh attempt: send the user back to sign in
// (or to re-enter the key, which is dropped) and fail the call.
function handleUnauthorized(hasApiKey: boolean): never {
  if (hasApiKey) {
    clearApiKey();
    if (typeof window !== 'undefined') {
      window.location.href = '/connect';
    }
  } else if (typeof window !== 'undefined') {
    window.location.href = '/login';
  }
  throw new Error('Unauthorized');
}

async function handleResponse<T>(response: Response, hasApiKey: boolean, sentTenantId: string | null): Promise<T> {
  if (response.status === 401) {
    handleUnauthorized(hasApiKey);
  }

  const contentType = response.headers.get('content-type');
  if (!contentType || !contentType.includes('application/json')) {
    if (!response.ok) {
      throw new Error(`HTTP error! status: ${response.status}`);
    }
    return {} as T;
  }

  const data = await response.json();

  if (!response.ok) {
    const error = apiErrorFromBody(data);
    if (response.status === 404 && error.error === WORKSPACE_NOT_FOUND && sentTenantId) {
      workspaceGone(sentTenantId);
    }
    throw error;
  }

  return data;
}

// The selected workspace is gone (deleted by someone else or in another tab,
// or the member lost access): drop the selection, so no later request sends
// it again, and have the switcher reload its list, which settles once on a
// workspace that still exists and remounts the page on it. Idempotent: only
// the selection the failing request carried is dropped, so the other
// in-flight requests that fail the same way (even one answered after the
// switcher has settled on another workspace) change and announce nothing,
// and one deletion reloads the list once.
function workspaceGone(failedTenantId: string): void {
  if (getSelectedTenantId() !== failedTenantId) return;
  tenantSelectionValidated = false;
  clearSelectedTenantId();
  emitTenantsChanged();
}

// A signed-in member who belongs to no workspace (theirs was deleted, or none
// was ever granted) has nothing to scope a request to. Answer here instead of
// sending it: the API would refuse it as unauthenticated and bounce them back
// to the sign-in page they just left.
function noWorkspaceError(): ApiRequestError {
  return new ApiRequestError(
    'no_workspace',
    'You are not a member of any workspace. Ask a platform administrator to add you to one.'
  );
}

const AUTH_PATH_PREFIX = '/v1/auth';

// Workspace list and management routes are platform-scoped: they never carry
// X-Tenant-ID, so a stale selection cannot make the very request that
// recovers from it fail with workspace_not_found.
const TENANTS_PATH = '/v1/tenants';

// Every platform-scoped route the UI calls (not in a workspace): the
// workspace list, the signed-in user's own account and MCP connections,
// user and SSO-mapping management, the OpenAPI document. They never carry
// X-Tenant-ID and are never answered with the no-workspace error, so a member
// who belongs to no workspace can still manage their own sessions.
const PLATFORM_PATHS = [
  TENANTS_PATH,
  '/v1/account',
  '/v1/oauth/grants',
  '/v1/users',
  '/v1/oidc-group-mappings',
  '/v1/openapi.json',
];

/** Whether path (with any query) is one of PLATFORM_PATHS or below it. */
export function isPlatformPath(path: string): boolean {
  const bare = path.split('?')[0];
  return PLATFORM_PATHS.some((prefix) => bare === prefix || bare.startsWith(`${prefix}/`));
}

let tenantSelectionValidated = false;
// The last tenant list loaded for this session was empty.
let noWorkspaces = false;

async function ensureTenantSelected(): Promise<void> {
  try {
    const response = await fetch(getApiUrl(TENANTS_PATH), {
      method: 'GET',
      credentials: 'include',
    });
    if (!response.ok) {
      return;
    }

    const data = (await response.json()) as TenantListResponse;
    const storedTenantId = getSelectedTenantId();
    const tenantIDs = new Set((data.items || []).map((tenant) => tenant.id));
    noWorkspaces = tenantIDs.size === 0;

    if (storedTenantId && tenantIDs.has(storedTenantId)) {
      tenantSelectionValidated = true;
      return;
    }

    const firstTenantId = data.items?.[0]?.id;
    if (firstTenantId) {
      setSelectedTenantId(firstTenantId);
      emitTenantChange();
    } else if (storedTenantId) {
      // Stored tenant became invalid (or tenants are empty): avoid sending stale X-Tenant-ID.
      clearSelectedTenantId();
    }
    tenantSelectionValidated = true;
  } catch {
    // Ignore tenant selection failures; caller will handle 401s.
  }
}

export async function apiRequest<T>(
  method: string,
  path: string,
  body?: unknown
): Promise<T> {
  const apiKey = getApiKey();
  const headers: HeadersInit = {
    'Content-Type': 'application/json',
  };

  if (apiKey) {
    headers.Authorization = `Bearer ${apiKey}`;
  }

  const isAuthPath = path.startsWith(AUTH_PATH_PREFIX);
  const isTenantsPath = path.startsWith(TENANTS_PATH);
  const isPlatform = isPlatformPath(path);

  if (!apiKey && !isAuthPath && !isPlatform) {
    // Ensure the selected tenant exists before attaching X-Tenant-ID.
    if (!tenantSelectionValidated || !getSelectedTenantId()) {
      await ensureTenantSelected();
    }
    if (noWorkspaces && !getSelectedTenantId()) {
      throw noWorkspaceError();
    }
  }

  if (!apiKey && isAuthPath) {
    // Auth transitions can invalidate cached tenant context.
    tenantSelectionValidated = false;
  }

  if (!apiKey && isTenantsPath) {
    // Tenant list may have changed since last validation.
    tenantSelectionValidated = false;
  }

  const tenantId = getSelectedTenantId();
  // The workspace this request is scoped to, if any (API keys are pinned).
  const sentTenantId = tenantId && !isPlatform && !apiKey ? tenantId : null;
  if (tenantId && !isPlatform) {
    headers['X-Tenant-ID'] = tenantId;
  }

  const options: RequestInit = {
    method,
    headers,
    credentials: 'include',
  };

  if (body) {
    options.body = JSON.stringify(body);
  }

  try {
    const response = await fetch(getApiUrl(path), options);
    if (response.status === 401 && !apiKey) {
      tenantSelectionValidated = false;
      const outcome = await refreshSessionOutcome();
      if (outcome === 'refreshed') {
        const retryResponse = await fetch(getApiUrl(path), options);
        return await handleResponse<T>(retryResponse, Boolean(apiKey), sentTenantId);
      }
      if (outcome === 'rate_limited') {
        throw refreshRateLimitedError();
      }
    }
    return await handleResponse<T>(response, Boolean(apiKey), sentTenantId);
  } catch (error) {
    if (error instanceof Error) {
      throw error;
    }
    throw new Error('Network error');
  }
}

// How a refresh ended: a new access token; the session is over (sign in
// again); or the API kept answering 429, which says nothing about the
// session.
export type RefreshOutcome = 'refreshed' | 'signed_out' | 'rate_limited';

// Refresh rotates the token server-side, so concurrent calls would race and
// all but one lose. Single-flight: every 401 handler (including the SSE
// stream) awaits the same in-flight request.
let refreshInFlight: Promise<RefreshOutcome> | null = null;

export function refreshSessionOutcome(): Promise<RefreshOutcome> {
  if (!refreshInFlight) {
    refreshInFlight = doRefresh().finally(() => {
      refreshInFlight = null;
    });
  }
  return refreshInFlight;
}

/** True when the session was refreshed; false otherwise (including when rate limited). */
export async function refreshSession(): Promise<boolean> {
  return (await refreshSessionOutcome()) === 'refreshed';
}

// A 429 on refresh is retried a few times, waiting what Retry-After asks
// (bounded), before the call gives up for now.
const REFRESH_RATE_LIMIT_RETRIES = 3;
const REFRESH_MAX_WAIT_MS = 10_000;

function retryAfterMs(response: Response, attempt: number): number {
  const seconds = Number(response.headers.get('retry-after'));
  const wait = Number.isFinite(seconds) && seconds >= 0 ? seconds * 1000 : 1000 * 2 ** attempt;
  return Math.min(wait, REFRESH_MAX_WAIT_MS);
}

async function doRefresh(): Promise<RefreshOutcome> {
  for (let attempt = 0; ; attempt++) {
    let response: Response;
    try {
      response = await fetch(getApiUrl('/v1/auth/refresh'), {
        method: 'POST',
        credentials: 'include',
      });
    } catch {
      return 'signed_out';
    }
    if (response.ok) return 'refreshed';
    if (response.status !== 429) return 'signed_out';
    if (attempt >= REFRESH_RATE_LIMIT_RETRIES) return 'rate_limited';
    const wait = retryAfterMs(response, attempt);
    await new Promise((resolve) => setTimeout(resolve, wait));
  }
}

// Plain-text downloads (the template sources are not JSON).
export async function apiRequestText(path: string): Promise<string> {
  const headers = apiKeyHeaders();
  const tenantId = getSelectedTenantId();
  if (tenantId) {
    headers['X-Tenant-ID'] = tenantId;
  }
  const response = await fetch(getApiUrl(path), {
    method: 'GET',
    headers,
    credentials: 'include',
  });
  if (!response.ok) {
    throw await errorFromResponse(response, `HTTP error! status: ${response.status}`);
  }
  return response.text();
}

// A non-JSON exchange (file download, multipart upload) with the same auth,
// tenant and refresh handling as apiRequest. Returns the raw response once a
// 401 has been ruled out; the caller reads the body and reports other errors.
export async function apiFetch(
  path: string,
  init: { method?: string; headers?: Record<string, string>; body?: BodyInit } = {}
): Promise<Response> {
  const apiKey = getApiKey();
  const isPlatform = isPlatformPath(path);
  if (!apiKey && !isPlatform) {
    await ensureTenantSelected();
    if (noWorkspaces && !getSelectedTenantId()) {
      throw noWorkspaceError();
    }
  }

  const makeRequest = async (): Promise<Response> => {
    const headers: Record<string, string> = { ...init.headers };

    if (apiKey) {
      headers.Authorization = `Bearer ${apiKey}`;
    } else if (!isPlatform) {
      const tenantId = getSelectedTenantId();
      if (tenantId) {
        headers['X-Tenant-ID'] = tenantId;
      }
    }

    return fetch(getApiUrl(path), {
      method: init.method ?? 'GET',
      headers,
      credentials: 'include',
      body: init.body,
    });
  };

  let response = await makeRequest();
  if (response.status === 401 && !apiKey) {
    const outcome = await refreshSessionOutcome();
    if (outcome === 'refreshed') {
      response = await makeRequest();
    } else if (outcome === 'rate_limited') {
      throw refreshRateLimitedError();
    }
  }

  if (response.status === 401) {
    handleUnauthorized(Boolean(apiKey));
  }

  return response;
}

// Fetches a file attachment (GET by default; POST with a JSON body for exports
// that take filters) and returns it with the server's Content-Disposition
// filename.
export async function apiDownload(
  path: string,
  fallbackFilename: string,
  init: { method?: 'GET' | 'POST'; body?: unknown } = {}
): Promise<{ blob: Blob; filename: string; headers: Headers }> {
  const hasBody = init.body !== undefined;
  const response = await apiFetch(path, {
    method: init.method,
    headers: hasBody ? { 'Content-Type': 'application/json' } : undefined,
    body: hasBody ? JSON.stringify(init.body) : undefined,
  });

  if (!response.ok) {
    throw await errorFromResponse(response, `Download failed with status ${response.status}`);
  }

  const contentDisposition = response.headers.get('content-disposition') || '';
  const filenameMatch = contentDisposition.match(/filename=\"?([^\";]+)\"?/i);

  return {
    blob: await response.blob(),
    filename: filenameMatch?.[1] || fallbackFilename,
    headers: response.headers,
  };
}
