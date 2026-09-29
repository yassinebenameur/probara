'use client';

import { ReactNode, useEffect } from 'react';
import { X } from 'lucide-react';
import ModalPortal from '@/components/ui/ModalPortal';

interface DiscoveryModalProps {
  title: string;
  subtitle?: ReactNode;
  onClose: () => void;
  /** Blocks Escape/backdrop dismissal, e.g. while a request is in flight. */
  locked?: boolean;
  footer?: ReactNode;
  width?: 'md' | 'lg';
  children: ReactNode;
}

// Shared shell for the discovery flows: portaled (see ModalPortal), Escape and
// backdrop close, scrollable body with a fixed footer.
export default function DiscoveryModal({ title, subtitle, onClose, locked = false, footer, width = 'md', children }: DiscoveryModalProps) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape' && !locked) onClose(); };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [locked, onClose]);

  return (
    <ModalPortal>
      <div
        className="fixed inset-0 z-50 flex items-center justify-center bg-slate-950/70 p-4 backdrop-blur-sm"
        onMouseDown={(e) => { if (e.target === e.currentTarget && !locked) onClose(); }}
      >
        <div
          role="dialog"
          aria-modal="true"
          aria-label={title}
          className={`flex max-h-[90vh] w-full ${width === 'lg' ? 'max-w-2xl' : 'max-w-lg'} flex-col rounded-xl border border-white/[0.08] bg-slate-900/95 shadow-2xl`}
        >
          <div className="flex items-start justify-between gap-4 border-b border-white/[0.06] px-6 py-4">
            <div className="min-w-0">
              <h2 className="text-base font-semibold text-white">{title}</h2>
              {subtitle && <div className="mt-1 text-xs text-slate-500">{subtitle}</div>}
            </div>
            <button
              type="button"
              onClick={onClose}
              disabled={locked}
              aria-label="Close"
              className="rounded-md p-1 text-slate-400 transition-colors hover:bg-white/[0.06] hover:text-white disabled:opacity-40"
            >
              <X className="h-4 w-4" aria-hidden="true" />
            </button>
          </div>
          <div className="dashboard-scroll min-h-0 flex-1 overflow-y-auto px-6 py-5">{children}</div>
          {footer && <div className="flex items-center justify-end gap-2 border-t border-white/[0.06] px-6 py-4">{footer}</div>}
        </div>
      </div>
    </ModalPortal>
  );
}
