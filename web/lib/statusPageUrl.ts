import { hasApiKey } from '@/lib/auth';

type StatusPageUrlInput = {
  slug: string;
  public_url?: string;
};

export function resolveStatusPagePublicUrl(input: StatusPageUrlInput): string {
  let resolved: URL;

  if (input.public_url) {
    try {
      resolved = new URL(input.public_url);
    } catch {
      const origin = typeof window !== 'undefined' ? window.location.origin : '';
      if (origin) {
        resolved = new URL(input.public_url, origin);
      } else {
        return appendEditQuery(input.public_url);
      }
    }
  } else {
    const origin = typeof window !== 'undefined' ? window.location.origin : '';
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

function appendEditQuery(url: string): string {
  if (hasApiKey()) {
    return url;
  }

  const separator = url.includes('?') ? '&' : '?';
  return `${url}${separator}edit=1`;
}
