import type { ReactNode } from 'react';
import BrandMark from '@/components/ui/BrandMark';

/**
 * The centred card the pages outside the app shell share (sign-in steps and
 * the emailed-link pages): the brand mark, a kicker, a title and the body.
 */
export default function AuthCard({
  kicker,
  title,
  subtitle,
  children,
}: {
  kicker?: string;
  title: ReactNode;
  subtitle?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className="flex min-h-screen items-center justify-center px-4 py-10">
      <div className="glass-overlay w-full max-w-md p-8">
        <div className="mb-6 flex justify-center">
          <BrandMark size={40} />
        </div>
        <div className="mb-6 text-center">
          {kicker && <p className="text-xs font-medium uppercase tracking-wide text-slate-400">{kicker}</p>}
          <h1 className="mt-1 break-words text-title font-semibold tracking-tight text-white">{title}</h1>
          {subtitle && <p className="mt-2 text-sm text-slate-400">{subtitle}</p>}
        </div>
        {children}
      </div>
    </div>
  );
}
