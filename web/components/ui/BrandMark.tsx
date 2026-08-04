interface BrandMarkProps {
  /** Rendered edge length in px. Corner radius and trace scale with the tile. */
  size?: number;
  className?: string;
}

/**
 * The Probara mark: a flight-recorder trace on the signal-orange tile.
 *
 * Geometry is identical to web/app/icon.svg, shared/statustemplate/default.gohtml
 * (monochrome variant) and website/app/icon.svg — change all of them together.
 */
export default function BrandMark({ size = 32, className }: BrandMarkProps) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 64 64"
      role="img"
      aria-label="Probara"
      className={className}
    >
      <rect width="64" height="64" rx="15" fill="#ff5a24" />
      <path
        d="M10 34h9l4-11 7 23 7-29 5 17h12"
        fill="none"
        stroke="#140a05"
        strokeWidth="5.5"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}
