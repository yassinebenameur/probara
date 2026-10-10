import { ReactNode } from 'react';

interface FormCardProps {
  children: ReactNode;
  className?: string;
}

export default function FormCard({ children, className = '' }: FormCardProps) {
  return (
    <div className={`glass p-6 ${className}`.trim()}>
      {children}
    </div>
  );
}
