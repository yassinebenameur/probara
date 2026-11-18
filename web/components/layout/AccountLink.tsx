'use client';

import Link from 'next/link';
import { usePathname } from 'next/navigation';
import { UserRound } from 'lucide-react';
import { FOCUS_RING, FOCUS_RING_INSET } from '@/components/ui/focus';
import { useTooltip } from '@/components/ui/Tooltip';

/**
 * The sidebar footer's way to the signed-in user's account page (profile,
 * sessions). An icon beside Sign out, sized like the theme toggle; hidden in
 * API-key mode, which has no account.
 */
export default function AccountLink({ rail, onNavigate }: { rail: boolean; onNavigate: () => void }) {
  const pathname = usePathname();
  const active = pathname === '/account';
  const tip = useTooltip<HTMLAnchorElement>({ label: 'Account', disabled: !rail });
  return (
    <>
      <Link
        ref={tip.ref}
        {...tip.triggerProps}
        href="/account"
        onClick={onNavigate}
        title={rail ? undefined : 'Account'}
        aria-label="Account"
        aria-current={active ? 'page' : undefined}
        className={`flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-lg transition-colors hover:bg-well-hover hover:text-slate-100 active:bg-well ${
          active ? 'bg-nav-selected text-white' : 'text-slate-400'
        } ${rail ? `!h-10 !w-10 ${FOCUS_RING_INSET}` : FOCUS_RING}`}
      >
        <UserRound className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />
      </Link>
      {tip.tooltip}
    </>
  );
}
