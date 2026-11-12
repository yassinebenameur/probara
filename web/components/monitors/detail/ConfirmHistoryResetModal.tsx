'use client';

import { AlertTriangle } from 'lucide-react';
import Callout from '@/components/ui/Callout';
import Input from '@/components/ui/Input';
import Button from '@/components/ui/Button';
import Dialog from '@/components/ui/Dialog';

export default function ConfirmHistoryResetModal({
  monitorName,
  isGroup,
  confirmationText,
  onConfirmationTextChange,
  onClose,
  onConfirm,
  loading,
}: {
  monitorName: string;
  isGroup: boolean;
  confirmationText: string;
  onConfirmationTextChange: (value: string) => void;
  onClose: () => void;
  onConfirm: () => void;
  loading: boolean;
}) {
  const expectedText = 'CLEAR';
  const canConfirm = confirmationText.trim().toUpperCase() === expectedText && !loading;

  return (
    <Dialog
      onClose={onClose}
      busy={loading}
      tone="danger"
      title="Clear monitor history"
      description={
        isGroup
          ? `This will permanently remove checks, alerts, and SLA history for ${monitorName} and every monitor inside that group.`
          : `This will permanently remove checks, alerts, and SLA history for ${monitorName}, but keep the monitor itself.`
      }
      icon={
        <div className="flex h-9 w-9 items-center justify-center rounded-full bg-rose-500/[0.13] text-rose-400">
          <AlertTriangle className="h-[18px] w-[18px]" strokeWidth={1.75} aria-hidden="true" />
        </div>
      }
      footer={
        <>
          <Button type="button" variant="ghost" onClick={onClose} disabled={loading}>
            Cancel
          </Button>
          <Button type="button" variant="danger" onClick={onConfirm} disabled={!canConfirm} loading={loading}>
            {loading ? 'Clearing…' : isGroup ? 'Clear group history' : 'Clear history'}
          </Button>
        </>
      }
    >
      <Callout tone="danger" title="This cannot be undone." live="off">
        Dashboard failures, monitor analytics, and public status page history will rebuild only from future checks.
      </Callout>

      {/* Enter confirms once the phrase matches. */}
      <form
        onSubmit={(event) => {
          event.preventDefault();
          if (canConfirm) onConfirm();
        }}
        noValidate
      >
        <Input
          label={`Type ${expectedText} to confirm`}
          type="text"
          value={confirmationText}
          onChange={(event) => onConfirmationTextChange(event.target.value)}
          placeholder={expectedText}
          autoFocus
          autoComplete="off"
          disabled={loading}
        />
      </form>
    </Dialog>
  );
}
