'use client';

import { useEffect, useState } from 'react';
import Link from 'next/link';
import { LayoutGrid, Pencil, Plus, Send, Trash2 } from 'lucide-react';
import { AlertChannel } from '@/lib/types';
import { deleteAlertChannel, getAlertChannels, testAlertChannel } from '@/lib/api';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import IconButton from '@/components/ui/IconButton';
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

  const actions = (
    <div className="flex items-center gap-2">
      <Button variant="ghost" size="sm" icon={<LayoutGrid strokeWidth={1.75} />} asChild>
        <Link href="/alert-channels/catalog">Browse catalog</Link>
      </Button>
      <Button variant="accent" size="sm" icon={<Plus strokeWidth={1.75} />} asChild>
        <Link href="/alert-channels/new">New channel</Link>
      </Button>
    </div>
  );

  return (
    <div>
      {!loading && !error && channels.length === 0 ? (
        <EmptyState
          icon={<Send strokeWidth={1.5} />}
          title="No alert channels yet"
          description="Add a channel so alerts reach your team."
          action={actions}
        />
      ) : (
        <div className="glass overflow-hidden">
          <div className="flex flex-wrap items-center justify-between gap-3 p-4">
            <p className="text-[13px] text-slate-400">Where alert notifications are delivered.</p>
            {actions}
          </div>

          {loading ? (
            <p className="px-4 pb-4 text-sm text-slate-500">Loading alert channels…</p>
          ) : error ? (
            <div className="px-4 pb-4">
              <Callout
                tone="danger"
                action={<Button variant="ghost" size="xs" onClick={loadChannels}>Retry</Button>}
              >
                {error}
              </Callout>
            </div>
          ) : (
            <div className="overflow-x-auto">
              <table className="data-table text-sm">
                <thead>
                  <tr>
                    <th>Name</th>
                    <th>Type</th>
                    <th>Status</th>
                    <th><span className="sr-only">Actions</span></th>
                  </tr>
                </thead>
                <tbody>
                  {channels.map((channel) => (
                    <tr key={channel.id}>
                      <td>
                        <Link
                          href={`/alert-channels/${channel.id}`}
                          className="font-medium text-white transition-colors hover:text-cyan-300"
                        >
                          {channel.name}
                        </Link>
                      </td>
                      <td className="text-[13px] text-slate-400">{channel.type}</td>
                      <td>
                        <Pill tone={channel.is_active ? 'success' : 'neutral'} size="xs" dot>
                          {channel.is_active ? 'Active' : 'Inactive'}
                        </Pill>
                      </td>
                      <td className="text-right">
                        <div className="inline-flex items-center gap-0.5">
                          <IconButton
                            icon={<Send strokeWidth={1.75} />}
                            label="Send a test notification"
                            onClick={() => handleTest(channel.id)}
                          />
                          <IconButton
                            icon={<Pencil strokeWidth={1.75} />}
                            label="Edit"
                            href={`/alert-channels/${channel.id}`}
                          />
                          <IconButton
                            icon={<Trash2 strokeWidth={1.75} />}
                            label="Delete"
                            onClick={() => setPendingDelete(channel)}
                            danger
                          />
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
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
