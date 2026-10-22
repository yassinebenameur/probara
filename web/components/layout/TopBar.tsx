'use client';

import Link from 'next/link';
import { Menu } from 'lucide-react';
import BrandMark from '@/components/ui/BrandMark';
import { ICON_BUTTON_CLASS } from '@/components/ui/IconButton';
import FleetHealth from './FleetHealth';

/**
 * Small-screen bar: opens the sidebar drawer. From md up the sidebar is always
 * on screen and carries everything this bar would, so the bar is hidden.
 */
export default function TopBar({
  onOpenNav,
  openAlerts,
}: {
  onOpenNav: () => void;
  openAlerts: number | null;
}) {
  return (
    <header className="glass-shell sticky top-3 z-30 mx-3 mt-3 flex h-12 items-center gap-2 px-2 md:hidden">
      <button type="button" onClick={onOpenNav} aria-label="Open navigation" className={ICON_BUTTON_CLASS}>
        <Menu strokeWidth={1.75} />
      </button>
      <Link href="/" className="flex items-center gap-2">
        <BrandMark size={24} />
        <span className="text-sm font-semibold tracking-tight text-white">Probara</span>
      </Link>
      <div className="ml-auto">
        <FleetHealth openAlerts={openAlerts} compact />
      </div>
    </header>
  );
}
