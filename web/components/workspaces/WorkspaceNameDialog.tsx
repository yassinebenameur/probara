'use client';

import { useId, useState } from 'react';
import Button from '@/components/ui/Button';
import Dialog from '@/components/ui/Dialog';
import Input from '@/components/ui/Input';
import { errorMessage } from '@/lib/errors';

/** The API's limit on a workspace name, in characters. */
export const MAX_WORKSPACE_NAME_LENGTH = 100;

/**
 * Asks for a workspace name, to create a workspace or rename one. onSubmit
 * gets the trimmed name; a rejection (a taken name, say) is shown under the
 * field and keeps the dialog open.
 */
export default function WorkspaceNameDialog({
  title,
  description,
  submitLabel,
  initialName = '',
  onSubmit,
  onClose,
}: {
  title: string;
  description?: string;
  submitLabel: string;
  initialName?: string;
  onSubmit: (name: string) => Promise<void>;
  onClose: () => void;
}) {
  const formId = useId();
  const [name, setName] = useState(initialName);
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);
  const trimmed = name.trim();
  const unchanged = initialName !== '' && trimmed === initialName;

  const submit = async () => {
    if (!trimmed || unchanged || saving) return;
    setSaving(true);
    setError('');
    try {
      await onSubmit(trimmed);
    } catch (err) {
      setError(errorMessage(err, 'Failed to save the workspace'));
      setSaving(false);
    }
  };

  return (
    <Dialog
      onClose={onClose}
      busy={saving}
      size="sm"
      title={title}
      description={description}
      footer={
        <>
          <Button type="button" variant="ghost" onClick={onClose} disabled={saving}>
            Cancel
          </Button>
          <Button type="submit" form={formId} variant="accent" loading={saving} disabled={!trimmed || unchanged}>
            {submitLabel}
          </Button>
        </>
      }
    >
      <form
        id={formId}
        noValidate
        onSubmit={(event) => {
          event.preventDefault();
          submit();
        }}
      >
        <Input
          label="Name"
          value={name}
          onChange={(event) => {
            setName(event.target.value);
            setError('');
          }}
          maxLength={MAX_WORKSPACE_NAME_LENGTH}
          autoFocus
          autoComplete="off"
          disabled={saving}
          error={error || undefined}
        />
      </form>
    </Dialog>
  );
}
