interface TagPillProps {
  children: React.ReactNode;
  variant?: 'default' | 'latency';
}

export default function TagPill({ children, variant = 'default' }: TagPillProps) {
  if (variant === 'latency') {
    return (
      <span className="rounded-full border border-[rgba(56,189,248,0.6)] bg-[rgba(15,23,42,0.95)] px-1.5 py-0.5 text-[0.72rem] text-[#e0f2fe]">
        {children}
      </span>
    );
  }

  return (
    <span className="rounded-full border border-[rgba(148,163,184,0.6)] bg-[rgba(15,23,42,0.95)] px-1.5 py-0.5 text-[0.7rem] text-gray-200">
      {children}
    </span>
  );
}

