'use client';

import { ReactNode } from 'react';
import { AlertTriangle } from 'lucide-react';

import Button from '@/components/ui/Button';
import Dialog from '@/components/ui/Dialog';

interface ConfirmDialogProps {
  open: boolean;
  title: string;
  description?: ReactNode;
  confirmLabel?: string;
  cancelLabel?: string;
  tone?: 'danger' | 'default';
  loading?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}

export default function ConfirmDialog({
  open,
  title,
  description,
  confirmLabel = 'Confirm',
  cancelLabel = 'Cancel',
  tone = 'danger',
  loading = false,
  onConfirm,
  onCancel,
}: ConfirmDialogProps) {
  return (
    <Dialog
      open={open}
      onClose={onCancel}
      title={title}
      description={description}
      tone={tone}
      size="sm"
      busy={loading}
      showClose={false}
      icon={
        tone === 'danger' ? (
          <div className="flex h-9 w-9 items-center justify-center rounded-full bg-rose-500/[0.13] text-rose-400">
            <AlertTriangle className="h-[18px] w-[18px]" strokeWidth={1.75} aria-hidden="true" />
          </div>
        ) : undefined
      }
      footer={
        <>
          <Button variant="ghost" onClick={onCancel} disabled={loading}>
            {cancelLabel}
          </Button>
          <Button
            autoFocus
            variant={tone === 'danger' ? 'danger' : 'accent'}
            onClick={onConfirm}
            loading={loading}
          >
            {confirmLabel}
          </Button>
        </>
      }
    />
  );
}
