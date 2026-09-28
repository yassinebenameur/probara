'use client';

import { ReactNode } from 'react';
import Link from 'next/link';
import Button from './Button';

interface IconButtonProps {
  icon: ReactNode;
  /** Tooltip and accessible name — the button has no visible text. */
  label: string;
  onClick?: () => void;
  href?: string;
  danger?: boolean;
  disabled?: boolean;
  loading?: boolean;
}

// Row action for tables and lists: icon only, named by its tooltip. Use up to
// three per row; beyond that, put the actions in a RowMenu.
export default function IconButton({ icon, label, onClick, href, danger, disabled, loading }: IconButtonProps) {
  const className = danger ? 'hover:!bg-rose-500/10 hover:!text-rose-300' : '';
  if (href) {
    return (
      <Button variant="subtle" size="xs" icon={icon} className={className} asChild>
        <Link href={href} title={label} aria-label={label}>
          <span className="sr-only">{label}</span>
        </Link>
      </Button>
    );
  }
  return (
    <Button
      variant="subtle"
      size="xs"
      icon={icon}
      className={className}
      onClick={onClick}
      disabled={disabled}
      loading={loading}
      title={label}
      aria-label={label}
    >
      <span className="sr-only">{label}</span>
    </Button>
  );
}
