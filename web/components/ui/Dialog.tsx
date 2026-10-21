'use client';

import { ReactNode, useId } from 'react';
import { X } from 'lucide-react';
import ModalPortal from './ModalPortal';
import { FOCUS_RING } from './focus';

export type DialogSize = 'sm' | 'md' | 'lg' | 'xl';

const SIZE: Record<DialogSize, string> = {
  sm: 'max-w-md',
  md: 'max-w-lg',
  lg: 'max-w-2xl',
  xl: 'max-w-4xl',
};

export interface DialogProps {
  /** Rendered only while true (default true, for callers that mount it conditionally). */
  open?: boolean;
  /** Escape, the close button and a backdrop click all call this. */
  onClose: () => void;
  /** The dialog's accessible name, rendered as its h2. */
  title: ReactNode;
  /** One line under the title; becomes aria-describedby. */
  description?: ReactNode;
  /** Shown left of the title (ConfirmDialog's warning glyph). */
  icon?: ReactNode;
  children?: ReactNode;
  /** Right-aligned action row (Cancel, Save). */
  footer?: ReactNode;
  size?: DialogSize;
  /** `danger` makes it an alertdialog (destructive confirmations). */
  tone?: 'default' | 'danger';
  /** While true, Escape, the close button and the backdrop do nothing (a save in flight). */
  busy?: boolean;
  /** Show the × button in the header (default true). */
  showClose?: boolean;
  /** Close on a backdrop click (default true). */
  dismissOnBackdrop?: boolean;
  /** Extra classes on the panel. */
  className?: string;
  /** Extra classes on the body (padding, spacing). Default `space-y-4`. */
  bodyClassName?: string;
}

/**
 * The one modal dialog: ModalPortal (portal to <body>, focus trap, focus
 * return, inert background) plus role=dialog/alertdialog, aria-modal,
 * aria-labelledby/-describedby, the scrim and the dialog-in entrance. The
 * body scrolls when the content is taller than the viewport; header and
 * footer stay put. Autofocus a field in `children` to choose the initial
 * focus, otherwise the first focusable element gets it.
 */
export default function Dialog({
  open = true,
  onClose,
  title,
  description,
  icon,
  children,
  footer,
  size = 'md',
  tone = 'default',
  busy = false,
  showClose = true,
  dismissOnBackdrop = true,
  className = '',
  bodyClassName = 'space-y-4',
}: DialogProps) {
  const titleId = useId();
  const descriptionId = useId();
  if (!open) return null;
  const close = busy ? undefined : onClose;
  const hasBody = children !== undefined && children !== null && children !== false;

  return (
    <ModalPortal onEscape={close ?? noop}>
      <div
        className="glass-scrim fixed inset-0 z-50 flex animate-fade-in items-center justify-center p-4"
        onMouseDown={(event) => {
          if (dismissOnBackdrop && event.target === event.currentTarget) close?.();
        }}
      >
        <div
          role={tone === 'danger' ? 'alertdialog' : 'dialog'}
          aria-modal="true"
          aria-labelledby={titleId}
          aria-describedby={description ? descriptionId : undefined}
          aria-busy={busy || undefined}
          className={`glass-overlay relative flex max-h-[calc(100dvh-2rem)] w-full animate-dialog-in flex-col ${SIZE[size]} ${className}`.trim()}
        >
          <div className={`flex items-start gap-4 pb-1 pl-6 pt-5 ${showClose ? 'pr-14' : 'pr-6'}`}>
            {icon && <div className="mt-0.5 flex-shrink-0">{icon}</div>}
            <div className="min-w-0 flex-1">
              <h2 id={titleId} className="text-base font-semibold text-white">
                {title}
              </h2>
              {description && (
                <div id={descriptionId} className="mt-1.5 text-sm leading-relaxed text-slate-400">
                  {description}
                </div>
              )}
            </div>
          </div>
          {hasBody && (
            <div className={`min-h-0 flex-1 overflow-y-auto px-6 py-4 ${bodyClassName}`.trim()}>{children}</div>
          )}
          {footer ? (
            <div className={`flex flex-wrap items-center justify-end gap-2 px-6 pb-5 ${hasBody ? 'pt-1' : 'pt-5'}`}>
              {footer}
            </div>
          ) : (
            <div className={hasBody ? 'pb-2' : 'pb-5'} />
          )}
          {/* Last in DOM order so initial focus lands on the content, not on
              Close; positioned into the header corner. */}
          {showClose && (
            <button
              type="button"
              onClick={close}
              disabled={busy}
              aria-label="Close"
              title="Close"
              className={`absolute right-4 top-4 inline-flex h-8 w-8 items-center justify-center rounded-lg text-slate-500 transition-colors duration-fast hover:bg-well-hover hover:text-white active:bg-control-hover disabled:opacity-40 ${FOCUS_RING}`}
            >
              <X className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />
            </button>
          )}
        </div>
      </div>
    </ModalPortal>
  );
}

// ModalPortal treats a missing onEscape as "the overlay handles Escape";
// a busy dialog swallows it instead so nothing underneath reacts.
function noop() {}
