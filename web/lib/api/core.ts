import { clearApiKey, getApiKey } from '../auth';
import { apiErrorFromBody } from '../errors';
import { clearSelectedTenantId, emitTenantChange, getSelectedTenantId, setSelectedTenantId } from '../tenant';
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

async function handleResponse<T>(response: Response, hasApiKey: boolean): Promise<T> {
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
    throw apiErrorFromBody(data);
  }

  return data;
}

const AUTH_PATH_PREFIX = '/v1/auth';

const TENANTS_PATH = '/v1/tenants';

let tenantSelectionValidated = false;

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

  if (!apiKey && !isAuthPath && !isTenantsPath) {
    // Ensure the selected tenant exists before attaching X-Tenant-ID.
    if (!tenantSelectionValidated || !getSelectedTenantId()) {
      await ensureTenantSelected();
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
  if (tenantId) {
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
      const refreshed = await refreshSession();
      if (refreshed) {
        const retryResponse = await fetch(getApiUrl(path), options);
        return await handleResponse<T>(retryResponse, Boolean(apiKey));
      }
    }
    return await handleResponse<T>(response, Boolean(apiKey));
  } catch (error) {
    if (error instanceof Error) {
      throw error;
    }
    throw new Error('Network error');
  }
}

// Refresh rotates the token server-side, so concurrent calls would race and
// all but one lose. Single-flight: every 401 handler (including the SSE
// stream) awaits the same in-flight request.
let refreshInFlight: Promise<boolean> | null = null;

export function refreshSession(): Promise<boolean> {
  if (!refreshInFlight) {
    refreshInFlight = doRefresh().finally(() => {
      refreshInFlight = null;
    });
  }
  return refreshInFlight;
}

async function doRefresh(): Promise<boolean> {
  try {
    const response = await fetch(getApiUrl('/v1/auth/refresh'), {
      method: 'POST',
      credentials: 'include',
    });
    return response.ok;
  } catch {
    return false;
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
  if (!apiKey) {
    await ensureTenantSelected();
  }

  const makeRequest = async (): Promise<Response> => {
    const headers: Record<string, string> = { ...init.headers };

    if (apiKey) {
      headers.Authorization = `Bearer ${apiKey}`;
    } else {
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
    const refreshed = await refreshSession();
    if (refreshed) {
      response = await makeRequest();
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
