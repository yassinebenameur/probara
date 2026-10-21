'use client';

import { useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';

/**
 * Renders overlay content at document.body, and keeps keyboard focus inside
 * it while it is open.
 *
 * A `fixed inset-0` backdrop is only viewport-sized when nothing in its parent
 * chain interferes. Mounted inside page content it inherits that content's
 * spacing — Tailwind's `space-y-*` puts `margin-top` on the overlay, which
 * offsets the backdrop and leaves a strip of the page undimmed — and an
 * ancestor with `overflow: clip` or a transform can clip it outright. Portaling
 * makes an overlay independent of where it is used.
 *
 * Focus: on open it moves into the overlay (unless a child already took it,
 * e.g. with autoFocus), Tab and Shift+Tab wrap inside it, and on close it
 * returns to whatever had it before. `onEscape` closes the overlay from the
 * keyboard; leave it out when the overlay handles Escape itself.
 *
 * While any overlay is open the app shell (#app-root, when the layout sets
 * it) is `inert`, so a screen reader's virtual cursor cannot wander into the
 * page behind the modal. Prefer components/ui/Dialog.tsx for modals: it adds
 * the dialog role, labelling and entrance on top of this.
 */
export default function ModalPortal({
  children,
  onEscape,
}: {
  children: React.ReactNode;
  onEscape?: () => void;
}) {
  // Portals need a DOM node, so wait for the client before rendering.
  const [mounted, setMounted] = useState(false);
  // Where focus was when the overlay was asked for, read before anything in
  // it (an autoFocus button) can take focus: that is where it goes back to.
  const [returnFocusTo] = useState(() =>
    typeof document !== 'undefined' && document.activeElement instanceof HTMLElement ? document.activeElement : null
  );
  useEffect(() => setMounted(true), []);
  if (!mounted) return null;
  return createPortal(
    <FocusScope onEscape={onEscape} returnFocusTo={returnFocusTo}>
      {children}
    </FocusScope>,
    document.body
  );
}

const FOCUSABLE =
  'a[href], button:not([disabled]), input:not([disabled]):not([type="hidden"]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

// Open scopes, innermost last: only the top one traps (a confirm over a modal).
const scopes: HTMLElement[] = [];

// The page behind an open overlay. Overlays portal to <body>, outside it.
function syncBackgroundInert() {
  const root = typeof document !== 'undefined' ? document.getElementById('app-root') : null;
  if (root) root.inert = scopes.length > 0;
}

function FocusScope({
  children,
  onEscape,
  returnFocusTo,
}: {
  children: React.ReactNode;
  onEscape?: () => void;
  returnFocusTo: HTMLElement | null;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const onEscapeRef = useRef(onEscape);
  useEffect(() => {
    onEscapeRef.current = onEscape;
  }, [onEscape]);

  useEffect(() => {
    const scope = ref.current;
    if (!scope) return;
    const previous = returnFocusTo;
    scopes.push(scope);
    syncBackgroundInert();

    const focusables = () =>
      Array.from(scope.querySelectorAll<HTMLElement>(FOCUSABLE)).filter((el) => el.getClientRects().length > 0);
    if (!scope.contains(document.activeElement)) focusables()[0]?.focus();

    const onKeyDown = (event: KeyboardEvent) => {
      if (scopes[scopes.length - 1] !== scope) return;
      if (event.key === 'Escape' && onEscapeRef.current) {
        event.preventDefault();
        onEscapeRef.current();
        return;
      }
      if (event.key !== 'Tab') return;
      const list = focusables();
      if (list.length === 0) {
        event.preventDefault();
        return;
      }
      const first = list[0];
      const last = list[list.length - 1];
      const active = document.activeElement;
      if (!scope.contains(active)) {
        event.preventDefault();
        (event.shiftKey ? last : first).focus();
      } else if (event.shiftKey && active === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && active === last) {
        event.preventDefault();
        first.focus();
      }
    };
    document.addEventListener('keydown', onKeyDown);
    return () => {
      document.removeEventListener('keydown', onKeyDown);
      const index = scopes.indexOf(scope);
      if (index >= 0) scopes.splice(index, 1);
      syncBackgroundInert();
      if (previous?.isConnected) previous.focus({ preventScroll: true });
    };
  }, [returnFocusTo]);

  // display: contents keeps the overlay's own fixed layout untouched.
  return (
    <div ref={ref} className="contents">
      {children}
    </div>
  );
}
