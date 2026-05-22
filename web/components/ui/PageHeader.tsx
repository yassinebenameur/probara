import { ReactNode } from 'react';
import Breadcrumb, { BreadcrumbItem } from './Breadcrumb';
import InfoTip, { InfoTipEntry } from './InfoTip';

interface PageHeaderProps {
  breadcrumb?: BreadcrumbItem[];
  title: ReactNode;
  subtitle?: ReactNode;
  titleInfoTip?: ReactNode | { title?: string; entries?: InfoTipEntry[] };
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

export default function PageHeader({
  breadcrumb,
  title,
  subtitle,
  titleInfoTip,
  action,
}: PageHeaderProps) {
  return (
    <div className="mb-6 flex items-start justify-between gap-4">
      <div className="min-w-0">
        {breadcrumb && <Breadcrumb items={breadcrumb} />}
        <div className="flex items-center gap-2">
          <h1 className="truncate text-xl font-semibold text-white">{title}</h1>
          {renderInfoTip(titleInfoTip)}
        </div>
        {subtitle && <p className="mt-1 text-sm text-slate-500">{subtitle}</p>}
      </div>
      {action && <div className="flex-shrink-0">{action}</div>}
    </div>
  );
}
