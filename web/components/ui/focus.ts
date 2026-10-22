/**
 * The one keyboard focus indicator. `ring-focus` is the --focus token
 * (≥3:1 on every surface in both themes); the offset ring is painted in the
 * canvas colour so the ring reads as a separate outline on glass, on wells
 * and on filled controls (an accent button, a checked box).
 *
 * Use FOCUS_RING on anything focusable that sits on a surface, and
 * FOCUS_RING_INSET where an outer ring would be clipped (rows and full-width
 * buttons inside an overflow-hidden panel). Never hand-roll a
 * `ring-cyan-*` focus style.
 */
export const FOCUS_RING =
  'focus:outline-none focus-visible:ring-2 focus-visible:ring-focus focus-visible:ring-offset-2 ' +
  'focus-visible:ring-offset-[var(--bg-base)]';

/** Inset variant: the ring is drawn inside the element's box. */
export const FOCUS_RING_INSET = 'focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-focus';
