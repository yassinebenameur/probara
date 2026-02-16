'use client';

import { CheckResult } from '@/lib/types';
import Card from '@/components/ui/Card';

interface StatusTimelineProps {
  results: CheckResult[];
  loading?: boolean;
}

export default function StatusTimeline({ results, loading }: StatusTimelineProps) {
  const getStatusColor = (status: string) => {
    switch (status) {
      case 'success':
        return 'bg-green-500';
      case 'failure':
        return 'bg-red-500';
      case 'error':
        return 'bg-orange-500';
      default:
        return 'bg-gray-400';
    }
  };

  if (loading) {
    return (
      <Card title="Status Timeline">
        <div className="text-center py-8 text-gray-500">Loading timeline data...</div>
      </Card>
    );
  }

  if (results.length === 0) {
    return (
      <Card title="Status Timeline">
        <div className="text-center py-8 text-gray-500">No status data available</div>
      </Card>
    );
  }

  // Sort results chronologically (oldest first)
  const sortedResults = [...results].sort(
    (a, b) => new Date(a.created_at).getTime() - new Date(b.created_at).getTime()
  );

  // Group results into time buckets for visualization (last 50 results)
  const displayResults = sortedResults.slice(-50);

  return (
    <Card title="Status Timeline">
      <div className="py-4">
        <div className="flex items-center gap-1 flex-wrap">
          {displayResults.map((result, index) => (
            <div
              key={result.id}
              className={`w-3 h-8 rounded ${getStatusColor(result.status)}`}
              title={`${new Date(result.created_at).toLocaleString()} - ${result.status}`}
            />
          ))}
        </div>
        <div className="mt-4 flex items-center gap-4 text-sm">
          <div className="flex items-center gap-2">
            <div className="w-4 h-4 bg-green-500 rounded"></div>
            <span>Success</span>
          </div>
          <div className="flex items-center gap-2">
            <div className="w-4 h-4 bg-red-500 rounded"></div>
            <span>Failure</span>
          </div>
          <div className="flex items-center gap-2">
            <div className="w-4 h-4 bg-orange-500 rounded"></div>
            <span>Error</span>
          </div>
        </div>
        <div className="mt-2 text-xs text-gray-500">
          Showing last {displayResults.length} checks (hover for details)
        </div>
      </div>
    </Card>
  );
}

