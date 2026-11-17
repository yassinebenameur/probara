/**
 * Public add-on URLs of a status page (Atom feed, summary.json, SVG badges)
 * and the copyable badge snippets. The routes are served by the status-page
 * service beside the page itself; see "Feed, summary and badges" in the
 * status-pages docs.
 */

export type BadgeMetric = 'status' | 'uptime';

/** Ranges an uptime badge accepts; the server defaults to 30d. */
export const BADGE_UPTIME_RANGES = ['1h', '24h', '7d', '30d', '90d'] as const;
export type BadgeUptimeRange = (typeof BADGE_UPTIME_RANGES)[number];

/** The page URL without query, fragment or trailing slash. */
export function statusPageBase(pageUrl: string): string {
  return pageUrl.split('#')[0].split('?')[0].replace(/\/+$/, '');
}

export interface StatusPageShareLinks {
  page: string;
  feed: string;
  summary: string;
  badge: string;
}

export function statusPageShareLinks(pageUrl: string): StatusPageShareLinks {
  const page = statusPageBase(pageUrl);
  return {
    page,
    feed: `${page}/feed.atom`,
    summary: `${page}/summary.json`,
    badge: `${page}/badge.svg`,
  };
}

/** A component badge: its status, or its uptime over `range`. */
export function monitorBadgeUrl(
  pageUrl: string,
  monitorId: string,
  metric: BadgeMetric,
  range: BadgeUptimeRange = '30d'
): string {
  const base = `${statusPageBase(pageUrl)}/monitors/${encodeURIComponent(monitorId)}`;
  return metric === 'uptime' ? `${base}/uptime.svg?range=${range}` : `${base}/badge.svg`;
}

// Parentheses end a Markdown link target early; spaces end it outright.
function markdownUrl(url: string): string {
  return url.replace(/\(/g, '%28').replace(/\)/g, '%29').replace(/ /g, '%20');
}

function markdownText(text: string): string {
  return text.replace(/[\\[\]]/g, (c) => `\\${c}`);
}

function htmlAttr(text: string): string {
  return text
    .replace(/&/g, '&amp;')
    .replace(/"/g, '&quot;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;');
}

/** A Markdown badge image that links to the page. */
export function badgeMarkdown(imageUrl: string, linkUrl: string, alt: string): string {
  return `[![${markdownText(alt)}](${markdownUrl(imageUrl)})](${markdownUrl(linkUrl)})`;
}

/** An HTML badge image that links to the page. */
export function badgeHtml(imageUrl: string, linkUrl: string, alt: string): string {
  return `<a href="${htmlAttr(linkUrl)}"><img src="${htmlAttr(imageUrl)}" alt="${htmlAttr(alt)}"></a>`;
}
