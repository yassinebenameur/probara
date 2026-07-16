import { ReactNode } from 'react';

interface FormCardProps {
  children: ReactNode;
  className?: string;
}

export default function FormCard({ children, className = '' }: FormCardProps) {
  return (
    <div className={`rounded-xl border border-white/[0.06] bg-slate-900/50 p-6 ${className}`.trim()}>
      {children}
    </div>
  );
}
