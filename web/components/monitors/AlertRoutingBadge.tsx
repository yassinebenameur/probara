import Link from 'next/link';
import { BellOff } from 'lucide-react';
import type { AlertRouting } from '@/lib/types';
import Callout from '@/components/ui/Callout';

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
        fixHref: '/settings?tab=channels',
      };
    case 'custom_channels_disabled':
      return {
        title: 'Alerts reach nobody',
        detail: `All ${routing.assigned_channels} channel${routing.assigned_channels === 1 ? '' : 's'} assigned to this monitor ${
          routing.assigned_channels === 1 ? 'is' : 'are'
        } disabled, so nothing is delivered. Re-enable one or assign another.`,
        fixHref: '/settings?tab=channels',
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
        fixHref: '/settings?tab=channels',
      };
    case 'group_rollup_unrouted':
      return {
        title: 'Alerts reach nobody',
        detail: `${
          routing.rollup_group_name ? `Group "${routing.rollup_group_name}"` : 'A group'
        } rolls this monitor's alerts into its own, and that group has no active channel — so this monitor's own channels never fire.`,
        fixHref: '/settings?tab=channels',
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
        fixHref: '/settings?tab=channels',
      };
  }
}

/**
 * Full-width callout for the monitor detail view, with a link to the fix.
 * Silent for paused monitors: a paused monitor never alerts, so its routing
 * is moot until it resumes (the same rule the dashboard count applies).
 */
export function AlertRoutingNotice({ routing, enabled = true }: { routing?: AlertRouting; enabled?: boolean }) {
  const described = describeAlertRouting(routing);
  if (!described || !enabled) return null;

  return (
    <Callout tone="warning" icon={<BellOff strokeWidth={1.75} />} title={described.title}>
      {described.detail}{' '}
      <Link href={described.fixHref} className="font-medium text-cyan-300 hover:text-cyan-200">
        Fix routing
      </Link>
    </Callout>
  );
}
