'use client';

import { useMemo } from 'react';
import { encodeQr, qrPath } from '@/lib/qr';

/**
 * A QR code drawn in the browser (lib/qr.ts), so the encoded value (a TOTP
 * secret) never goes to a QR service. Always dark-on-white with a quiet
 * zone: scanners need the contrast whatever the theme.
 */
export default function QrCode({ value, label, size = 192 }: { value: string; label: string; size?: number }) {
  const { path, dim } = useMemo(() => {
    const matrix = encodeQr(value);
    return { path: qrPath(matrix, 4), dim: matrix.length + 8 };
  }, [value]);
  return (
    <svg
      role="img"
      aria-label={label}
      width={size}
      height={size}
      viewBox={`0 0 ${dim} ${dim}`}
      shapeRendering="crispEdges"
      className="rounded-md"
    >
      <rect width={dim} height={dim} fill="#ffffff" />
      <path d={path} fill="#000000" />
    </svg>
  );
}
