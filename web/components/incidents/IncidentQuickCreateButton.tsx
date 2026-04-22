'use client';

import { useState } from 'react';

import type { AdminUser, IncidentDetail } from '@/lib/types';
import IncidentCreateDialog, {
  type IncidentCreateDialogInitialContext,
} from '@/components/incidents/IncidentCreateDialog';

interface IncidentQuickCreateButtonProps {
  users: AdminUser[];
  onCreated: (incident: IncidentDetail) => void;
  initialContext?: IncidentCreateDialogInitialContext;
  disabled?: boolean;
  buttonLabel?: string;
}

export default function IncidentQuickCreateButton({
  users,
  onCreated,
  initialContext,
  disabled = false,
  buttonLabel = 'Create Incident',
}: IncidentQuickCreateButtonProps) {
  const [open, setOpen] = useState(false);

  return (
    <>
      <button
        type="button"
        onClick={() => setOpen(true)}
        className="btn btn-primary btn-sm"
        disabled={disabled}
      >
        <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
        </svg>
        {buttonLabel}
      </button>

      <IncidentCreateDialog
        open={open}
        onClose={() => setOpen(false)}
        onCreated={onCreated}
        users={users}
        initialContext={initialContext}
      />
    </>
  );
}
