'use client';

import { useEffect, useState } from 'react';
import { useRouter } from 'next/navigation';
import Link from 'next/link';
import { AlertChannel } from '@/lib/types';
import { deleteAlertChannel, getAlertChannels, testAlertChannel } from '@/lib/api';
import Panel from '@/components/ui/Panel';
import Toast from '@/components/ui/Toast';

export default function AlertChannelsPage() {
  const router = useRouter();
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
    if (!confirm('Are you sure you want to delete this alert channel?')) {
      return;
    }

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

  if (loading) {
    return (
      <div className="flex h-64 items-center justify-center">
        <div className="text-muted">Loading alert channels...</div>
      </div>
    );
  }

  if (error) {
    return (
      <Panel title="Error" subtitle="Failed to load alert channels">
        <div className="text-danger">{error}</div>
        <button
          onClick={loadChannels}
          className="btn btn-secondary btn-sm mt-4"
        >
          Retry
        </button>
      </Panel>
    );
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <div />
        <button
          onClick={() => router.push('/alert-channels/new')}
          className="btn btn-primary btn-sm"
        >
          + Create Alert Channel
        </button>
      </div>

      <Panel
        title="Alert Channels"
        subtitle={`${channels.length} channel${channels.length !== 1 ? 's' : ''} configured`}
      >
        <div className="table-card mt-1">
          {channels.length === 0 ? (
            <div className="py-12 text-center">
              <div className="text-4xl mb-3">📣</div>
              <div className="text-lg font-medium mb-1">No alert channels yet</div>
              <div className="text-sm text-muted mb-4">
                Create your first channel to deliver alerts to your team.
              </div>
              <button
                onClick={() => router.push('/alert-channels/new')}
                className="btn btn-primary btn-sm"
              >
                Create Alert Channel
              </button>
            </div>
          ) : (
            <table className="data-table text-xs">
              <thead>
                <tr>
                  <th>
                    Name
                  </th>
                  <th>
                    Type
                  </th>
                  <th>
                    Status
                  </th>
                  <th className="text-right">
                    Actions
                  </th>
                </tr>
              </thead>
              <tbody>
                {channels.map((channel, idx) => (
                  <tr key={channel.id} className={idx % 2 === 1 ? 'bg-[rgba(15,23,42,0.35)]' : ''}>
                    <td>
                      <div className="font-medium text-sm">{channel.name}</div>
                    </td>
                    <td className="text-muted">
                      {channel.type.toUpperCase()}
                    </td>
                    <td>
                      <span className={channel.is_active ? 'badge badge-success' : 'badge badge-default'}>
                        {channel.is_active ? 'Active' : 'Inactive'}
                      </span>
                    </td>
                    <td className="text-right">
                      <button
                        onClick={() => handleTest(channel.id)}
                        className="btn btn-outline btn-xs"
                      >
                        Test
                      </button>
                      <Link href={`/alert-channels/${channel.id}`}>
                        <button className="ml-1 btn btn-secondary btn-xs">
                          Edit
                        </button>
                      </Link>
                      <button
                        onClick={() => handleDelete(channel.id)}
                        className="ml-1 btn btn-danger btn-xs"
                      >
                        Delete
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </Panel>

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
