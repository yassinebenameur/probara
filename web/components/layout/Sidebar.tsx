'use client';

import Link from 'next/link';
import { usePathname, useRouter } from 'next/navigation';
import { useState, useEffect, useCallback, useRef } from 'react';
import {
  LayoutDashboard,
  Activity,
  FileText,
  AlertTriangle,
  Users,
  Settings,
  LogOut,
  Siren,
  Workflow,
  Wrench,
  MapPin,
  ScrollText,
  Target,
  X,
  PanelLeftClose,
  PanelLeftOpen,
} from 'lucide-react';
import { getIncidents } from '@/lib/api';
import { clearApiKey, hasApiKey } from '@/lib/auth';
import { TENANT_CHANGED_EVENT, clearSelectedTenantId } from '@/lib/tenant';
import { useCurrentUser } from '@/components/providers/CurrentUserProvider';
import BrandMark from '@/components/ui/BrandMark';
import ThemeToggle from '@/components/ui/ThemeToggle';
import { ICON_BUTTON_CLASS } from '@/components/ui/IconButton';
import { FOCUS_RING, FOCUS_RING_INSET } from '@/components/ui/focus';
import { useTooltip } from '@/components/ui/Tooltip';
import TenantSwitcher from './TenantSwitcher';
import FleetHealth, { CountBadge } from './FleetHealth';
import { RAIL_ROW_MOTION, railGutter, railLabel, railRowPadding } from './rail';
import { useSidebarCollapsed } from './useSidebarCollapsed';

type NavItem = {
  name: string;
  href: string;
  icon: React.ElementType;
  // Other routes that live under this entry (tabs folded into its page).
  also?: string[];
  showOpenIncidents?: boolean;
};

// Groups are separated by space alone; the names are only for keys.
const navGroups: { key: string; items: NavItem[] }[] = [
  {
    key: 'overview',
    items: [{ name: 'Dashboard', href: '/', icon: LayoutDashboard }],
  },
  {
    key: 'monitoring',
    items: [
      { name: 'Monitors', href: '/monitors', icon: Activity },
      { name: 'Locations', href: '/locations', icon: MapPin, also: ['/mesh'] },
      { name: 'Dependencies', href: '/dependencies', icon: Workflow },
      { name: 'Maintenance', href: '/maintenance', icon: Wrench },
      { name: 'Status pages', href: '/status-pages', icon: FileText },
      { name: 'SLAs', href: '/slas', icon: Target },
    ],
  },
  {
    key: 'alerting',
    items: [
      { name: 'Alerts', href: '/alerts', icon: AlertTriangle },
      { name: 'Incidents', href: '/incidents', icon: Siren, showOpenIncidents: true },
    ],
  },
  {
    key: 'admin',
    items: [
      { name: 'Users', href: '/users', icon: Users },
      { name: 'Audit log', href: '/audit', icon: ScrollText },
      { name: 'Settings', href: '/settings', icon: Settings, also: ['/alert-channels'] },
    ],
  },
];

// The toggle's aria-controls target.
const SIDEBAR_ID = 'app-sidebar';

// "[" toggles the rail unless the key is meant for something else: typing in
// a field, a Cmd/Ctrl chord (Cmd+[ is browser Back), a held key's auto-repeat,
// or an open popup — a modal, or a menu whose fixed position and focus would
// be orphaned when the sidebar resizes or the rail's workspace menu unmounts.
// AltGr, or Alt on a key other than the "[" key itself, is how layouts such as
// FR, DE and macOS DE type "[" (event.key is already "["), so those count;
// Alt on the physical "[" key is a chord and does not.
//
// Known WCAG 2.1.4 (character key shortcuts) exception: a bare key, with no
// setting to turn it off or remap it. The header toggle is the equivalent
// control, and the key is inert in every text field.
function isShortcutTarget(event: KeyboardEvent) {
  const altGraph = event.getModifierState?.('AltGraph') ?? false;
  const layoutComposed = altGraph || event.code !== 'BracketLeft';
  if (
    event.key !== '[' ||
    event.repeat ||
    event.metaKey ||
    (event.ctrlKey && !altGraph) ||
    (event.altKey && !layoutComposed) ||
    event.defaultPrevented ||
    event.isComposing
  ) {
    return false;
  }
  const target = event.target;
  if (
    target instanceof HTMLElement &&
    (target.isContentEditable ||
      target.closest(
        'input, textarea, select, [contenteditable]:not([contenteditable="false"]), [role="menu"], [role="listbox"], [role="dialog"], [role="combobox"]'
      ))
  ) {
    return false;
  }
  return !document.querySelector('[aria-modal="true"], [role="menu"]');
}

// A CSS <time> in ms. The token is written "240ms" but the CSS build may
// minify it to ".24s", so the unit matters.
function cssDurationMs(value: string) {
  const n = parseFloat(value);
  if (!Number.isFinite(n)) return 0;
  return value.trim().endsWith('ms') ? n : n * 1000;
}

const FOOTER_FOCUSABLE = 'a[href], button:not([disabled]), select:not([disabled])';

/**
 * The footer's layout lags the rail by the width transition. Its two layouts
 * differ in height and width (workspace select and a Sign out row, or a
 * stack of 40px icons), so rather than squeeze one into the other while the
 * width animates, the footer fades out, swaps once the width has settled
 * (--dur-slow, 0 under reduced motion), and fades back in. Focus inside it
 * follows the swap to the same control.
 */
function useSettledFooter(rail: boolean, footerRef: React.RefObject<HTMLDivElement | null>) {
  const [footerRail, setFooterRail] = useState(rail);
  const pendingFocus = useRef(-1);
  useEffect(() => {
    if (footerRail === rail) return;
    const ms = cssDurationMs(getComputedStyle(document.documentElement).getPropertyValue('--dur-slow'));
    const timer = window.setTimeout(() => {
      const focusables = Array.from(footerRef.current?.querySelectorAll<HTMLElement>(FOOTER_FOCUSABLE) ?? []);
      pendingFocus.current = focusables.findIndex((el) => el === document.activeElement);
      setFooterRail(rail);
    }, ms);
    return () => window.clearTimeout(timer);
  }, [rail, footerRail, footerRef]);
  useEffect(() => {
    const index = pendingFocus.current;
    pendingFocus.current = -1;
    const footer = footerRef.current;
    if (index < 0 || !footer || footer.contains(document.activeElement)) return;
    footer.querySelectorAll<HTMLElement>(FOOTER_FOCUSABLE)[index]?.focus({ preventScroll: true });
  }, [footerRail, footerRef]);
  return { footerRail, footerSettling: footerRail !== rail };
}

interface SidebarProps {
  /** Drawer state below md; ignored from md up, where the sidebar is always shown. */
  open: boolean;
  onClose: () => void;
  openAlerts: number | null;
}

/**
 * The shell's one piece of chrome: a floating glass sidebar on desktop, a
 * drawer on small screens. It is a single instance on every breakpoint, so
 * the tenant bootstrap in TenantSwitcher and the counts here load once.
 *
 * From md the sidebar is expanded or collapsed to a 64px icon rail (header
 * toggle or "["; useSidebarCollapsed). The width is CSS keyed on
 * html[data-sidebar] (--sidebar-width / --sidebar-offset, app/globals.css);
 * every row keeps one structure in both states and only fades its label and
 * eases its padding (./rail.ts), so the same elements stay mounted and
 * nothing reflows while the width animates. The header is one fixed-height
 * row in both states (in the rail the toggle overlays the brand mark), so
 * nothing below it moves vertically; the footer swaps layout once the width
 * settles (useSettledFooter). Below md the drawer is always expanded.
 */
export default function Sidebar({ open, onClose, openAlerts }: SidebarProps) {
  const pathname = usePathname();
  const router = useRouter();
  const [apiKeyMode, setApiKeyMode] = useState(true);
  const [openIncidents, setOpenIncidents] = useState(0);
  const asideRef = useRef<HTMLElement>(null);
  const closeButtonRef = useRef<HTMLButtonElement>(null);
  const footerRef = useRef<HTMLDivElement>(null);
  const [collapsed, setCollapsed] = useSidebarCollapsed();

  // Below md the drawer is only translated off-screen; inert keeps its links
  // out of the tab order (and AT) while it is closed. From md it is the rail.
  // The shell first renders on the client (AuthGuard holds it back until the
  // session check), so the initial value can read the viewport directly.
  const [isDrawer, setIsDrawer] = useState(
    () => typeof window !== 'undefined' && window.matchMedia('(max-width: 767px)').matches
  );
  const rail = collapsed && !isDrawer;
  const { footerRail, footerSettling } = useSettledFooter(rail, footerRef);

  const toggleTip = useTooltip<HTMLButtonElement>({
    label: collapsed ? 'Expand sidebar' : 'Collapse sidebar',
    shortcut: '[',
  });
  const hideToggleTip = toggleTip.hide;
  const toggleCollapsed = useCallback(() => {
    hideToggleTip();
    setCollapsed(!collapsed);
  }, [collapsed, setCollapsed, hideToggleTip]);

  useEffect(() => {
    if (isDrawer) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (!isShortcutTarget(event)) return;
      event.preventDefault();
      toggleCollapsed();
    };
    document.addEventListener('keydown', onKeyDown);
    return () => document.removeEventListener('keydown', onKeyDown);
  }, [isDrawer, toggleCollapsed]);
  useEffect(() => {
    const query = window.matchMedia('(max-width: 767px)');
    const update = () => setIsDrawer(query.matches);
    update();
    query.addEventListener('change', update);
    return () => query.removeEventListener('change', update);
  }, []);

  // Widening past md while the drawer is open turns it back into the rail;
  // close it so the shell does not keep the page inert behind a drawer that
  // is no longer one.
  useEffect(() => {
    if (!isDrawer && open) onClose();
  }, [isDrawer, open, onClose]);

  // Open drawer: focus moves in, Tab stays inside, and closing hands focus
  // back to whatever opened it (the TopBar button).
  useEffect(() => {
    if (!open || !isDrawer) return;
    const aside = asideRef.current;
    const opener = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const frame = requestAnimationFrame(() => closeButtonRef.current?.focus());
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Tab' || !aside) return;
      const items = Array.from(
        aside.querySelectorAll<HTMLElement>('a[href], button:not([disabled]), select:not([disabled]), input:not([disabled])')
      ).filter((el) => el.getClientRects().length > 0);
      if (items.length === 0) return;
      const first = items[0];
      const last = items[items.length - 1];
      const active = document.activeElement;
      if (!aside.contains(active)) {
        event.preventDefault();
        (event.shiftKey ? last : first).focus();
      } else if (event.shiftKey && active === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && active === last) {
        event.preventDefault();
        first.focus();
      }
    };
    document.addEventListener('keydown', onKeyDown);
    return () => {
      cancelAnimationFrame(frame);
      document.removeEventListener('keydown', onKeyDown);
      if (opener?.isConnected && !aside?.contains(opener)) opener.focus({ preventScroll: true });
    };
  }, [open, isDrawer]);

  const handleLogout = () => {
    if (hasApiKey()) {
      clearApiKey();
      router.push('/connect');
      return;
    }

    fetch('/api/v1/auth/logout', {
      method: 'POST',
      credentials: 'include',
    }).finally(() => {
      clearSelectedTenantId();
      router.push('/login');
    });
  };

  const loadCounts = useCallback(async () => {
    try {
      const incidentsRes = await getIncidents({ page_size: 1 });
      setOpenIncidents(incidentsRes.open_total || 0);
    } catch (error) {
      console.error('Failed to load counts:', error);
    }
  }, []);

  useEffect(() => {
    loadCounts();
  }, [loadCounts]);

  useEffect(() => {
    setApiKeyMode(hasApiKey());
  }, []);

  useEffect(() => {
    const handler = () => loadCounts();
    window.addEventListener(TENANT_CHANGED_EVENT, handler);
    return () => window.removeEventListener(TENANT_CHANGED_EVENT, handler);
  }, [loadCounts]);

  useEffect(() => {
    if (!open) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [open, onClose]);

  const { isSuperadmin, role, loading: userLoading } = useCurrentUser();

  // /users is superadmin-only; /audit needs tenant admin (or superadmin).
  // While identity is loading, hide gated items rather than flashing them.
  const canSeeUsers = !apiKeyMode && !userLoading && isSuperadmin;
  const canSeeAudit = !apiKeyMode && !userLoading && (isSuperadmin || role === 'admin');

  const visibleGroups = navGroups
    .map((group) => ({
      ...group,
      items: group.items.filter((item) => {
        if (item.href === '/users') return canSeeUsers;
        if (item.href === '/audit') return canSeeAudit;
        return true;
      }),
    }))
    .filter((group) => group.items.length > 0);

  return (
    <>
      {open && (
        <div className="glass-scrim fixed inset-0 z-40 md:hidden" onClick={onClose} aria-hidden="true" />
      )}
      {/* Below md an open drawer is a modal dialog (focus is trapped above and
          Layout makes the page inert); from md it is a plain landmark-free
          wrapper around the named <nav>. */}
      <aside
        ref={asideRef}
        id={SIDEBAR_ID}
        inert={isDrawer && !open}
        role={isDrawer && open ? 'dialog' : undefined}
        aria-modal={isDrawer && open ? true : undefined}
        aria-label={isDrawer && open ? 'Navigation' : undefined}
        className={`glass-shell fixed inset-y-3 left-3 z-50 flex w-64 flex-col overflow-hidden transition-[transform,width] duration-slow ease-emphasized md:sticky md:inset-auto md:top-3 md:z-auto md:my-3 md:ml-3 md:h-[calc(100vh-1.5rem)] md:w-[var(--sidebar-width)] md:flex-shrink-0 md:translate-x-0 ${
          open ? 'translate-x-0' : '-translate-x-[calc(100%+1rem)]'
        }`}
      >
        {/* One 60px row in both states, so nothing below it moves while the
            width animates. The toggle is a 40×40 box pinned 11px from the
            right edge: top-right when expanded, and in the 64px rail that is
            exactly over the brand mark (17px in from the 1px edge), so it
            rides the closing edge onto the mark. There it stays invisible
            until the header is hovered or the toggle has keyboard focus, when
            the mark gives way to the expand icon. The home link is inert in
            the rail: the toggle covers it and Dashboard is the next row. */}
        <div
          className={`group/header relative flex h-[60px] flex-shrink-0 items-center justify-between pb-3 pr-4 pt-4 [transition:padding_var(--dur-slow)_var(--ease-emphasized)] ${
            rail ? 'pl-[17px]' : 'pl-4'
          }`}
        >
          <Link
            href="/"
            onClick={onClose}
            inert={rail}
            className={`flex min-w-0 items-center gap-2.5 rounded-lg ${FOCUS_RING}`}
          >
            <BrandMark
              size={28}
              className={`flex-shrink-0 transition-opacity ${
                rail
                  ? 'duration-fast group-hover/header:opacity-0 group-has-[:focus-visible]/header:opacity-0'
                  : 'duration-base delay-[var(--dur-fast)]'
              }`}
            />
            <span
              className={`min-w-0 overflow-hidden whitespace-nowrap text-title font-semibold tracking-tight text-white transition-opacity ${
                rail ? 'opacity-0 duration-fast' : 'opacity-100 duration-base delay-[var(--dur-fast)]'
              }`}
            >
              Probara
            </span>
          </Link>
          <button
            ref={toggleTip.ref}
            {...toggleTip.triggerProps}
            type="button"
            onClick={toggleCollapsed}
            aria-label={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
            aria-expanded={!collapsed}
            aria-controls={SIDEBAR_ID}
            // Expanding, the toggle (and the mark it uncovers) fade in a beat
            // late, as the labels do: the width eases out of the rail slowly,
            // so for the first ~100ms the two still overlap.
            className={`absolute right-[11px] top-3 hidden h-10 w-10 items-center justify-center rounded-lg text-slate-400 hover:bg-well-hover hover:text-white active:bg-control-hover md:inline-flex ${
              rail
                ? `transition-[background-color,color,opacity] duration-fast ease-standard opacity-0 focus-visible:opacity-100 group-hover/header:opacity-100 ${FOCUS_RING_INSET}`
                : `[transition:background-color_var(--dur-fast)_var(--ease-standard),color_var(--dur-fast)_var(--ease-standard),opacity_var(--dur-base)_var(--ease-standard)_var(--dur-fast)] ${FOCUS_RING}`
            }`}
          >
            {collapsed ? (
              <PanelLeftOpen className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />
            ) : (
              <PanelLeftClose className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />
            )}
          </button>
          {toggleTip.tooltip}
          <button
            ref={closeButtonRef}
            type="button"
            onClick={onClose}
            aria-label="Close navigation"
            className={`${ICON_BUTTON_CLASS} md:hidden`}
          >
            <X strokeWidth={1.75} />
          </button>
        </div>

        <div className={railGutter(rail)}>
          <div className="relative">
            <FleetHealth openAlerts={openAlerts} onNavigate={onClose} rail={rail} />
            {/* The same 16px break and hairline as between nav groups. */}
            <div
              aria-hidden="true"
              className={`pointer-events-none absolute left-2 right-2 top-full mt-2 h-px bg-divider transition-opacity duration-base ${
                rail ? 'opacity-100' : 'opacity-0'
              }`}
            />
          </div>
        </div>

        {/* In the rail the scrollbar is hidden (wheel, touch and keyboard
            focus still scroll): a classic scrollbar would eat into the 11px
            gutter and push the 40px rows off-centre. */}
        <nav
          aria-label="Main"
          className={`mt-4 flex-1 space-y-4 overflow-y-auto overflow-x-hidden pb-3 ${railGutter(rail)} ${
            rail ? '[scrollbar-width:none] [&::-webkit-scrollbar]:hidden' : 'dashboard-scroll'
          }`}
        >
          {visibleGroups.map((group, index) => (
            <div key={group.key} className="relative space-y-0.5">
              {/* The rail has no space to spare for whitespace alone: a
                  hairline marks each group break instead. */}
              {index > 0 && (
                <div
                  aria-hidden="true"
                  className={`pointer-events-none absolute -top-2 left-2 right-2 h-px bg-divider transition-opacity duration-base ${
                    rail ? 'opacity-100' : 'opacity-0'
                  }`}
                />
              )}
              {group.items.map((item) => {
                const isActive =
                  pathname === item.href ||
                  [item.href, ...(item.also ?? [])].some(
                    (prefix) => prefix !== '/' && pathname?.startsWith(prefix)
                  );
                return (
                  <NavLink
                    key={item.name}
                    item={item}
                    active={isActive}
                    count={item.showOpenIncidents ? openIncidents : 0}
                    rail={rail}
                    onNavigate={onClose}
                  />
                );
              })}
            </div>
          ))}
        </nav>

        <div ref={footerRef} className={`border-t border-divider py-3 ${railGutter(rail)}`}>
          {/* Expanded content keeps its full width (min-w) so a closing rail
              clips it instead of squeezing it while it fades. */}
          <div
            className={`transition-opacity ${footerSettling ? 'opacity-0 duration-fast' : 'opacity-100 duration-base'} ${
              footerRail ? 'flex flex-col items-start gap-0.5' : 'min-w-[214px] space-y-2'
            }`}
          >
            <TenantSwitcher rail={footerRail} />
            <div className={`flex ${footerRail ? 'flex-col items-start gap-0.5' : 'items-center gap-1'}`}>
              <SignOutButton rail={footerRail} onClick={handleLogout} />
              <ThemeToggle tooltip={footerRail} className={footerRail ? '!h-10 !w-10' : ''} />
            </div>
          </div>
        </div>
      </aside>
    </>
  );
}

function NavLink({
  item,
  active,
  count,
  rail,
  onNavigate,
}: {
  item: NavItem;
  active: boolean;
  count: number;
  rail: boolean;
  onNavigate: () => void;
}) {
  const tip = useTooltip<HTMLAnchorElement>({
    label: count > 0 ? `${item.name} · ${count} open` : item.name,
    disabled: !rail,
  });
  const Icon = item.icon;
  return (
    <>
      <Link
        ref={tip.ref}
        {...tip.triggerProps}
        href={item.href}
        onClick={onNavigate}
        aria-current={active ? 'page' : undefined}
        // Active = a neutral filled step above hover (bg-nav-selected; white
        // segment fill would vanish on the light shell). No rail, no orange:
        // orange means "act", and the user is
        // already here. The label stays in the DOM in the collapsed rail (it
        // is the link's accessible name); only its paint fades.
        className={`group flex items-center gap-2.5 overflow-hidden whitespace-nowrap rounded-lg text-ui ${RAIL_ROW_MOTION} ${railRowPadding(rail)} ${FOCUS_RING_INSET} ${
          active
            ? 'bg-nav-selected font-medium text-white'
            : 'text-slate-400 hover:bg-well hover:text-slate-100 active:bg-well-hover'
        }`}
      >
        <span className="relative flex h-4 w-4 flex-shrink-0">
          <Icon
            className={`h-4 w-4 transition-colors ${active ? 'text-white' : 'text-slate-500 group-hover:text-slate-200'}`}
            strokeWidth={1.75}
            aria-hidden="true"
          />
          {rail && count > 0 && <CountBadge count={count} overlay />}
        </span>
        <span className={railLabel(rail)}>{item.name}</span>
        {/* Mounted in both states (clipped by the 40px rail row, faded with
            the label), so the name reads "Incidents 1 open" either way.
            -my-px keeps the 20px badge inside the 18px label line, so the
            row stays 32px expanded and exactly 40px in the rail. */}
        {count > 0 && (
          <span
            className={`-my-px ml-auto flex flex-shrink-0 transition-opacity ${
              rail ? 'opacity-0 duration-fast' : 'opacity-100 duration-base delay-[var(--dur-fast)]'
            }`}
          >
            <CountBadge count={count} srLabel="open" />
          </span>
        )}
      </Link>
      {tip.tooltip}
    </>
  );
}

function SignOutButton({ rail, onClick }: { rail: boolean; onClick: () => void }) {
  const tip = useTooltip<HTMLButtonElement>({ label: 'Sign out', disabled: !rail });
  return (
    <>
      <button
        ref={tip.ref}
        {...tip.triggerProps}
        type="button"
        onClick={onClick}
        // No padding motion: the footer swaps layout while faded out.
        className={`flex items-center gap-2.5 overflow-hidden whitespace-nowrap rounded-lg text-ui text-slate-400 transition-colors duration-fast hover:bg-well hover:text-slate-100 active:bg-well-hover ${railRowPadding(rail)} ${
          rail ? `w-10 flex-none ${FOCUS_RING_INSET}` : `flex-1 ${FOCUS_RING}`
        }`}
      >
        <LogOut className="h-4 w-4 flex-shrink-0 text-slate-500" strokeWidth={1.75} aria-hidden="true" />
        <span className={railLabel(rail)}>Sign out</span>
      </button>
      {tip.tooltip}
    </>
  );
}
