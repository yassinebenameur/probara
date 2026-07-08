import { hasApiKey } from '@/lib/auth';

type StatusPageUrlInput = {
  slug: string;
  public_url?: string;
};

const STATUS_PAGE_BASE_URL = process.env.NEXT_PUBLIC_STATUS_PAGE_URL?.trim();

export function resolveStatusPagePublicUrl(input: StatusPageUrlInput): string {
  let resolved: URL;

  if (input.public_url) {
    try {
      resolved = new URL(input.public_url);
    } catch {
      const origin = resolveStatusPageOrigin();
      if (origin) {
        resolved = new URL(input.public_url, origin);
      } else {
        return appendEditQuery(input.public_url);
      }
    }
  } else {
    const origin = resolveStatusPageOrigin();
    const relativePath = `/public/status/${input.slug}`;

    if (!origin) {
      return appendEditQuery(relativePath);
    }

    resolved = new URL(relativePath, origin);
  }

  if (!hasApiKey()) {
    resolved.searchParams.set('edit', '1');
  }

  return resolved.toString();
}

// resolveStatusPageDraftPreviewUrl points at the public renderer's
// draft-template preview route. token comes from the template API state and
// is only required when the deployment sets STATUS_PAGE_PREVIEW_SECRET.
export function resolveStatusPageDraftPreviewUrl(
  input: StatusPageUrlInput,
  token?: string
): string {
  const path = `/public/status/${input.slug}/preview/draft${
    token ? `?token=${encodeURIComponent(token)}` : ''
  }`;
  const origin = resolveStatusPageOrigin();
  if (!origin) {
    return path;
  }
  return new URL(path, origin).toString();
}

function appendEditQuery(url: string): string {
  if (hasApiKey()) {
    return url;
  }

  const separator = url.includes('?') ? '&' : '?';
  return `${url}${separator}edit=1`;
}

function resolveStatusPageOrigin(): string {
  if (STATUS_PAGE_BASE_URL) {
    return STATUS_PAGE_BASE_URL.replace(/\/+$/, '');
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
