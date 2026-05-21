'use client';

import { ReactNode } from 'react';
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
  sticky?: boolean;
  /** Optional content rendered between cancel and submit (e.g. status text). */
  middle?: ReactNode;
}

export default function FormActions({ cancel, submit, sticky = false, middle }: FormActionsProps) {
  const container = sticky
    ? 'sticky bottom-0 z-10 -mx-6 -mb-6 mt-6 border-t border-white/[0.06] bg-slate-900/95 px-6 py-4 backdrop-blur'
    : 'mt-6';
  return (
    <div className={`${container} flex items-center justify-end gap-3`}>
      {middle && <div className="mr-auto text-xs text-slate-500">{middle}</div>}
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
