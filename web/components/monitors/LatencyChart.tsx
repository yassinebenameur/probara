'use client';

import { useState, useEffect } from 'react';
import dynamic from 'next/dynamic';
import { CheckResult } from '@/lib/types';
import Card from '@/components/ui/Card';

// Chart data type
interface ChartDataPoint {
  time: string;
  timestamp: number;
  latency: number;
}

// Dynamically import the entire chart to avoid SSR issues
const RechartsLineChart = dynamic(
  () => import('recharts').then((mod) => {
    const { LineChart, Line, XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer } = mod;
    const RechartsLineChartInner = ({ data }: { data: ChartDataPoint[] }) => (
      <ResponsiveContainer width="100%" height="100%">
        <LineChart data={data}>
          <CartesianGrid strokeDasharray="3 3" />
          <XAxis
            dataKey="time"
            angle={-45}
            textAnchor="end"
            height={80}
            interval="preserveStartEnd"
          />
          <YAxis label={{ value: 'Latency (ms)', angle: -90, position: 'insideLeft' }} />
          <Tooltip
            formatter={(value: number) => [`${value} ms`, 'Latency']}
            labelFormatter={(label) => `Time: ${label}`}
          />
          <Line
            type="monotone"
            dataKey="latency"
            stroke="#1e90ff"
            strokeWidth={2}
            dot={{ r: 3 }}
            activeDot={{ r: 5 }}
          />
        </LineChart>
      </ResponsiveContainer>
    );

    RechartsLineChartInner.displayName = 'RechartsLineChartInner';
    return RechartsLineChartInner;
  }),
  { ssr: false }
);

interface LatencyChartProps {
  results: CheckResult[];
  loading?: boolean;
}

export default function LatencyChart({ results, loading }: LatencyChartProps) {
  const [mounted, setMounted] = useState(false);

  useEffect(() => {
    setMounted(true);
  }, []);

  if (loading) {
    return (
      <Card title="Latency Over Time">
        <div className="text-center py-8 text-gray-500">Loading chart data...</div>
      </Card>
    );
  }

  // Filter results that have latency data and sort chronologically
  const chartData = results
    .filter((r) => r.latency_ms !== undefined)
    .map((r) => ({
      time: new Date(r.created_at).toLocaleTimeString(),
      timestamp: new Date(r.created_at).getTime(),
      latency: r.latency_ms!,
    }))
    .sort((a, b) => a.timestamp - b.timestamp);

  if (chartData.length === 0) {
    return (
      <Card title="Latency Over Time">
        <div className="text-center py-8 text-gray-500">No latency data available</div>
      </Card>
    );
  }

  if (!mounted) {
    return (
      <Card title="Latency Over Time">
        <div className="text-center py-8 text-gray-500">Loading chart...</div>
      </Card>
    );
  }

  return (
    <Card title="Latency Over Time">
      <div style={{ width: '100%', height: '300px' }}>
        <RechartsLineChart data={chartData} />
      </div>
    </Card>
  );
}
