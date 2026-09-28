'use client';

import { useEffect, useState } from 'react';

import { getIncidents, getUsers } from '@/lib/api';
import type { AdminUser, IncidentDetail, IncidentListItem } from '@/lib/types';
import IncidentQuickCreateButton from '@/components/incidents/IncidentQuickCreateButton';
import IncidentList from '@/components/incidents/IncidentList';
import { useToast } from '@/components/ui/ToastProvider';
import PageHeader from '@/components/ui/PageHeader';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';

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
        action={
          <IncidentQuickCreateButton
            users={users}
            onCreated={handleCreated}
            disabled={usersLoading || Boolean(usersError) || users.length === 0}
          />
        }
      />

      {usersLoading ? null : usersError ? (
        <Callout
          tone="danger"
          action={<Button variant="subtle" size="xs" onClick={loadUsers}>Retry</Button>}
        >
          {usersError}
        </Callout>
      ) : users.length === 0 ? (
        <Callout tone="warning">No users are available to assign as incident owners.</Callout>
      ) : null}

      {loading ? (
        <div className="glass p-8 text-center text-sm text-slate-500">Loading incidents…</div>
      ) : error ? (
        <Callout
          tone="danger"
          action={<Button variant="subtle" size="xs" onClick={loadIncidents}>Retry</Button>}
        >
          {error}
        </Callout>
      ) : (
        <IncidentList incidents={incidents} />
      )}
    </div>
  );
}
