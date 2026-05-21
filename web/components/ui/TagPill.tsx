import { ReactNode } from 'react';
import Pill, { PillTone } from './Pill';

interface TagPillProps {
  children: ReactNode;
  variant?: 'default' | 'latency';
}

const TONE: Record<NonNullable<TagPillProps['variant']>, PillTone> = {
  default: 'neutral',
  latency: 'info',
};

/**
 * @deprecated Use `<Pill tone="neutral|info" size="xs" />` directly.
 */
export default function TagPill({ children, variant = 'default' }: TagPillProps) {
  return (
    <Pill tone={TONE[variant]} size="xs">
      {children}
    </Pill>
  );
}
