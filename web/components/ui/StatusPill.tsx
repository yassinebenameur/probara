import Pill, { PillTone } from './Pill';

interface StatusPillProps {
  status: 'up' | 'down' | 'degraded' | 'unknown' | 'paused';
  label?: string;
}

const TONE: Record<StatusPillProps['status'], PillTone> = {
  up: 'success',
  down: 'danger',
  degraded: 'warning',
  unknown: 'neutral',
  paused: 'neutral',
};

const DEFAULT_LABEL: Record<StatusPillProps['status'], string> = {
  up: 'Up',
  down: 'Down',
  degraded: 'Degraded',
  unknown: 'Unknown',
  paused: 'Paused',
};

/**
 * @deprecated Use `<Pill tone="success|danger|warning|neutral" dot />` directly.
 */
export default function StatusPill({ status, label }: StatusPillProps) {
  return (
    <Pill tone={TONE[status]} dot size="sm">
      {label ?? DEFAULT_LABEL[status]}
    </Pill>
  );
}
