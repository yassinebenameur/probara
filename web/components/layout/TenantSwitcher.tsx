'use client';

import { KeyboardEvent, useEffect, useId, useLayoutEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { Check, ChevronsUpDown } from 'lucide-react';
import { hasApiKey } from '@/lib/auth';
import {
  clearSelectedTenantId,
  emitTenantChange,
  getSelectedTenantId,
  setSelectedTenantId,
} from '@/lib/tenant';
import { getTenants } from '@/lib/api';
import type { Tenant } from '@/lib/types';
import { useSelectedTenantId } from '@/components/providers/TenantProvider';
import { FOCUS_RING, FOCUS_RING_INSET } from '@/components/ui/focus';
import { useTooltip } from '@/components/ui/Tooltip';

/**
 * Tenant picker in the sidebar foot. Admin sessions load the tenant list,
 * settle the selection (stored id if still valid, else the first tenant) and
 * announce it; the picker only renders when there is more than one tenant.
 * Mounted once: the sidebar is a single instance on every breakpoint, and
 * the collapsed rail passes `rail` to this same instance (a square initials
 * button opening a portaled menu) rather than rendering a second copy, so the
 * bootstrap still runs once.
 */
export default function TenantSwitcher({ rail = false }: { rail?: boolean }) {
  const [mounted, setMounted] = useState(false);
  const [tenants, setTenants] = useState<Tenant[]>([]);
  const [isAdmin, setIsAdmin] = useState(false);
  // The provider is the single source of truth for the selection; this picker
  // only writes to it, so no local copy can drift out of sync.
  const selectedTenantId = useSelectedTenantId();

  useEffect(() => {
    setMounted(true);
  }, []);

  useEffect(() => {
    let isActive = true;

    if (!mounted) return;
    if (hasApiKey()) return; // API-key sessions are pinned to one tenant

    getTenants()
      .then((response) => {
        if (!isActive) return;
        setIsAdmin(true);
        setTenants(response.items || []);

        const storedTenantId = getSelectedTenantId();
        const fallbackTenantId = response.items?.[0]?.id;
        const nextTenantId =
          storedTenantId && response.items.some((tenant) => tenant.id === storedTenantId)
            ? storedTenantId
            : fallbackTenantId || null;

        if (nextTenantId) {
          setSelectedTenantId(nextTenantId);
        } else {
          clearSelectedTenantId();
        }
        emitTenantChange();
      })
      .catch(() => {
        if (isActive) {
          setIsAdmin(false);
        }
      });

    return () => {
      isActive = false;
    };
  }, [mounted]);

  if (!isAdmin || tenants.length <= 1) return null;

  const select = (tenantId: string) => {
    setSelectedTenantId(tenantId);
    emitTenantChange();
  };

  if (rail) {
    return <TenantRailMenu tenants={tenants} selectedTenantId={selectedTenantId} onSelect={select} />;
  }

  return (
    <label className="block">
      <span className="mb-1 block px-1 text-2xs font-medium text-slate-500">Workspace</span>
      <span className="relative block">
      <select
        className={`w-full cursor-pointer appearance-none truncate rounded-lg border border-[var(--input-edge)] bg-control py-2 pl-3 pr-8 text-ui font-medium text-slate-200 transition-colors hover:border-slate-400 hover:bg-control-hover ${FOCUS_RING}`}
        value={selectedTenantId ?? ''}
        onChange={(e) => select(e.target.value)}
      >
        {tenants.map((tenant) => (
          <option key={tenant.id} value={tenant.id}>
            {tenant.name}
          </option>
        ))}
      </select>
      <ChevronsUpDown
        className="pointer-events-none absolute right-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-slate-500"
        strokeWidth={1.75}
        aria-hidden="true"
      />
      </span>
    </label>
  );
}

function initials(name: string) {
  const words = name.trim().split(/[\s_-]+/).filter(Boolean);
  if (words.length === 0) return '?';
  return words
    .slice(0, 2)
    .map((word) => word[0])
    .join('')
    .toUpperCase();
}

const MENU_GAP = 10;
const VIEWPORT_PADDING = 8;

/**
 * The workspace picker in the collapsed rail: the selected tenant's initials
 * in a square, opening a menu to the right (portaled: the rail's
 * backdrop-filter would otherwise contain and clip it). Opening focuses the
 * selected tenant; arrows, Home and End move; Escape and Tab close it and
 * return focus to the trigger; a click outside closes it.
 */
function TenantRailMenu({
  tenants,
  selectedTenantId,
  onSelect,
}: {
  tenants: Tenant[];
  selectedTenantId: string | null;
  onSelect: (tenantId: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const [pos, setPos] = useState<{ left: number; top: number } | null>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const menuId = useId();
  const current = tenants.find((tenant) => tenant.id === selectedTenantId) ?? null;
  const name = current?.name ?? 'Select a workspace';
  const tip = useTooltip<HTMLButtonElement>({ label: `Workspace: ${name}`, disabled: open });

  const close = () => {
    setOpen(false);
    setPos(null);
  };
  const closeToTrigger = () => {
    close();
    tip.ref.current?.focus();
  };

  // Measure, then place: the menu's bottom lines up with the trigger's (it
  // sits at the foot of the rail), clamped into the viewport.
  useLayoutEffect(() => {
    if (!open || !menuRef.current || !tip.ref.current) return;
    const rect = tip.ref.current.getBoundingClientRect();
    const height = menuRef.current.offsetHeight;
    const top = Math.max(
      VIEWPORT_PADDING,
      Math.min(rect.bottom - height, window.innerHeight - VIEWPORT_PADDING - height)
    );
    setPos({ left: rect.right + MENU_GAP, top });
  }, [open, tip.ref]);

  const placed = pos !== null;
  useEffect(() => {
    if (!placed) return;
    const menu = menuRef.current;
    const target =
      menu?.querySelector<HTMLElement>('[aria-checked="true"]') ?? menu?.querySelector<HTMLElement>('[role="menuitemradio"]');
    target?.focus({ preventScroll: true });
  }, [placed]);

  useEffect(() => {
    if (!open) return;
    const onPointerDown = (event: MouseEvent) => {
      const target = event.target as Node;
      if (menuRef.current?.contains(target) || tip.ref.current?.contains(target)) return;
      close();
    };
    const onScroll = (event: Event) => {
      if (event.target instanceof Node && menuRef.current?.contains(event.target)) return;
      close();
    };
    document.addEventListener('mousedown', onPointerDown);
    window.addEventListener('scroll', onScroll, true);
    window.addEventListener('resize', close);
    return () => {
      document.removeEventListener('mousedown', onPointerDown);
      window.removeEventListener('scroll', onScroll, true);
      window.removeEventListener('resize', close);
    };
  }, [open, tip.ref]);

  const onMenuKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === 'Escape' || event.key === 'Tab') {
      event.preventDefault();
      event.stopPropagation();
      closeToTrigger();
      return;
    }
    const items = Array.from(menuRef.current?.querySelectorAll<HTMLElement>('[role="menuitemradio"]') ?? []);
    if (items.length === 0) return;
    const index = items.indexOf(document.activeElement as HTMLElement);
    let next: number | null = null;
    if (event.key === 'ArrowDown') next = index < 0 ? 0 : (index + 1) % items.length;
    else if (event.key === 'ArrowUp') next = index <= 0 ? items.length - 1 : index - 1;
    else if (event.key === 'Home') next = 0;
    else if (event.key === 'End') next = items.length - 1;
    if (next === null) return;
    event.preventDefault();
    items[next].focus({ preventScroll: true });
  };

  return (
    <>
      <button
        ref={tip.ref}
        {...tip.triggerProps}
        type="button"
        onClick={() => (open ? close() : setOpen(true))}
        aria-label={`Workspace: ${name}`}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-controls={open ? menuId : undefined}
        className={`flex h-10 w-10 items-center justify-center rounded-lg transition-colors hover:bg-well-hover active:bg-control-hover ${
          open ? 'bg-well-hover' : ''
        } ${FOCUS_RING_INSET}`}
      >
        <span
          aria-hidden="true"
          className="flex h-7 w-7 items-center justify-center rounded-md border border-[var(--input-edge)] bg-control text-2xs font-semibold tracking-wide text-slate-200"
        >
          {current ? initials(current.name) : '?'}
        </span>
      </button>
      {open &&
        createPortal(
          <div
            ref={menuRef}
            id={menuId}
            role="menu"
            aria-label="Workspace"
            onKeyDown={onMenuKeyDown}
            className={`glass-overlay fixed z-[60] w-60 !rounded-xl p-1 ${placed ? 'animate-pop-in origin-bottom-left' : ''}`}
            style={{ left: pos?.left ?? 0, top: pos?.top ?? 0, visibility: placed ? 'visible' : 'hidden' }}
          >
            <div className="px-2.5 pb-1 pt-1.5 text-2xs font-medium text-slate-500" aria-hidden="true">
              Workspace
            </div>
            <div className="dashboard-scroll max-h-[60vh] overflow-y-auto">
              {tenants.map((tenant) => {
                const selected = tenant.id === selectedTenantId;
                return (
                  <button
                    key={tenant.id}
                    type="button"
                    role="menuitemradio"
                    aria-checked={selected}
                    tabIndex={-1}
                    onClick={() => {
                      if (!selected) onSelect(tenant.id);
                      closeToTrigger();
                    }}
                    className={`flex w-full items-center gap-2.5 rounded-lg px-2.5 py-1.5 text-left text-ui transition-colors duration-fast ${FOCUS_RING_INSET} ${
                      selected ? 'font-medium text-white' : 'text-slate-300'
                    } hover:bg-well-hover hover:text-white focus-visible:bg-well-hover focus-visible:text-white active:bg-control-hover`}
                  >
                    <span
                      aria-hidden="true"
                      className="flex h-6 w-6 flex-shrink-0 items-center justify-center rounded-md border border-[var(--input-edge)] bg-control text-2xs font-semibold text-slate-200"
                    >
                      {initials(tenant.name)}
                    </span>
                    <span className="min-w-0 flex-1 truncate">{tenant.name}</span>
                    {selected && <Check className="h-3.5 w-3.5 flex-shrink-0 text-slate-300" strokeWidth={2} aria-hidden="true" />}
                  </button>
                );
              })}
            </div>
          </div>,
          document.body
        )}
      {tip.tooltip}
    </>
  );
}
