'use client';

import { useState } from 'react';
import { Plus } from 'lucide-react';

import type { AdminUser, IncidentDetail } from '@/lib/types';
import IncidentCreateDialog, {
  type IncidentCreateDialogInitialContext,
} from '@/components/incidents/IncidentCreateDialog';
import Button from '@/components/ui/Button';

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
  buttonLabel = 'Create incident',
}: IncidentQuickCreateButtonProps) {
  const [open, setOpen] = useState(false);

  return (
    <>
      <Button
        variant="ghost"
        size="sm"
        icon={<Plus strokeWidth={1.75} />}
        onClick={() => setOpen(true)}
        disabled={disabled}
      >
        {buttonLabel}
      </Button>

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
