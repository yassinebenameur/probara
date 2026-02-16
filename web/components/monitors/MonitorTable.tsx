'use client';

import {
  Monitor,
  HTTPMonitorConfig,
  PingMonitorConfig,
  DNSMonitorConfig,
  SyntheticAPIMonitorConfig,
  SyntheticBrowserMonitorConfig,
} from '@/lib/types';
import { toggleMonitorEnabled } from '@/lib/api';
import Table from '@/components/ui/Table';
import Toggle from '@/components/ui/Toggle';
import Button from '@/components/ui/Button';
import Link from 'next/link';

interface MonitorTableProps {
  monitors: Monitor[];
  onToggle?: (monitor: Monitor) => void;
  onDelete?: (id: string) => void;
}

export default function MonitorTable({
  monitors,
  onToggle,
  onDelete,
}: MonitorTableProps) {
  const handleToggle = async (monitor: Monitor) => {
    try {
      await toggleMonitorEnabled(monitor.id, !monitor.enabled);
      if (onToggle) {
        onToggle(monitor);
      }
    } catch (error) {
      console.error('Failed to toggle monitor:', error);
    }
  };

  const getMonitorTarget = (monitor: Monitor) => {
    if (monitor.type === 'http') {
      // New format: config object
      if (monitor.config && typeof monitor.config === 'object') {
        return (monitor.config as HTTPMonitorConfig).url;
      }
      // Old format: fields directly on monitor (backward compatibility)
      const oldMonitor = monitor as any;
      if (oldMonitor.url) {
        return oldMonitor.url;
      }
    } else if (monitor.type === 'ping') {
      if (monitor.config && typeof monitor.config === 'object') {
        return (monitor.config as PingMonitorConfig).host;
      }
    } else if (monitor.type === 'dns') {
      if (monitor.config && typeof monitor.config === 'object') {
        return (monitor.config as DNSMonitorConfig).host;
      }
    } else if (monitor.type === 'synthetic_api') {
      if (monitor.config && typeof monitor.config === 'object') {
        return (monitor.config as SyntheticAPIMonitorConfig).base_url || 'Workflow';
      }
    } else if (monitor.type === 'synthetic_browser') {
      if (monitor.config && typeof monitor.config === 'object') {
        return (monitor.config as SyntheticBrowserMonitorConfig).start_url;
      }
    }
    return 'N/A';
  };

  const getMonitorMethod = (monitor: Monitor) => {
    if (monitor.type === 'http') {
      // New format
      if (monitor.config && typeof monitor.config === 'object') {
        return (monitor.config as HTTPMonitorConfig).method;
      }
      // Old format (backward compatibility)
      const oldMonitor = monitor as any;
      if (oldMonitor.method) {
        return oldMonitor.method;
      }
    } else if (monitor.type === 'dns') {
      if (monitor.config && typeof monitor.config === 'object') {
        return ((monitor.config as DNSMonitorConfig).record_type || 'A').toUpperCase();
      }
    } else if (monitor.type === 'synthetic_api') {
      return 'WORKFLOW';
    } else if (monitor.type === 'synthetic_browser') {
      return 'BROWSER';
    }
    return monitor.type.toUpperCase();
  };

  return (
    <Table
      headers={['Name', 'Type', 'Target', 'Method', 'Interval', 'Status', 'Actions']}
    >
      {monitors.map((monitor) => (
        <tr key={monitor.id} className="hover:bg-gray-50">
          <td className="px-6 py-4 whitespace-nowrap">
            <div className="text-sm font-medium text-gray-900">{monitor.name}</div>
            {monitor.tags && monitor.tags.length > 0 && (
              <div className="text-sm text-gray-500">
                {monitor.tags.map((tag) => (
                  <span key={tag} className="mr-2 text-xs bg-gray-100 px-2 py-0.5 rounded">
                    {tag}
                  </span>
                ))}
              </div>
            )}
          </td>
          <td className="px-6 py-4 whitespace-nowrap">
            <span className="px-2 py-1 text-xs font-medium bg-blue-100 text-blue-800 rounded uppercase">
              {monitor.type}
            </span>
          </td>
          <td className="px-6 py-4">
            <div className="text-sm text-gray-900 max-w-xs truncate">{getMonitorTarget(monitor)}</div>
          </td>
          <td className="px-6 py-4 whitespace-nowrap">
            <span className="px-2 py-1 text-xs font-medium bg-gray-100 text-gray-800 rounded">
              {getMonitorMethod(monitor)}
            </span>
          </td>
          <td className="px-6 py-4 whitespace-nowrap text-sm text-gray-500">
            {monitor.interval_seconds}s
          </td>
          <td className="px-6 py-4 whitespace-nowrap">
            <Toggle
              checked={monitor.enabled}
              onChange={() => handleToggle(monitor)}
            />
          </td>
          <td className="px-6 py-4 whitespace-nowrap text-sm font-medium">
            <div className="flex gap-2">
              <Link href={`/monitors/${monitor.id}`}>
                <Button variant="secondary" className="text-xs py-1 px-2">
                  Edit
                </Button>
              </Link>
              {onDelete && (
                <Button
                  variant="danger"
                  className="text-xs py-1 px-2"
                  onClick={() => onDelete(monitor.id)}
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
