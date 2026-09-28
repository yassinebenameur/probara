'use client';

import { useEffect, useState } from 'react';
import Link from 'next/link';
import { LayoutGrid, Plus, Send, Trash2 } from 'lucide-react';
import { AlertChannel } from '@/lib/types';
import { deleteAlertChannel, getAlertChannels, testAlertChannel } from '@/lib/api';
import Button from '@/components/ui/Button';
import Pill from '@/components/ui/Pill';
import EmptyState from '@/components/ui/EmptyState';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import { useToast } from '@/components/ui/ToastProvider';

// The workspace's alert channels, shown as the Channels tab of Settings.
export default function AlertChannelList() {
  const { showToast } = useToast();
  const [channels, setChannels] = useState<AlertChannel[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>('');
  const [pendingDelete, setPendingDelete] = useState<AlertChannel | null>(null);
  const [deleting, setDeleting] = useState(false);

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

  const handleDelete = async () => {
    if (!pendingDelete) return;
    setDeleting(true);
    try {
      await deleteAlertChannel(pendingDelete.id);
      setChannels((channels || []).filter((c) => c.id !== pendingDelete.id));
      showToast('Alert channel deleted successfully', 'success');
      setPendingDelete(null);
    } catch (err: any) {
      showToast(err.message || 'Failed to delete alert channel', 'error');
    } finally {
      setDeleting(false);
    }
  };

  const handleTest = async (id: string) => {
    try {
      await testAlertChannel(id);
      showToast('Test notification sent', 'success');
    } catch (err: any) {
      showToast(err.message || 'Failed to send test notification', 'error');
    }
  };

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-xs text-slate-500">Where alert notifications are delivered.</p>
        <div className="flex items-center gap-2">
          <Button variant="ghost" size="sm" icon={<LayoutGrid strokeWidth={1.75} />} asChild>
            <Link href="/alert-channels/catalog">Browse catalog</Link>
          </Button>
          <Button variant="accent" size="sm" icon={<Plus strokeWidth={1.75} />} asChild>
            <Link href="/alert-channels/new">New channel</Link>
          </Button>
        </div>
      </div>

      {loading ? (
        <div className="text-sm text-slate-500">Loading alert channels…</div>
      ) : error ? (
        <div className="flex items-center justify-between gap-3 rounded-xl border border-rose-500/20 bg-rose-500/10 px-4 py-3 text-sm text-rose-300">
          <span>{error}</span>
          <Button variant="ghost" size="xs" onClick={loadChannels}>Retry</Button>
        </div>
      ) : channels.length === 0 ? (
        <EmptyState
          icon={<Send strokeWidth={1.5} />}
          title="No alert channels yet"
          description="Add a channel so alerts reach your team."
        />
      ) : (
        <div className="table-card">
          <table className="data-table text-xs">
            <thead>
              <tr>
                <th>Name</th>
                <th>Type</th>
                <th>Status</th>
                <th><span className="sr-only">Actions</span></th>
              </tr>
            </thead>
            <tbody>
              {channels.map((channel, idx) => (
                <tr key={channel.id} className={idx % 2 === 1 ? 'bg-slate-900/[0.35]' : ''}>
                  <td>
                    <Link
                      href={`/alert-channels/${channel.id}`}
                      className="text-sm font-medium text-white hover:text-cyan-400"
                    >
                      {channel.name}
                    </Link>
                  </td>
                  <td className="text-slate-400">{channel.type}</td>
                  <td>
                    <Pill tone={channel.is_active ? 'success' : 'neutral'} size="xs" dot>
                      {channel.is_active ? 'Active' : 'Inactive'}
                    </Pill>
                  </td>
                  <td className="text-right">
                    <div className="inline-flex items-center gap-0.5">
                      <Button
                        variant="subtle"
                        size="xs"
                        icon={<Send strokeWidth={1.75} />}
                        onClick={() => handleTest(channel.id)}
                        title="Send a test notification"
                      >
                        <span className="sr-only">Test</span>
                      </Button>
                      <Button
                        variant="subtle"
                        size="xs"
                        icon={<Trash2 strokeWidth={1.75} />}
                        onClick={() => setPendingDelete(channel)}
                        title="Delete"
                      >
                        <span className="sr-only">Delete</span>
                      </Button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <ConfirmDialog
        open={pendingDelete !== null}
        title="Delete alert channel"
        description={
          pendingDelete
            ? `“${pendingDelete.name}” will be removed and will stop receiving alerts. This cannot be undone.`
            : undefined
        }
        confirmLabel="Delete channel"
        loading={deleting}
        onConfirm={handleDelete}
        onCancel={() => !deleting && setPendingDelete(null)}
      />
    </div>
  );
}
