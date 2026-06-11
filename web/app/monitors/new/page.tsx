'use client';

import { useEffect, useState } from 'react';
import { useRouter, useSearchParams } from 'next/navigation';
import { CreateMonitorRequest, UpdateMonitorRequest } from '@/lib/types';
import { createMonitor, getMonitor } from '@/lib/api';
import { buildClonedMonitorInitialData } from '@/lib/monitor-clone';
import MonitorForm from '@/components/monitors/MonitorForm';
import PageHeader from '@/components/ui/PageHeader';
import FormCard from '@/components/ui/FormCard';
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
  }, [cloneMonitorId]);

  const handleSubmit = async (data: CreateMonitorRequest | UpdateMonitorRequest) => {
    try {
      setLoading(true);
      const created = await createMonitor(data as CreateMonitorRequest);
      showToast('Monitor created successfully', 'success');
      setTimeout(() => {
        router.push(`/monitors/${created.id}`);
      }, 1000);
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
              ? `Cloning "${cloneSourceName}". Update anything before creating the new monitor.`
              : 'Loading monitor details to clone…'
            : 'Set up a new monitor to track your service availability.'
        }
      />

      <FormCard>
        {isCloneMode && cloneLoading ? (
          <div className="flex h-40 items-center justify-center text-sm text-slate-500">
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
      </FormCard>

    </div>
  );
}
