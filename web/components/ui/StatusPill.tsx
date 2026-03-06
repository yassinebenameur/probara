interface StatusPillProps {
  status: 'up' | 'down' | 'degraded' | 'unknown' | 'paused';
  label?: string;
}

export default function StatusPill({ status, label }: StatusPillProps) {
  const configs = {
    up: {
      border: 'rgba(34, 197, 94, 0.7)',
      bg: 'rgba(21, 128, 61, 0.5)',
      text: '#bbf7d0',
      label: label || 'Up',
    },
    down: {
      border: 'rgba(239, 68, 68, 0.7)',
      bg: 'rgba(127, 29, 29, 0.7)',
      text: '#fecaca',
      label: label || 'Down',
    },
    degraded: {
      border: 'rgba(251, 191, 36, 0.7)',
      bg: 'rgba(161, 98, 7, 0.5)',
      text: '#fef3c7',
      label: label || 'Degraded',
    },
    paused: {
      border: 'rgba(148, 163, 184, 0.6)',
      bg: 'rgba(51, 65, 85, 0.5)',
      text: '#cbd5e1',
      label: label || 'Paused',
    },
    unknown: {
      border: 'rgba(148, 163, 184, 0.6)',
      bg: 'rgba(51, 65, 85, 0.5)',
      text: '#cbd5e1',
      label: label || 'Unknown',
    },
  };

  const config = configs[status];

  return (
    <span
      className="inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[0.72rem]"
      style={{
        border: `1px solid ${config.border}`,
        background: config.bg,
        color: config.text,
      }}
    >
      <span
        className="h-[7px] w-[7px] rounded-full"
        style={{ background: 'currentColor' }}
      />
      {config.label}
    </span>
  );
}
