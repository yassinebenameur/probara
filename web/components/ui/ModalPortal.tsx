'use client';

import { useEffect, useState } from 'react';
import { createPortal } from 'react-dom';

/**
 * Renders overlay content at document.body.
 *
 * A `fixed inset-0` backdrop is only viewport-sized when nothing in its parent
 * chain interferes. Mounted inside page content it inherits that content's
 * spacing — Tailwind's `space-y-*` puts `margin-top` on the overlay, which
 * offsets the backdrop and leaves a strip of the page undimmed — and an
 * ancestor with `overflow: clip` or a transform can clip it outright. Portaling
 * makes an overlay independent of where it is used.
 */
export default function ModalPortal({ children }: { children: React.ReactNode }) {
  // Portals need a DOM node, so wait for the client before rendering.
  const [mounted, setMounted] = useState(false);
  useEffect(() => setMounted(true), []);
  if (!mounted) return null;
  return createPortal(children, document.body);
}
