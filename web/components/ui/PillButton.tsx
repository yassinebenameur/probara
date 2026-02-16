interface PillButtonProps {
  children: React.ReactNode;
  active?: boolean;
  onClick?: () => void;
}

export default function PillButton({ children, active = false, onClick }: PillButtonProps) {
  return (
    <button
      onClick={onClick}
      className={`cursor-pointer rounded-full border px-2 py-0.5 text-xs transition-all ${
        active
          ? 'border-[rgba(129,140,248,0.9)] bg-[rgba(79,70,229,0.3)] text-gray-200'
          : 'border-[rgba(255,255,255,0.06)] bg-[rgba(15,23,42,0.9)] text-muted hover:border-[rgba(255,255,255,0.1)] hover:text-gray-300'
      }`}
    >
      {children}
    </button>
  );
}

