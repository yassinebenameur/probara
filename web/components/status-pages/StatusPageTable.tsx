'use client';

import { StatusPage } from '@/lib/types';
import Table from '@/components/ui/Table';
import Button from '@/components/ui/Button';
import Link from 'next/link';

interface StatusPageTableProps {
  statusPages: StatusPage[];
  onDelete?: (id: string) => void;
}

export default function StatusPageTable({
  statusPages,
  onDelete,
}: StatusPageTableProps) {
  return (
    <Table
      headers={['Title', 'Slug', 'Monitors', 'Actions']}
    >
      {statusPages.map((page) => (
        <tr key={page.id} className="hover:bg-gray-50">
          <td className="px-6 py-4 whitespace-nowrap">
            <div className="text-sm font-medium text-gray-900">{page.title}</div>
            {page.description && (
              <div className="text-sm text-gray-500 max-w-xs truncate">
                {page.description}
              </div>
            )}
          </td>
          <td className="px-6 py-4 whitespace-nowrap">
            <code className="text-sm bg-gray-100 px-2 py-1 rounded">{page.slug}</code>
          </td>
          <td className="px-6 py-4 whitespace-nowrap text-sm text-gray-500">
            {page.monitor_ids?.length || 0} monitor{(page.monitor_ids?.length || 0) !== 1 ? 's' : ''}
          </td>
          <td className="px-6 py-4 whitespace-nowrap text-sm font-medium">
            <div className="flex gap-2">
              <Link href={`/status-pages/${page.id}`}>
                <Button variant="secondary" className="text-xs py-1 px-2">
                  Edit
                </Button>
              </Link>
              {onDelete && (
                <Button
                  variant="danger"
                  className="text-xs py-1 px-2"
                  onClick={() => onDelete(page.id)}
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

