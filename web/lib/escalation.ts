// Escalation policies (people layer): an ordered list of steps, each paging
// its targets once the alert has paged for the step's delay. Channel targets
// are ordinary routes (tiers with reminders); person targets — a user,
// whoever a schedule has on call, every workspace admin — are paged at their
// verified contacts while the alert is active and unsnoozed, and again on
// each repeat cycle. The server's rules live in shared/escalation.

import { formatDuration } from './format';

export type EscalationTargetKind = 'user' | 'schedule' | 'channel' | 'all_admins';

export interface EscalationTarget {
  kind: EscalationTargetKind;
  user_id?: string;
  schedule_id?: string;
  channel_id?: string;
  /** Channel targets only: keep paging the route while acknowledged. */
  notify_after_ack?: boolean;
  /** Read-only: username, schedule or channel name. */
  label?: string;
  channel_type?: string;
  /** Read-only: an active channel, or a person target that can page somebody now. */
  reachable?: boolean;
}

export interface EscalationStep {
  delay_seconds: number;
  targets: EscalationTarget[];
}

export interface EscalationPolicy {
  id: string;
  name: string;
  description: string;
  repeat_count: number;
  repeat_after_seconds: number;
  steps: EscalationStep[];
  monitor_count: number;
  tenant_default: boolean;
  created_at: string;
  updated_at: string;
}

export interface EscalationPolicyInput {
  name: string;
  description?: string;
  repeat_count: number;
  repeat_after_seconds: number;
  steps: EscalationStep[];
}

export interface ConvertRoutesRequest {
  monitor_id?: string;
  name?: string;
  assign: boolean;
}

// Limits, mirrored from shared/escalation.
export const MAX_STEPS = 10;
export const MAX_TARGETS_PER_STEP = 20;
export const MAX_REPEAT_COUNT = 10;
export const MIN_REPEAT_AFTER_SECONDS = 60;
export const MAX_REPEAT_AFTER_SECONDS = 86400;
export const MAX_STEP_DELAY_SECONDS = 7 * 86400;

export const TARGET_KIND_LABELS: Record<EscalationTargetKind, string> = {
  user: 'Person',
  schedule: 'On-call schedule',
  channel: 'Channel',
  all_admins: 'All workspace admins',
};

/** The id a target carries, by kind ('' for all_admins). */
export function targetRef(t: EscalationTarget): string {
  switch (t.kind) {
    case 'user':
      return t.user_id ?? '';
    case 'schedule':
      return t.schedule_id ?? '';
    case 'channel':
      return t.channel_id ?? '';
    default:
      return '';
  }
}

/** A target of kind with ref as its id. */
export function makeTarget(kind: EscalationTargetKind, ref: string): EscalationTarget {
  switch (kind) {
    case 'user':
      return { kind, user_id: ref };
    case 'schedule':
      return { kind, schedule_id: ref };
    case 'channel':
      return { kind, channel_id: ref };
    default:
      return { kind };
  }
}

/** "Immediately", "after 5m", … for a step's delay. */
export function describeDelay(seconds: number): string {
  return seconds <= 0 ? 'Immediately' : `After ${formatDuration(seconds)}`;
}

/** One line summarizing the repeat behaviour. */
export function describeRepeat(p: Pick<EscalationPolicy, 'repeat_count' | 'repeat_after_seconds'>): string {
  if (p.repeat_count <= 0) return 'People are paged once per step';
  const times = p.repeat_count === 1 ? 'once more' : `${p.repeat_count} more times`;
  return `People steps repeat ${times}, ${formatDuration(p.repeat_after_seconds)} after the last step`;
}

/** Whether any step has a person target. */
export function hasPeople(steps: EscalationStep[]): boolean {
  return steps.some((s) => s.targets.some((t) => t.kind !== 'channel'));
}

/**
 * The client-side check of a policy before saving (the server re-checks):
 * the first problem, naming the step, or null.
 */
export function policyInputError(p: EscalationPolicyInput): string | null {
  if (!p.name.trim()) return 'Name the policy.';
  if (p.steps.length === 0) return 'Add at least one step.';
  if (p.steps.length > MAX_STEPS) return `A policy has at most ${MAX_STEPS} steps.`;
  if (p.repeat_count < 0 || p.repeat_count > MAX_REPEAT_COUNT) return `Repeat between 0 and ${MAX_REPEAT_COUNT} times.`;
  if (p.repeat_after_seconds < MIN_REPEAT_AFTER_SECONDS || p.repeat_after_seconds > MAX_REPEAT_AFTER_SECONDS) {
    return 'The pause before a repeat must be between 1 minute and 24 hours.';
  }
  const channels = new Map<string, number>();
  for (let i = 0; i < p.steps.length; i++) {
    const step = p.steps[i];
    const n = i + 1;
    if (!Number.isInteger(step.delay_seconds) || step.delay_seconds < 0 || step.delay_seconds > MAX_STEP_DELAY_SECONDS) {
      return `Step ${n}: the delay must be between 0 and 7 days.`;
    }
    if (i > 0 && step.delay_seconds < p.steps[i - 1].delay_seconds) {
      return `Step ${n}: its delay must not be shorter than step ${i}'s.`;
    }
    if (step.targets.length === 0) return `Step ${n}: add at least one target.`;
    if (step.targets.length > MAX_TARGETS_PER_STEP) return `Step ${n}: at most ${MAX_TARGETS_PER_STEP} targets.`;
    const seen = new Set<string>();
    for (const t of step.targets) {
      const ref = targetRef(t);
      if (t.kind !== 'all_admins' && !ref) return `Step ${n}: choose a ${TARGET_KIND_LABELS[t.kind].toLowerCase()}.`;
      const key = `${t.kind}:${ref}`;
      if (seen.has(key)) return `Step ${n}: the same target is listed twice.`;
      seen.add(key);
      if (t.kind === 'channel') {
        const prev = channels.get(ref);
        if (prev !== undefined) return `Step ${n}: a channel can be in one step only (it is already in step ${prev}).`;
        channels.set(ref, n);
      }
    }
  }
  return null;
}

/** The write payload of a policy (labels and reachability dropped). */
export function toPolicyInput(p: EscalationPolicyInput): EscalationPolicyInput {
  return {
    name: p.name.trim(),
    description: (p.description ?? '').trim(),
    repeat_count: p.repeat_count,
    repeat_after_seconds: p.repeat_after_seconds,
    steps: p.steps.map((s) => ({
      delay_seconds: s.delay_seconds,
      targets: s.targets.map((t) => {
        const out = makeTarget(t.kind, targetRef(t));
        if (t.kind === 'channel' && t.notify_after_ack) out.notify_after_ack = true;
        return out;
      }),
    })),
  };
}
