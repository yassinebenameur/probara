interface KPICardProps {
  label: string;
  pillLabel?: string;
  mainValue: string | number;
  subValue?: string;
  trend?: {
    value: string;
    label: string;
    positive?: boolean;
  };
  showSparkline?: boolean;
}

export default function KPICard({
  label,
  pillLabel,
  mainValue,
  subValue,
  trend,
  showSparkline = true,
}: KPICardProps) {
  return (
    <div className="relative overflow-hidden rounded-xl border border-white/[0.08] bg-slate-900/[0.92] p-4 shadow-soft">
      {/* Background gradient */}
      <div className="absolute inset-0 bg-[radial-gradient(circle_at_top,rgba(174,182,194,0.12),transparent_55%)] pointer-events-none" />
      <div className="absolute inset-0 bg-[radial-gradient(circle_at_top_right,rgba(111,181,221,0.18),transparent_60%)] opacity-50 pointer-events-none" />
      
      <div className="relative z-10">
        {/* Label */}
        <div className="mb-1.5 flex items-center gap-1.5 text-xs text-muted">
          <span>{label}</span>
          {pillLabel && (
            <span className="rounded-full border border-slate-300/[0.45] bg-slate-900/90 px-1.5 py-0.5 text-[0.66rem] uppercase tracking-wider text-gray-200">
              {pillLabel}
            </span>
          )}
        </div>

        {/* Main Value */}
        <div className="mb-1 flex items-baseline gap-1.5 text-2xl font-semibold">
          {mainValue}
          {subValue && (
            <span className="text-sm font-normal text-muted">{subValue}</span>
          )}
        </div>

        {/* Trend */}
        {trend && (
          <div className="mb-2 flex items-center gap-1.5 text-xs">
            <span
              className={`rounded-full border px-2 py-0.5 text-[0.7rem] ${
                trend.positive !== false
                  ? 'border-[rgba(70,209,127,0.55)] bg-[rgba(22,163,74,0.16)] text-[#bbf7d0]'
                  : 'border-[rgba(239,68,68,0.6)] bg-[rgba(244,112,125,0.14)] text-[#fecaca]'
              }`}
            >
              {trend.value}
            </span>
            <span className="text-muted">{trend.label}</span>
          </div>
        )}

        {/* Sparkline */}
        {showSparkline && (
          <div className="mt-1 h-[26px] overflow-hidden rounded-full bg-[linear-gradient(90deg,rgba(70,209,127,0.22),rgba(70,209,127,0.1),rgba(174,182,194,0.15),rgba(244,112,125,0.25))] relative">
            <div className="absolute inset-0 bg-[linear-gradient(to_right,rgba(16,19,26,0),rgba(16,19,26,0.6))] mix-blend-soft-light" />
          </div>
        )}
      </div>
    </div>
  );
}

