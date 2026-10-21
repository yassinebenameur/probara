'use client';

import Button from './Button';

interface ActionConfig {
  label: string;
  onClick?: () => void;
  href?: string;
  disabled?: boolean;
  loading?: boolean;
  type?: 'button' | 'submit';
}

interface FormActionsProps {
  cancel?: ActionConfig;
  submit: ActionConfig;
}

/**
 * Cancel + submit at the foot of a short form. A long form whose buttons must
 * stay in reach uses a sticky bar instead: the monitor forms'
 * FormActionBar (components/monitors/form-kit.tsx).
 */
export default function FormActions({ cancel, submit }: FormActionsProps) {
  return (
    <div className="mt-6 flex flex-wrap items-center justify-end gap-3">
      {cancel && (
        <Button
          variant="ghost"
          size="sm"
          type={cancel.type ?? 'button'}
          onClick={cancel.onClick}
          disabled={cancel.disabled}
        >
          {cancel.label}
        </Button>
      )}
      <Button
        variant="accent"
        size="sm"
        type={submit.type ?? 'submit'}
        onClick={submit.onClick}
        disabled={submit.disabled}
        loading={submit.loading}
      >
        {submit.label}
      </Button>
    </div>
  );
}
