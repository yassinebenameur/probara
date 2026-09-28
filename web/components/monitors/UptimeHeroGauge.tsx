'use client';

import { useState, useEffect } from 'react';
import { DEFAULT_SLA_TARGET } from '@/lib/sla';

interface UptimeHeroGaugeProps {
  uptime: number;
  hasData: boolean;
  /** The strictest SLA covering the monitor (lib/sla strictestTarget). */
  target?: number;
}

export function UptimeHeroGauge({ uptime, hasData, target = DEFAULT_SLA_TARGET }: UptimeHeroGaugeProps) {
  const [mounted, setMounted] = useState(false);
  useEffect(() => {
    const id = requestAnimationFrame(() => setMounted(true));
    return () => cancelAnimationFrame(id);
  }, []);

  const isCompliant = uptime >= target;

  // Compact gauge geometry tuned for a denser hero layout.
  const SIZE = 300;
  const cx = SIZE / 2;
  const cy = SIZE / 2;
  const R = 116;
  const GAP_DEG = 100;
  const ARC_DEG = 360 - GAP_DEG;
  const circumference = 2 * Math.PI * R;
  const arcLength = (ARC_DEG / 360) * circumference;
  const gapLength = circumference - arcLength;

  const fillRatio = hasData ? Math.max(0, Math.min(1, uptime / 100)) : 0;
  const fillLength = fillRatio * arcLength;

  // SVG 0deg = 3-o'clock; gap centred at 6-o'clock → start at 140deg
  const rotationDeg = 90 + GAP_DEG / 2;

  const accentColor = isCompliant ? '#ff5a24' : '#f04a5a';

  // Split uptime value into integer and decimal parts for differential sizing
  const uptimeStr = hasData ? uptime.toFixed(3) : '—';
  const dotIdx = uptimeStr.indexOf('.');
  const intPart = dotIdx >= 0 ? uptimeStr.slice(0, dotIdx) : uptimeStr;
  const decPart = dotIdx >= 0 ? uptimeStr.slice(dotIdx) : '';

  return (
    <div className="flex flex-col items-center select-none py-2">
      <div className="relative aspect-square w-[200px] sm:w-[220px] lg:w-[250px]">
        <svg
          width="100%"
          height="100%"
          viewBox={`0 0 ${SIZE} ${SIZE}`}
          className="overflow-visible"
        >
          {/* Track ring */}
          <circle
            cx={cx} cy={cy} r={R}
            fill="none"
            className="stroke-white/[0.07]"
            strokeWidth="10"
            strokeLinecap="round"
            strokeDasharray={`${arcLength} ${gapLength}`}
            transform={`rotate(${rotationDeg} ${cx} ${cy})`}
          />

          {/* Main filled arc */}
          <circle
            cx={cx} cy={cy} r={R}
            fill="none"
            stroke={accentColor}
            strokeWidth="10"
            strokeLinecap="round"
            strokeDashoffset="0"
            transform={`rotate(${rotationDeg} ${cx} ${cy})`}
            className={`transition-all duration-[1400ms] ease-[cubic-bezier(0.22,1,0.36,1)]`}
            style={{ strokeDasharray: `${mounted ? fillLength : 0} ${circumference}` }}
          />

          {/* Leading edge dot at arc tip */}
          {hasData && (() => {
            const tipAngle = (rotationDeg + fillRatio * ARC_DEG) * (Math.PI / 180);
            const tipX = cx + R * Math.cos(tipAngle);
            const tipY = cy + R * Math.sin(tipAngle);
            return (
              <circle
                cx={tipX} cy={tipY} r="5"
                fill={accentColor}
                className={`transition-all duration-[1400ms] ease-[cubic-bezier(0.22,1,0.36,1)] ${mounted ? '' : 'opacity-0'}`}
              />
            );
          })()}
        </svg>

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
    </div>
  );
}
