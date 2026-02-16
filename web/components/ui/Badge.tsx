interface BadgeProps {
  children: React.ReactNode;
  variant?: 'default' | 'success' | 'danger' | 'warning';
  dot?: boolean;
}

export default function Badge({ children, variant = 'default', dot = false }: BadgeProps) {
  const variants = {
    default: 'border-[rgba(148,163,184,0.4)] bg-[rgba(15,23,42,0.95)] text-gray-200',
    success: 'border-[rgba(34,197,94,0.6)] bg-[rgba(22,101,52,0.6)] text-[#bbf7d0]',
    danger: 'border-[rgba(248,113,113,0.6)] bg-[rgba(127,29,29,0.6)] text-[#fecaca]',
    warning: 'border-[rgba(250,204,21,0.6)] bg-[rgba(161,98,7,0.6)] text-[#fef3c7]',
  };

  return (
    <span
      className={`inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-[0.7rem] ${variants[variant]}`}
    >
      {dot && (
        <span
          className="h-[7px] w-[7px] rounded-full bg-current"
          style={{
            boxShadow: variant === 'success' ? '0 0 0 4px rgba(34,197,94,0.3)' : undefined,
          }}
        />
      )}
      {children}
    </span>
  );
}

