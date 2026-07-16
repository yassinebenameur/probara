import { ReactNode } from 'react';
import InfoTip, { InfoTipEntry } from './InfoTip';

interface FormFieldProps {
  label: string;
  description?: string;
  error?: string;
  required?: boolean;
  infoTip?: ReactNode | { title?: string; entries?: InfoTipEntry[] };
  children: ReactNode;
}

function isInfoTipShape(
  value: unknown
): value is { title?: string; entries?: InfoTipEntry[] } {
  if (typeof value !== 'object' || value === null) return false;
  if ('type' in value) return false;
  return 'title' in value || 'entries' in value;
}

function renderInfoTip(infoTip: FormFieldProps['infoTip']) {
  if (!infoTip) return null;
  if (isInfoTipShape(infoTip)) {
    return <InfoTip title={infoTip.title} entries={infoTip.entries} inLabel />;
  }
  return <InfoTip inLabel>{infoTip as ReactNode}</InfoTip>;
}

export default function FormField({
  label,
  description,
  error,
  required,
  infoTip,
  children,
}: FormFieldProps) {
  return (
    <div>
      <div className="mb-1.5 flex items-center gap-1.5">
        <label className="block text-xs font-medium text-slate-400">
          {label}
          {required && <span className="ml-1 text-rose-400">*</span>}
        </label>
        {renderInfoTip(infoTip)}
      </div>
      {description && <p className="mb-2 text-xs text-slate-500">{description}</p>}
      {children}
      {error && <p className="mt-1 text-xs text-rose-400">{error}</p>}
    </div>
  );
}
