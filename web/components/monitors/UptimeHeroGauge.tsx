'use client';

import { useState, useEffect } from 'react';

// TODO: make SLA_TARGET configurable per-monitor from settings
export const SLA_TARGET = 99.9;

interface UptimeHeroGaugeProps {
  uptime: number;
  hasData: boolean;
}

export function UptimeHeroGauge({ uptime, hasData }: UptimeHeroGaugeProps) {
  const [mounted, setMounted] = useState(false);
  useEffect(() => {
    const id = requestAnimationFrame(() => setMounted(true));
    return () => cancelAnimationFrame(id);
  }, []);

  const isCompliant = uptime >= SLA_TARGET;

  // Arc geometry: gap of ~100deg at the bottom, arc sweeps 260deg
  // SIZE=380, R=148 → inner clear diameter ≈278px; font budget fits "100.000%"
  const SIZE = 380;
  const cx = SIZE / 2;
  const cy = SIZE / 2;
  const R = 148;
  const GAP_DEG = 100;
  const ARC_DEG = 360 - GAP_DEG;
  const circumference = 2 * Math.PI * R;
  const arcLength = (ARC_DEG / 360) * circumference;
  const gapLength = circumference - arcLength;

  const fillRatio = hasData ? Math.max(0, Math.min(1, uptime / 100)) : 0;
  const fillLength = fillRatio * arcLength;

  // SVG 0deg = 3-o'clock; gap centred at 6-o'clock → start at 140deg
  const rotationDeg = 90 + GAP_DEG / 2;

  const accentColor = isCompliant ? '#06b6d4' : '#f43f5e';
  const glowClass = isCompliant ? 'uptime-gauge-outer' : 'uptime-gauge-outer--breach';

  // Split uptime value into integer and decimal parts for differential sizing
  const uptimeStr = hasData ? uptime.toFixed(3) : '—';
  const dotIdx = uptimeStr.indexOf('.');
  const intPart = dotIdx >= 0 ? uptimeStr.slice(0, dotIdx) : uptimeStr;
  const decPart = dotIdx >= 0 ? uptimeStr.slice(dotIdx) : '';

  const slaLabel = isCompliant ? 'SLA COMPLIANT' : 'SLA BREACH';
  const slaBorderColor = isCompliant
    ? 'border-cyan-400/40 text-cyan-300 bg-slate-900/70'
    : 'border-rose-400/40 text-rose-300 bg-slate-900/70';
  const slaDotColor = isCompliant ? 'bg-cyan-400' : 'bg-rose-400';

  return (
    <div className="flex flex-col items-center select-none py-6">
      {/* SLA compliance badge */}
      <div
        className={`mb-6 inline-flex items-center gap-2 rounded-full border px-4 py-1.5 text-[11px] font-semibold uppercase tracking-[0.18em] backdrop-blur-sm ${slaBorderColor}`}
      >
        <span className={`h-1.5 w-1.5 rounded-full animate-pulse-soft ${slaDotColor}`} />
        {slaLabel}
      </div>

      {/* Gauge SVG */}
      <div className="relative" style={{ width: SIZE, height: SIZE }}>
        <svg
          width={SIZE}
          height={SIZE}
          viewBox={`0 0 ${SIZE} ${SIZE}`}
          className="overflow-visible"
        >
          <defs>
            <filter id="gauge-glow" x="-30%" y="-30%" width="160%" height="160%">
              <feGaussianBlur stdDeviation="8" result="blur" />
              <feComposite in="SourceGraphic" in2="blur" operator="over" />
            </filter>
            <linearGradient id="gauge-gradient" x1="0%" y1="0%" x2="100%" y2="0%">
              <stop offset="0%" stopColor={accentColor} stopOpacity="0.6" />
              <stop offset="100%" stopColor={accentColor} stopOpacity="1" />
            </linearGradient>
          </defs>

          {/* Track ring */}
          <circle
            cx={cx} cy={cy} r={R}
            fill="none"
            stroke="rgba(255,255,255,0.06)"
            strokeWidth="14"
            strokeLinecap="round"
            strokeDasharray={`${arcLength} ${gapLength}`}
            transform={`rotate(${rotationDeg} ${cx} ${cy})`}
          />

          {/* Inner dark track depth */}
          <circle
            cx={cx} cy={cy} r={R}
            fill="none"
            stroke="rgba(0,0,0,0.3)"
            strokeWidth="18"
            strokeLinecap="butt"
            strokeDasharray={`${arcLength} ${gapLength}`}
            transform={`rotate(${rotationDeg} ${cx} ${cy})`}
          />

          {/* Glow duplicate (blurred, behind main arc) */}
          <circle
            cx={cx} cy={cy} r={R}
            fill="none"
            stroke={accentColor}
            strokeWidth="10"
            strokeLinecap="round"
            strokeDashoffset="0"
            transform={`rotate(${rotationDeg} ${cx} ${cy})`}
            filter="url(#gauge-glow)"
            opacity="0.5"
            className="animate-gauge-glow-pulse transition-all duration-[1400ms] ease-[cubic-bezier(0.22,1,0.36,1)]"
            style={{ strokeDasharray: `${mounted ? fillLength : 0} ${circumference}` }}
          />

          {/* Main filled arc */}
          <circle
            cx={cx} cy={cy} r={R}
            fill="none"
            stroke="url(#gauge-gradient)"
            strokeWidth="10"
            strokeLinecap="round"
            strokeDashoffset="0"
            transform={`rotate(${rotationDeg} ${cx} ${cy})`}
            className={`${glowClass} transition-all duration-[1400ms] ease-[cubic-bezier(0.22,1,0.36,1)]`}
            style={{ strokeDasharray: `${mounted ? fillLength : 0} ${circumference}` }}
          />

          {/* Leading edge dot at arc tip */}
          {hasData && (() => {
            const tipAngle = (rotationDeg + fillRatio * ARC_DEG) * (Math.PI / 180);
            const tipX = cx + R * Math.cos(tipAngle);
            const tipY = cy + R * Math.sin(tipAngle);
            return (
              <>
                <circle
                  cx={tipX} cy={tipY} r="10"
                  fill={accentColor} opacity="0.3"
                  className={`transition-all duration-[1400ms] ease-[cubic-bezier(0.22,1,0.36,1)] ${mounted ? '' : 'opacity-0'}`}
                />
                <circle
                  cx={tipX} cy={tipY} r="5"
                  fill={accentColor}
                  className={`transition-all duration-[1400ms] ease-[cubic-bezier(0.22,1,0.36,1)] ${mounted ? '' : 'opacity-0'}`}
                />
              </>
            );
          })()}
        </svg>

        {/* Center text — hard-capped to inner ring width */}
        <div className="absolute inset-0 flex flex-col items-center justify-center pointer-events-none">
          <div
            className="flex items-baseline gap-0.5 overflow-hidden"
            style={{ maxWidth: R * 1.72 }}
          >
            <span className="uptime-number-int">{intPart}</span>
            {decPart && <span className="uptime-number-dec">{decPart}</span>}
            <span className="uptime-number-pct">%</span>
          </div>
        </div>
      </div>

      {/* Subtitle label */}
      <p className="mt-4 text-[10px] font-semibold uppercase tracking-[0.22em] text-slate-500">
        Uptime Aggregate&nbsp;
        <span className="text-slate-600">{'//'}</span>
        &nbsp;30 Day Window
      </p>
    </div>
  );
}
