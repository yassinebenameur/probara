'use client';

import { ReactNode } from 'react';
import CollapsibleSection from './CollapsibleSection';
import InfoTip, { InfoTipEntry } from './InfoTip';

interface FormSectionProps {
  title?: string;
  summary?: string;
  infoTip?: ReactNode | { title?: string; entries?: InfoTipEntry[] };
  collapsible?: boolean;
  defaultOpen?: boolean;
  children: ReactNode;
}

function isInfoTipShape(
  value: unknown
): value is { title?: string; entries?: InfoTipEntry[] } {
  if (typeof value !== 'object' || value === null) return false;
  if ('type' in value) return false;
  return 'title' in value || 'entries' in value;
}

function renderInfoTip(infoTip: FormSectionProps['infoTip']) {
  if (!infoTip) return null;
  if (isInfoTipShape(infoTip)) {
    return <InfoTip title={infoTip.title} entries={infoTip.entries} />;
  }
  return <InfoTip>{infoTip as ReactNode}</InfoTip>;
}

export default function FormSection({
  title,
  summary,
  infoTip,
  collapsible = false,
  defaultOpen = true,
  children,
}: FormSectionProps) {
  if (collapsible && title) {
    return (
      <CollapsibleSection title={title} summary={summary} defaultOpen={defaultOpen}>
        <div className="space-y-4">{children}</div>
      </CollapsibleSection>
    );
  }
  return (
    <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-6">
      {title && (
        <div className="mb-4 flex items-center gap-2">
          <h3 className="text-sm font-medium text-white">{title}</h3>
          {renderInfoTip(infoTip)}
        </div>
      )}
      <div className="space-y-4">{children}</div>
    </div>
  );
}
