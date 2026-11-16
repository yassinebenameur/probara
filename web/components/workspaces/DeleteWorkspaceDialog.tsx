'use client';

import { useState } from 'react';
import { AlertTriangle } from 'lucide-react';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import Dialog from '@/components/ui/Dialog';
import Input from '@/components/ui/Input';
import { errorMessage } from '@/lib/errors';
import type { WorkspaceOverview } from '@/lib/types';

/**
 * The typed confirmation for deleting a workspace: the button arms only once
 * the name is repeated exactly, as the API also requires.
 */
export default function DeleteWorkspaceDialog({
  workspace,
  onConfirm,
  onClose,
}: {
  workspace: WorkspaceOverview;
  onConfirm: (confirmName: string) => Promise<void>;
  onClose: () => void;
}) {
  const [typed, setTyped] = useState('');
  const [error, setError] = useState('');
  const [deleting, setDeleting] = useState(false);
  const canConfirm = typed.trim() === workspace.name && !deleting;

  const confirm = async () => {
    if (!canConfirm) return;
    setDeleting(true);
    setError('');
    try {
      await onConfirm(typed.trim());
    } catch (err) {
      setError(errorMessage(err, 'Failed to delete the workspace'));
      setDeleting(false);
    }
  };

  return (
    <Dialog
      onClose={onClose}
      busy={deleting}
      tone="danger"
      size="sm"
      title="Delete workspace"
      description={`“${workspace.name}” and everything in it will be deleted for good.`}
      icon={
        <div className="flex h-9 w-9 items-center justify-center rounded-full bg-rose-500/[0.13] text-rose-400">
          <AlertTriangle className="h-[18px] w-[18px]" strokeWidth={1.75} aria-hidden="true" />
        </div>
      }
      footer={
        <>
          <Button type="button" variant="ghost" onClick={onClose} disabled={deleting}>
            Cancel
          </Button>
          <Button type="button" variant="danger" onClick={confirm} disabled={!canConfirm} loading={deleting}>
            Delete workspace
          </Button>
        </>
      }
    >
      <Callout tone="danger" title="This cannot be undone." live="off">
        Its monitors stop at once and its status pages go offline. Members lose access, and its API keys,
        agent and push tokens and private locations stop working. Monitors, history, alerts, incidents,
        status pages, channels and SLAs are then erased in the background; there is no way to restore them.
      </Callout>
      {error && (
        <Callout tone="danger" live="assertive">
          {error}
        </Callout>
      )}
      {/* Enter confirms once the name matches. */}
      <form
        noValidate
        onSubmit={(event) => {
          event.preventDefault();
          confirm();
        }}
      >
        <Input
          label={`Type “${workspace.name}” to confirm`}
          value={typed}
          onChange={(event) => setTyped(event.target.value)}
          autoFocus
          autoComplete="off"
          spellCheck={false}
          disabled={deleting}
        />
      </form>
    </Dialog>
  );
}
