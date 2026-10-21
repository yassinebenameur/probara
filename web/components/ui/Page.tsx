import { ReactNode } from 'react';

export type PageWidth = 'data' | 'detail' | 'wide' | 'reading' | 'form';

/**
 * Page width by page type. The shell has no content cap below 2560px, so
 * every page root picks its own measure here:
 *
 *   data     lists, tables, dashboards, settings: the full content width
 *            (settings columns come from components/settings/layout.ts)
 *   detail   one entity's page (monitor, incident, SLA): ~1600px, centred
 *   wide     wide forms and editors with a side preview: 1152px
 *   reading  single-entity edit pages (channel, user): 1024px
 *   form     single-column forms: 768px
 */
const WIDTH: Record<PageWidth, string> = {
  data: 'w-full',
  detail: 'mx-auto w-full max-w-[100rem]',
  wide: 'mx-auto w-full max-w-6xl',
  reading: 'mx-auto w-full max-w-5xl',
  form: 'mx-auto w-full max-w-3xl',
};

interface PageProps {
  width?: PageWidth;
  className?: string;
  children: ReactNode;
}

/** Page root: vertical rhythm (space-y-6) plus the width for its type. */
export default function Page({ width = 'data', className = '', children }: PageProps) {
  return <div className={`space-y-6 ${WIDTH[width]} ${className}`.trim()}>{children}</div>;
}
