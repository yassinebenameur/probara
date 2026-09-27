'use client';

interface Props {
  buckets: (number | null)[]; // 0-100 values; null = no checks in that bucket
}

export default function GroupSparkline({ buckets }: Props) {
  const width = 120;
  const height = 28;
  const min = 95;
  const max = 100;
  const denom = max - min;
  const stepX = buckets.length > 1 ? width / (buckets.length - 1) : width;

  const values = buckets.filter((v): v is number => v !== null);
  if (values.length === 0) {
    return <span className="text-xs text-slate-600">no sparkline data</span>;
  }

  // Consecutive non-null runs become separate polylines so no-data buckets
  // render as gaps instead of fabricated values (S-D1).
  const segments: { x: number; y: number }[][] = [];
  let current: { x: number; y: number }[] = [];
  buckets.forEach((v, i) => {
    if (v === null) {
      if (current.length > 0) {
        segments.push(current);
        current = [];
      }
      return;
    }
    const clamped = Math.max(min, Math.min(max, v));
    const y = height - ((clamped - min) / denom) * height;
    current.push({ x: i * stepX, y });
  });
  if (current.length > 0) segments.push(current);

  const worst = Math.min(...values);
  const stroke = worst >= 99 ? '#46d17f' : worst >= 95 ? '#e6b23f' : '#f87171';

  return (
    <svg width={width} height={height} aria-label="Uptime sparkline" role="img">
      {segments.map((segment, i) =>
        segment.length === 1 ? (
          <circle key={i} cx={segment[0].x} cy={segment[0].y} r={1.5} fill={stroke} />
        ) : (
          <polyline
            key={i}
            fill="none"
            stroke={stroke}
            strokeWidth={1.5}
            strokeLinejoin="round"
            points={segment.map((p) => `${p.x},${p.y.toFixed(1)}`).join(' ')}
          />
        )
      )}
    </svg>
  );
}
