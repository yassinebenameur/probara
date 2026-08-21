import Link from 'next/link';
import { BellOff } from 'lucide-react';
import type { AlertRouting } from '@/lib/types';

/**
 * Explains an unrouted monitor: what is wrong and where to fix it. Returns
 * null when the monitor's alerts do reach someone, so callers can render
 * nothing in the healthy case.
 *
 * `reason` mirrors shared/alertrouting — the rules the alerter dispatches by.
 */
export function describeAlertRouting(routing?: AlertRouting): { title: string; detail: string; fixHref: string } | null {
  if (!routing || routing.reachable) return null;

  switch (routing.reason) {
    case 'no_custom_channels':
      return {
        title: 'Alerts reach nobody',
        detail:
          'This monitor uses custom routing but has no channel assigned. Assign one, or switch it to the workspace default routing.',
        fixHref: '/alert-channels',
      };
    case 'custom_channels_disabled':
      return {
        title: 'Alerts reach nobody',
        detail: `All ${routing.assigned_channels} channel${routing.assigned_channels === 1 ? '' : 's'} assigned to this monitor ${
          routing.assigned_channels === 1 ? 'is' : 'are'
        } disabled, so nothing is delivered. Re-enable one or assign another.`,
        fixHref: '/alert-channels',
      };
    case 'no_tenant_default_channels':
      return {
        title: 'Alerts reach nobody',
        detail:
          'This monitor follows the workspace default routing, which has no channels. Add a default channel in settings, or give the monitor custom routing.',
        fixHref: '/settings',
      };
    case 'tenant_default_channels_disabled':
      return {
        title: 'Alerts reach nobody',
        detail:
          'Every channel in the workspace default routing is disabled, so nothing is delivered. Re-enable one in settings.',
        fixHref: '/alert-channels',
      };
    case 'group_rollup_unrouted':
      return {
        title: 'Alerts reach nobody',
        detail: `${
          routing.rollup_group_name ? `Group "${routing.rollup_group_name}"` : 'A group'
        } rolls this monitor's alerts into its own, and that group has no active channel — so this monitor's own channels never fire.`,
        fixHref: '/alert-channels',
      };
    case 'group_rollup_paused':
      return {
        title: 'Alerts reach nobody',
        detail: `${
          routing.rollup_group_name ? `Group "${routing.rollup_group_name}"` : 'A group'
        } rolls this monitor's alerts into its own but is paused, so neither the group nor this monitor notifies anyone. Resume the group, or stop rolling its members up.`,
        fixHref: '/monitors',
      };
    default:
      return {
        title: 'Alerts reach nobody',
        detail: 'No active notification channel resolves for this monitor.',
        fixHref: '/alert-channels',
      };
  }
}

/**
 * Icon-only marker for list rows, where a text pill would crowd the name, type,
 * and tags. The reason lives in the tooltip and the accessible label; the
 * detail page carries the full sentence. Renders nothing when alerts are
 * routed, or when the monitor is paused — a paused monitor never alerts, so its
 * routing is moot until it resumes (the same rule the dashboard count applies).
 */
export function AlertRoutingBadge({ routing, enabled = true }: { routing?: AlertRouting; enabled?: boolean }) {
  const described = describeAlertRouting(routing);
  if (!described || !enabled) return null;

  return (
    <span
      className="inline-flex h-4 w-4 flex-shrink-0 items-center justify-center rounded-full border border-amber-500/30 bg-amber-500/10 text-amber-300"
      title={`No alert route — ${described.detail}`}
      aria-label={`No alert route — ${described.detail}`}
      role="img"
    >
      <BellOff className="h-2.5 w-2.5" strokeWidth={2} />
    </span>
  );
}

/**
 * Full-width callout for the monitor detail view, with a link to the fix.
 * Silent for paused monitors (see AlertRoutingBadge).
 */
export function AlertRoutingNotice({ routing, enabled = true }: { routing?: AlertRouting; enabled?: boolean }) {
  const described = describeAlertRouting(routing);
  if (!described || !enabled) return null;

  return (
    <div className="flex items-start gap-3 rounded-lg border border-amber-500/30 bg-amber-500/[0.07] px-3 py-2.5">
      <BellOff className="mt-0.5 h-4 w-4 shrink-0 text-amber-300" strokeWidth={1.75} />
      <div className="min-w-0 text-xs">
        <p className="font-medium text-amber-200">{described.title}</p>
        <p className="mt-0.5 text-amber-200/70">
          {described.detail}{' '}
          <Link href={described.fixHref} className="text-cyan-400 hover:text-cyan-300">
            Fix routing
          </Link>
        </p>
      </div>
    </div>
  );
}
