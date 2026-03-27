'use client';

import { useEffect, useState } from 'react';

import { getIncidents } from '@/lib/api';
import type { IncidentDetail, IncidentListItem } from '@/lib/types';
import IncidentCreateDialog from '@/components/incidents/IncidentCreateDialog';
import IncidentList from '@/components/incidents/IncidentList';
import Toast from '@/components/ui/Toast';

export default function IncidentsPage() {
  const [incidents, setIncidents] = useState<IncidentListItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [dialogOpen, setDialogOpen] = useState(false);
  const [toast, setToast] = useState<{ message: string; type: 'success' | 'error' } | null>(null);

  const loadIncidents = async () => {
    try {
      setLoading(true);
      setError('');
      const response = await getIncidents({ page_size: 100 });
      setIncidents(response.items || []);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load incidents');
      setIncidents([]);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    loadIncidents();
  }, []);

  const handleCreated = async (_incident: IncidentDetail) => {
    await loadIncidents();
    setToast({ message: 'Incident created successfully', type: 'success' });
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-semibold text-white">Incidents</h1>
          <p className="mt-1 text-sm text-slate-500">
            Coordinate customer-impacting issues and publish updates deliberately.
          </p>
        </div>
        <button onClick={() => setDialogOpen(true)} className="btn btn-primary btn-sm">
          <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
          </svg>
          Create Incident
        </button>
      </div>

      {loading ? (
        <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-6 text-sm text-slate-500">
          Loading incidents...
        </div>
      ) : error ? (
        <div className="rounded-xl border border-rose-500/20 bg-rose-500/10 px-4 py-3 text-sm text-rose-300">
          {error}
          <button onClick={loadIncidents} className="ml-2 underline hover:no-underline">
            Retry
          </button>
        </div>
      ) : (
        <IncidentList incidents={incidents} />
      )}

      <IncidentCreateDialog
        open={dialogOpen}
        onClose={() => setDialogOpen(false)}
        onCreated={handleCreated}
      />

      {toast ? (
        <Toast
          message={toast.message}
          type={toast.type}
          onClose={() => setToast(null)}
        />
      ) : null}
    </div>
  );
}
