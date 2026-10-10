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
  /**
   * Keep the bar on screen at the bottom of the viewport while the form
   * scrolls. Defaults to on when `middle` is given (the monitor forms' plan
   * summary), so the summary and Save stay reachable on a long form.
   */
  sticky?: boolean;
  /** Optional content rendered between cancel and submit (e.g. status text). */
  middle?: ReactNode;
}

export default function FormActions({ cancel, submit, sticky: stickyProp, middle }: FormActionsProps) {
  const sticky = stickyProp ?? middle !== undefined;
  const container = sticky
    ? 'glass-overlay sticky bottom-3 z-10 mt-6 !rounded-2xl px-4 py-3 sm:bottom-4'
    : 'mt-6';
  return (
    <div
      // globals.css reserves scroll room for the sticky bar from this marker.
      data-sticky-actions={sticky || undefined}
      className={`${container} flex flex-wrap items-center justify-end gap-3`}
    >
      {middle && <div className="w-full text-xs text-slate-500 sm:mr-auto sm:w-auto">{middle}</div>}
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
