'use client';

import { useMemo } from 'react';
import { encodeQr, qrPath } from '@/lib/qr';

/**
 * A QR code drawn in the browser (lib/qr.ts), so the authenticator secret it
 * carries never leaves the page. Dark modules on a white field with a quiet
 * zone, whatever the theme: scanners need the contrast.
 */
export default function QrCode({ value, size = 184, label }: { value: string; size?: number; label: string }) {
  const { path, modules } = useMemo(() => {
    const matrix = encodeQr(value);
    return { path: qrPath(matrix, 4), modules: matrix.length + 8 };
  }, [value]);
  return (
    <svg
      role="img"
      aria-label={label}
      width={size}
      height={size}
      viewBox={`0 0 ${modules} ${modules}`}
      shapeRendering="crispEdges"
      className="rounded-lg"
    >
      <rect width={modules} height={modules} fill="#ffffff" />
      <path d={path} fill="#000000" />
    </svg>
  );
}
