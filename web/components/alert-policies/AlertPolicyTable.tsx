'use client';

import { AlertPolicy } from '@/lib/types';
import Table from '@/components/ui/Table';
import Button from '@/components/ui/Button';
import Link from 'next/link';

interface AlertPolicyTableProps {
  policies: AlertPolicy[];
  onDelete?: (id: string) => void;
}

export default function AlertPolicyTable({
  policies,
  onDelete,
}: AlertPolicyTableProps) {
  return (
    <Table
      headers={['Name', 'Failure Threshold', 'Window', 'Description', 'Actions']}
    >
      {policies.map((policy) => (
        <tr key={policy.id} className="hover:bg-gray-50">
          <td className="px-6 py-4 whitespace-nowrap">
            <div className="text-sm font-medium text-gray-900">{policy.name}</div>
          </td>
          <td className="px-6 py-4 whitespace-nowrap text-sm text-gray-900">
            {policy.failure_threshold}
          </td>
          <td className="px-6 py-4 whitespace-nowrap text-sm text-gray-500">
            {policy.failure_window_seconds}s
          </td>
          <td className="px-6 py-4">
            <div className="text-sm text-gray-500 max-w-xs truncate">
              {policy.description || '-'}
            </div>
          </td>
          <td className="px-6 py-4 whitespace-nowrap text-sm font-medium">
            <div className="flex gap-2">
              <Link href={`/alert-policies/${policy.id}`}>
                <Button variant="secondary" className="text-xs py-1 px-2">
                  Edit
                </Button>
              </Link>
              {onDelete && (
                <Button
                  variant="danger"
                  className="text-xs py-1 px-2"
                  onClick={() => onDelete(policy.id)}
                >
                  Delete
                </Button>
              )}
            </div>
          </td>
        </tr>
      ))}
    </Table>
  );
}

