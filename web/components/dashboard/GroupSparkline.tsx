'use client';

interface Props {
  buckets: number[];   // 0-100 values
}

export default function GroupSparkline({ buckets }: Props) {
  if (buckets.length === 0) {
    return <span className="text-xs text-slate-600">no sparkline data</span>;
  }
  const width = 120;
  const height = 28;
  const min = 95;
  const max = 100;
  const denom = max - min;
  const stepX = buckets.length > 1 ? width / (buckets.length - 1) : width;
  const points = buckets
    .map((v, i) => {
      const clamped = Math.max(min, Math.min(max, v));
      const y = height - ((clamped - min) / denom) * height;
      return `${i * stepX},${y.toFixed(1)}`;
    })
    .join(' ');
  const worst = Math.min(...buckets);
  const stroke = worst >= 99 ? '#34d399' : worst >= 95 ? '#fbbf24' : '#f87171';

  return (
    <svg width={width} height={height} aria-label="Uptime sparkline" role="img">
      <polyline fill="none" stroke={stroke} strokeWidth={1.5} strokeLinejoin="round" points={points} />
    </svg>
  );
}
