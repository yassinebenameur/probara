import {
  Children,
  cloneElement,
  isValidElement,
  ReactElement,
  ReactNode,
  useId,
} from 'react';
import InfoTip, { InfoTipEntry } from './InfoTip';
import Input from './Input';
import Select from './Select';

interface FormFieldProps {
  label: string;
  description?: string;
  error?: string;
  required?: boolean;
  infoTip?: ReactNode | { title?: string; entries?: InfoTipEntry[] };
  /** Id of the control the label names; generated when omitted. */
  htmlFor?: string;
  /**
   * The control. A single <input>/<select>/<textarea> (or ui Input/Select)
   * child gets the label's id, aria-invalid and aria-describedby
   * automatically; for anything else pass a function and spread the props.
   */
  children: ReactNode | ((control: FieldControlProps) => ReactNode);
}

export interface FieldControlProps {
  id: string;
  'aria-invalid'?: true;
  'aria-describedby'?: string;
  /** Set for required fields: the visual asterisk is aria-hidden. */
  'aria-required'?: true;
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

const LINKABLE = new Set<unknown>(['input', 'select', 'textarea', Input, Select]);

export default function FormField({
  label,
  description,
  error,
  required,
  infoTip,
  htmlFor,
  children,
}: FormFieldProps) {
  const autoId = useId();
  const descriptionId = `${autoId}-description`;
  const errorId = `${autoId}-error`;
  const describedBy = [description && descriptionId, error && errorId].filter(Boolean).join(' ') || undefined;

  let controlId = htmlFor ?? autoId;
  let content: ReactNode;
  let linked = Boolean(htmlFor);

  if (typeof children === 'function') {
    content = children({
      id: controlId,
      'aria-invalid': error ? true : undefined,
      'aria-describedby': describedBy,
      'aria-required': required ? true : undefined,
    });
    linked = true;
  } else {
    const only = Children.count(children) === 1 ? Children.toArray(children)[0] : null;
    if (!htmlFor && isValidElement(only) && LINKABLE.has(only.type)) {
      const el = only as ReactElement<Record<string, unknown>>;
      controlId = (el.props.id as string | undefined) ?? autoId;
      content = cloneElement(el, {
        id: controlId,
        'aria-invalid': (el.props['aria-invalid'] as boolean | undefined) ?? (error ? true : undefined),
        'aria-describedby': (el.props['aria-describedby'] as string | undefined) ?? describedBy,
        'aria-required':
          (el.props['aria-required'] as boolean | undefined) ??
          (el.props.required as boolean | undefined) ??
          (required ? true : undefined),
      });
      linked = true;
    } else {
      content = children;
    }
  }

  return (
    <div>
      <div className="mb-1.5 flex items-center gap-1.5">
        <label htmlFor={linked ? controlId : undefined} className="block text-ui font-medium text-slate-300">
          {label}
          {required && <span className="ml-1 text-rose-300" aria-hidden="true">*</span>}
        </label>
        {renderInfoTip(infoTip)}
      </div>
      {description && <p id={descriptionId} className="mb-2 text-xs text-slate-500">{description}</p>}
      {content}
      {error && <p id={errorId} className="mt-1 text-xs text-rose-300">{error}</p>}
    </div>
  );
}
