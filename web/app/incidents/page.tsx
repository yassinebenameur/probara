'use client';

import { useEffect, useState } from 'react';

import { getIncidents, getUsers } from '@/lib/api';
import type { AdminUser, IncidentDetail, IncidentListItem } from '@/lib/types';
import IncidentQuickCreateButton from '@/components/incidents/IncidentQuickCreateButton';
import IncidentList from '@/components/incidents/IncidentList';
import Toast from '@/components/ui/Toast';

export default function IncidentsPage() {
  const [incidents, setIncidents] = useState<IncidentListItem[]>([]);
  const [users, setUsers] = useState<AdminUser[]>([]);
  const [loading, setLoading] = useState(true);
  const [usersLoading, setUsersLoading] = useState(true);
  const [error, setError] = useState('');
  const [usersError, setUsersError] = useState('');
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

  const loadUsers = async () => {
    try {
      setUsersLoading(true);
      setUsersError('');
      const response = await getUsers({ page_size: 1000 });
      setUsers(response.items || []);
    } catch (err) {
      setUsersError(err instanceof Error ? err.message : 'Failed to load users');
      setUsers([]);
    } finally {
      setUsersLoading(false);
    }
  };

  useEffect(() => {
    void loadIncidents();
    void loadUsers();
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
        <IncidentQuickCreateButton
          users={users}
          onCreated={handleCreated}
          disabled={usersLoading || Boolean(usersError) || users.length === 0}
        />
      </div>

      {usersLoading ? (
        <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 px-4 py-3 text-sm text-slate-500">
          Loading users for incident ownership...
        </div>
      ) : usersError ? (
        <div className="rounded-xl border border-rose-500/20 bg-rose-500/10 px-4 py-3 text-sm text-rose-300">
          {usersError}
          <button onClick={loadUsers} className="ml-2 underline hover:no-underline">
            Retry
          </button>
        </div>
      ) : users.length === 0 ? (
        <div className="rounded-xl border border-amber-500/20 bg-amber-500/10 px-4 py-3 text-sm text-amber-200">
          No users are available to assign as incident owners.
        </div>
      ) : null}

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
