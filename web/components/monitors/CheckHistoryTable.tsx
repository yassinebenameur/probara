'use client';

import { CheckResult } from '@/lib/types';
import Card from '@/components/ui/Card';

interface CheckHistoryTableProps {
  results: CheckResult[];
  loading?: boolean;
}

export default function CheckHistoryTable({ results, loading }: CheckHistoryTableProps) {
  const getStatusColor = (status: string) => {
    switch (status) {
      case 'success':
        return 'text-green-600 bg-green-50';
      case 'failure':
        return 'text-red-600 bg-red-50';
      case 'error':
        return 'text-orange-600 bg-orange-50';
      default:
        return 'text-gray-600 bg-gray-50';
    }
  };

  const getStatusLabel = (status: string) => {
    switch (status) {
      case 'success':
        return 'Success';
      case 'failure':
        return 'Failure';
      case 'error':
        return 'Error';
      default:
        return 'Unknown';
    }
  };

  if (loading) {
    return (
      <Card title="Recent Check History">
        <div className="text-center py-8 text-gray-500">Loading check history...</div>
      </Card>
    );
  }

  if (results.length === 0) {
    return (
      <Card title="Recent Check History">
        <div className="text-center py-8 text-gray-500">No check results available</div>
      </Card>
    );
  }

  return (
    <Card title="Recent Check History">
      <div className="overflow-x-auto">
        <table className="min-w-full divide-y divide-gray-200">
          <thead className="bg-gray-50">
            <tr>
              <th className="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                Time
              </th>
              <th className="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                Status
              </th>
              <th className="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                HTTP Status
              </th>
              <th className="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                Latency
              </th>
              <th className="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                Error Message
              </th>
            </tr>
          </thead>
          <tbody className="bg-white divide-y divide-gray-200">
            {results.map((result) => (
              <tr key={result.id}>
                <td className="px-6 py-4 whitespace-nowrap text-sm text-gray-900">
                  {new Date(result.created_at).toLocaleString()}
                </td>
                <td className="px-6 py-4 whitespace-nowrap">
                  <span
                    className={`inline-flex px-2 py-1 text-xs font-semibold rounded-full ${getStatusColor(
                      result.status
                    )}`}
                  >
                    {getStatusLabel(result.status)}
                  </span>
                </td>
                <td className="px-6 py-4 whitespace-nowrap text-sm text-gray-900">
                  {result.http_status ?? '—'}
                </td>
                <td className="px-6 py-4 whitespace-nowrap text-sm text-gray-900">
                  {result.latency_ms !== undefined ? `${result.latency_ms} ms` : '—'}
                </td>
                <td className="px-6 py-4 text-sm text-gray-900">
                  {result.error_message || '—'}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Card>
  );
}

