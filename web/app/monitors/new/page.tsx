'use client';

import { useEffect, useState } from 'react';
import { useRouter, useSearchParams } from 'next/navigation';
import { CreateMonitorRequest, UpdateMonitorRequest } from '@/lib/types';
import { createMonitor, getMonitor } from '@/lib/api';
import { buildClonedMonitorInitialData } from '@/lib/monitor-clone';
import MonitorForm from '@/components/monitors/MonitorForm';
import PageHeader from '@/components/ui/PageHeader';
import { useToast } from '@/components/ui/ToastProvider';

export default function NewMonitorPage() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const cloneMonitorId = searchParams.get('clone');
  const [loading, setLoading] = useState(false);
  const [cloneLoading, setCloneLoading] = useState(false);
  const [initialData, setInitialData] = useState<CreateMonitorRequest | null>(null);
  const [cloneSourceName, setCloneSourceName] = useState('');
  const { showToast } = useToast();
  const isCloneMode = Boolean(cloneMonitorId);

  useEffect(() => {
    if (!cloneMonitorId) {
      setInitialData(null);
      setCloneSourceName('');
      return;
    }

    let cancelled = false;
    const loadCloneSource = async () => {
      setCloneLoading(true);
      try {
        const sourceMonitor = await getMonitor(cloneMonitorId);
        if (cancelled) return;
        setCloneSourceName(sourceMonitor.name);
        setInitialData(buildClonedMonitorInitialData(sourceMonitor));
      } catch (err: any) {
        if (cancelled) return;
        setInitialData(null);
        setCloneSourceName('');
        showToast(err?.message || 'Failed to load monitor to clone. Starting with an empty form.', 'error');
      } finally {
        if (!cancelled) {
          setCloneLoading(false);
        }
      }
    };

    void loadCloneSource();

    return () => {
      cancelled = true;
    };
  }, [cloneMonitorId, showToast]);

  const handleSubmit = async (data: CreateMonitorRequest | UpdateMonitorRequest) => {
    try {
      setLoading(true);
      const created = await createMonitor(data as CreateMonitorRequest);
      showToast('Monitor created', 'success');
      // The toast lives in the layout and survives the navigation, so there is
      // no need to hold the form on "Saving…" first. `loading` stays on until
      // the detail page replaces this one.
      router.push(`/monitors/${created.id}`);
    } catch (err: any) {
      showToast(err.message || 'Failed to create monitor', 'error');
      setLoading(false);
    }
  };

  return (
    <div className="max-w-6xl space-y-6">
      <PageHeader
        breadcrumb={[
          { label: 'Monitors', href: '/monitors' },
          { label: isCloneMode ? 'Clone monitor' : 'New' },
        ]}
        title={isCloneMode ? 'Clone monitor' : 'Create monitor'}
        subtitle={
          isCloneMode
            ? cloneSourceName
              ? `A copy of ${cloneSourceName}. Change anything before creating it.`
              : 'Loading monitor details to clone…'
            : undefined
        }
      />

      {isCloneMode && cloneLoading ? (
        <div className="glass flex h-40 items-center justify-center text-sm text-slate-500">
          Loading clone form…
        </div>
      ) : (
        <MonitorForm
          initialData={initialData || undefined}
          onSubmit={handleSubmit}
          onCancel={() => router.push('/monitors')}
          loading={loading}
        />
      )}
    </div>
  );
}
