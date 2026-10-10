import { Children, ReactNode } from 'react';
import Breadcrumb, { BreadcrumbItem } from './Breadcrumb';
import InfoTip, { InfoTipEntry } from './InfoTip';

interface PageHeaderProps {
  breadcrumb?: BreadcrumbItem[];
  title: ReactNode;
  /**
   * One line under the title. Pass an array to render separate facts as
   * spaced segments ("25 monitors", "16 down") instead of a `·` string.
   */
  subtitle?: ReactNode | ReactNode[];
  /** Sits beside the h1, outside it (e.g. a status Pill on a detail page). */
  titleAside?: ReactNode;
  titleInfoTip?: ReactNode | { title?: string; entries?: InfoTipEntry[] };
  /** At most one accent button; the rest ghost. */
  action?: ReactNode;
}

function isInfoTipShape(
  value: unknown
): value is { title?: string; entries?: InfoTipEntry[] } {
  if (typeof value !== 'object' || value === null) return false;
  if ('type' in value) return false;
  return 'title' in value || 'entries' in value;
}

function renderInfoTip(infoTip: PageHeaderProps['titleInfoTip']) {
  if (!infoTip) return null;
  if (isInfoTipShape(infoTip)) {
    return <InfoTip title={infoTip.title} entries={infoTip.entries} />;
  }
  return <InfoTip>{infoTip as ReactNode}</InfoTip>;
}

// The breadcrumb sits on its own line so the actions align with the title
// line on every page, with or without a breadcrumb or subtitle: the button
// no longer jumps between the title and subtitle baselines page to page.
export default function PageHeader({
  breadcrumb,
  title,
  subtitle,
  titleAside,
  titleInfoTip,
  action,
}: PageHeaderProps) {
  return (
    <div className="mb-7">
      {breadcrumb && <Breadcrumb items={breadcrumb} />}
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between sm:gap-6">
        <div className="min-w-0">
          <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
            <h1 className="min-w-0 truncate text-2xl font-semibold tracking-tight text-white">{title}</h1>
            {(titleAside || titleInfoTip) && (
              <div className="flex items-center gap-2">
                {titleAside}
                {renderInfoTip(titleInfoTip)}
              </div>
            )}
          </div>
          {subtitle !== undefined && subtitle !== null && subtitle !== false && (
            Array.isArray(subtitle) ? (
              <div className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-sm text-slate-400">
                {Children.toArray(subtitle.filter((s) => s !== null && s !== undefined && s !== false)).map((segment, i) => (
                  <span key={i} className="tabular-nums">{segment}</span>
                ))}
              </div>
            ) : (
              <div className="mt-1.5 text-sm text-slate-400">{subtitle}</div>
            )
          )}
        </div>
        {action && (
          <div className="flex flex-shrink-0 flex-wrap items-center gap-2 sm:-mt-0.5">{action}</div>
        )}
      </div>
    </div>
  );
}
