'use client';

import { useEffect, useState } from 'react';
import Link from 'next/link';
import { LayoutGrid, Pencil, Plus, Send, Trash2 } from 'lucide-react';
import { AlertChannel } from '@/lib/types';
import { deleteAlertChannel, getAlertChannels, testAlertChannel } from '@/lib/api';
import Panel from '@/components/ui/Panel';
import Toast from '@/components/ui/Toast';
import Button from '@/components/ui/Button';
import Pill from '@/components/ui/Pill';
import PageHeader from '@/components/ui/PageHeader';
import EmptyState from '@/components/ui/EmptyState';

export default function AlertChannelsPage() {
  const [channels, setChannels] = useState<AlertChannel[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>('');
  const [toast, setToast] = useState<{ message: string; type: 'success' | 'error' } | null>(null);

  useEffect(() => {
    loadChannels();
  }, []);

  const loadChannels = async () => {
    try {
      setLoading(true);
      setError('');
      const response = await getAlertChannels({ page_size: 100 });
      setChannels(response?.items || []);
    } catch (err: any) {
      setError(err.message || 'Failed to load alert channels');
      setChannels([]);
    } finally {
      setLoading(false);
    }
  };

  const handleDelete = async (id: string) => {
    if (!confirm('Are you sure you want to delete this alert channel?')) return;
    try {
      await deleteAlertChannel(id);
      setChannels((channels || []).filter((c) => c.id !== id));
      setToast({ message: 'Alert channel deleted successfully', type: 'success' });
    } catch (err: any) {
      setToast({ message: err.message || 'Failed to delete alert channel', type: 'error' });
    }
  };

  const handleTest = async (id: string) => {
    try {
      await testAlertChannel(id);
      setToast({ message: 'Test notification sent', type: 'success' });
    } catch (err: any) {
      setToast({ message: err.message || 'Failed to send test notification', type: 'error' });
    }
  };

  const header = (
    <PageHeader
      title="Alert channels"
      subtitle="Deliver alerts to your team."
      action={
        <div className="flex items-center gap-2">
          <Button variant="ghost" size="sm" icon={<LayoutGrid strokeWidth={1.75} />} asChild>
            <Link href="/alert-channels/catalog">Browse catalog</Link>
          </Button>
          <Button variant="ghost" size="sm" icon={<Plus strokeWidth={1.75} />} asChild>
            <Link href="/alert-channels/new">Create alert channel</Link>
          </Button>
        </div>
      }
    />
  );

  return (
    <div className="flex flex-col gap-4">
      {header}

      {loading ? (
        <Panel title="Alert channels" subtitle="Loading channels">
          <div className="text-sm text-slate-500">Loading alert channels…</div>
        </Panel>
      ) : error ? (
        <Panel title="Error" subtitle="Failed to load alert channels">
          <div className="text-sm text-rose-400">{error}</div>
          <div className="mt-3">
            <Button variant="ghost" size="sm" onClick={loadChannels}>Retry</Button>
          </div>
        </Panel>
      ) : (
        <Panel
          title="Alert channels"
          subtitle={`${channels.length} channel${channels.length !== 1 ? 's' : ''} configured`}
        >
          <div className="table-card mt-1">
            {channels.length === 0 ? (
              <EmptyState
                icon={<Send strokeWidth={1.5} />}
                title="No alert channels yet"
                description="Create your first channel to deliver alerts to your team."
                action={
                  <Button variant="ghost" size="sm" icon={<Plus strokeWidth={1.75} />} asChild>
                    <Link href="/alert-channels/new">Create alert channel</Link>
                  </Button>
                }
              />
            ) : (
              <table className="data-table text-xs">
                <thead>
                  <tr>
                    <th>Name</th>
                    <th>Type</th>
                    <th>Status</th>
                    <th className="text-right">Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {channels.map((channel, idx) => (
                    <tr key={channel.id} className={idx % 2 === 1 ? 'bg-[rgba(15,23,42,0.35)]' : ''}>
                      <td>
                        <div className="font-medium text-sm">{channel.name}</div>
                      </td>
                      <td className="text-slate-400">
                        {channel.type.toUpperCase()}
                      </td>
                      <td>
                        <Pill tone={channel.is_active ? 'success' : 'neutral'} size="xs" dot>
                          {channel.is_active ? 'Active' : 'Inactive'}
                        </Pill>
                      </td>
                      <td className="text-right">
                        <div className="inline-flex items-center gap-1.5">
                          <Button
                            variant="ghost"
                            size="xs"
                            icon={<Send strokeWidth={1.75} />}
                            onClick={() => handleTest(channel.id)}
                          >
                            Test
                          </Button>
                          <Button variant="ghost" size="xs" icon={<Pencil strokeWidth={1.75} />} asChild>
                            <Link href={`/alert-channels/${channel.id}`}>Edit</Link>
                          </Button>
                          <Button
                            variant="danger"
                            size="xs"
                            icon={<Trash2 strokeWidth={1.75} />}
                            onClick={() => handleDelete(channel.id)}
                          >
                            Delete
                          </Button>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        </Panel>
      )}

      {toast && (
        <Toast
          message={toast.message}
          type={toast.type}
          onClose={() => setToast(null)}
        />
      )}
    </div>
  );
}
