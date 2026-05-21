import { ReactNode } from 'react';
import Pill, { PillTone } from './Pill';

interface BadgeProps {
  children: ReactNode;
  variant?: 'default' | 'success' | 'danger' | 'warning';
  dot?: boolean;
}

const TONE: Record<NonNullable<BadgeProps['variant']>, PillTone> = {
  default: 'neutral',
  success: 'success',
  danger: 'danger',
  warning: 'warning',
};

/**
 * @deprecated Use `<Pill tone="..." dot />` from `components/ui/Pill` directly.
 * Kept as a thin shim during the design-consistency migration.
 */
export default function Badge({ children, variant = 'default', dot = false }: BadgeProps) {
  return (
    <Pill tone={TONE[variant]} dot={dot} size="xs">
      {children}
    </Pill>
  );
}
