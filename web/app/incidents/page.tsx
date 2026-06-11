'use client';

import { useEffect, useState } from 'react';

import { getIncidents, getUsers } from '@/lib/api';
import type { AdminUser, IncidentDetail, IncidentListItem } from '@/lib/types';
import IncidentQuickCreateButton from '@/components/incidents/IncidentQuickCreateButton';
import IncidentList from '@/components/incidents/IncidentList';
import { useToast } from '@/components/ui/ToastProvider';
import PageHeader from '@/components/ui/PageHeader';
import Button from '@/components/ui/Button';

export default function IncidentsPage() {
  const [incidents, setIncidents] = useState<IncidentListItem[]>([]);
  const [users, setUsers] = useState<AdminUser[]>([]);
  const [loading, setLoading] = useState(true);
  const [usersLoading, setUsersLoading] = useState(true);
  const [error, setError] = useState('');
  const [usersError, setUsersError] = useState('');
  const { showToast } = useToast();

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
    showToast('Incident created successfully', 'success');
  };

  return (
    <div className="space-y-6">
      <PageHeader
        title="Incidents"
        subtitle="Coordinate customer-impacting issues."
        action={
          <IncidentQuickCreateButton
            users={users}
            onCreated={handleCreated}
            disabled={usersLoading || Boolean(usersError) || users.length === 0}
          />
        }
      />

      {usersLoading ? (
        <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 px-4 py-3 text-sm text-slate-500">
          Loading users for incident ownership...
        </div>
      ) : usersError ? (
        <div className="flex items-center justify-between gap-3 rounded-xl border border-rose-500/20 bg-rose-500/10 px-4 py-3 text-sm text-rose-300">
          <span>{usersError}</span>
          <Button variant="ghost" size="xs" onClick={loadUsers}>
            Retry
          </Button>
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
        <div className="flex items-center justify-between gap-3 rounded-xl border border-rose-500/20 bg-rose-500/10 px-4 py-3 text-sm text-rose-300">
          <span>{error}</span>
          <Button variant="ghost" size="xs" onClick={loadIncidents}>
            Retry
          </Button>
        </div>
      ) : (
        <IncidentList incidents={incidents} />
      )}

    </div>
  );
}
