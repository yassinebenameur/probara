import type { ReactNode } from 'react';
import BrandMark from '@/components/ui/BrandMark';

/** The centered card the sign-in and account-link pages share (no app shell). */
export default function AuthCard({ title, subtitle, children }: { title: string; subtitle?: ReactNode; children: ReactNode }) {
  return (
    <div className="flex min-h-screen items-center justify-center px-4 py-10">
      <div className="glass-overlay w-full max-w-md p-8">
        <div className="mb-6 flex justify-center">
          <BrandMark size={48} />
        </div>
        <div className="mb-8 text-center">
          <h1 className="text-2xl font-semibold tracking-tight text-white">{title}</h1>
          {subtitle && <p className="mt-2 text-sm text-slate-400">{subtitle}</p>}
        </div>
        {children}
      </div>
    </div>
  );
}
