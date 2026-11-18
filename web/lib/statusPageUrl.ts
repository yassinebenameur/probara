type StatusPageUrlInput = {
  slug: string;
  public_url?: string;
};

// Build-time fallback for development. NEXT_PUBLIC_* values are frozen into
// the bundle when it is built, so the published image carries none; a
// deployment's status-page origin reaches the UI at runtime instead, as
// status_page_base_url from GET /v1/auth-context (useStatusPageBaseUrl).
const BUILD_TIME_STATUS_PAGE_URL = process.env.NEXT_PUBLIC_STATUS_PAGE_URL?.trim();

/**
 * Public URL of a status page. runtimeBaseUrl is the deployment's
 * STATUS_PAGE_BASE_URL as the API reports it; see resolveStatusPageOrigin.
 */
export function resolveStatusPagePublicUrl(input: StatusPageUrlInput, runtimeBaseUrl?: string | null): string {
  let resolved: URL;

  if (input.public_url) {
    try {
      resolved = new URL(input.public_url);
    } catch {
      const origin = resolveStatusPageOrigin(runtimeBaseUrl);
      if (origin) {
        resolved = new URL(input.public_url, origin);
      } else {
        return input.public_url;
      }
    }
  } else {
    const origin = resolveStatusPageOrigin(runtimeBaseUrl);
    const relativePath = `/public/status/${input.slug}`;

    if (!origin) {
      return relativePath;
    }

    resolved = new URL(relativePath, origin);
  }

  return resolved.toString();
}

// resolveStatusPageDraftPreviewUrl points at the public renderer's
// draft-template preview route. token comes from the template API state and
// is only required when the deployment sets STATUS_PAGE_PREVIEW_SECRET.
export function resolveStatusPageDraftPreviewUrl(
  input: StatusPageUrlInput,
  token?: string,
  runtimeBaseUrl?: string | null
): string {
  const path = `/public/status/${input.slug}/preview/draft${
    token ? `?token=${encodeURIComponent(token)}` : ''
  }`;
  const origin = resolveStatusPageOrigin(runtimeBaseUrl);
  if (!origin) {
    return path;
  }
  return new URL(path, origin).toString();
}

/**
 * Where status pages live, first match wins: the runtime value from the API,
 * the build-time NEXT_PUBLIC_STATUS_PAGE_URL, the dev convention of :8082 on
 * a localhost UI, else the UI's own origin. A configured value that is not an
 * absolute http(s) URL is ignored rather than producing broken links.
 * lib/csp.ts (statusPageFrameOrigins) mirrors this for the preview iframe.
 */
export function resolveStatusPageOrigin(runtimeBaseUrl?: string | null): string {
  for (const configured of [runtimeBaseUrl, BUILD_TIME_STATUS_PAGE_URL]) {
    const base = absoluteBase(configured);
    if (base) {
      return base;
    }
  }

  if (typeof window === 'undefined') {
    return '';
  }

  const { protocol, hostname, port, origin } = window.location;
  const isLocalHost = hostname === 'localhost' || hostname === '127.0.0.1';

  if (isLocalHost && port !== '8082') {
    return `${protocol}//${hostname}:8082`;
  }

  return origin;
}

function absoluteBase(value: string | null | undefined): string {
  const trimmed = value?.trim().replace(/\/+$/, '');
  if (!trimmed) {
    return '';
  }
  try {
    const parsed = new URL(trimmed);
    return parsed.protocol === 'http:' || parsed.protocol === 'https:' ? trimmed : '';
  } catch {
    return '';
  }
}
