// On-call schedules (/v1/oncall-schedules): types and the pure helpers the
// /oncall pages use; the requests live in lib/api.ts. Schedules are
// informational for now — nothing pages them until escalation policies.

export type RotationKind = 'daily' | 'weekly';
export type ShiftSource = 'rotation' | 'override';

export interface OnCallUser {
  id: string;
  username: string;
  /** False once the user can no longer be on call here (disabled, removed). */
  eligible: boolean;
  verified_contacts: number;
}

export interface OnCallPerson extends OnCallUser {
  role: string;
}

export interface Rotation {
  id: string;
  position: number;
  name: string;
  kind: RotationKind;
  handoff_time: string;
  handoff_weekday?: number;
  start_at: string;
  end_at?: string;
  members: OnCallUser[];
}

export interface Shift {
  user: OnCallUser;
  source: ShiftSource;
  rotation_id?: string;
  override_id?: string;
  start: string;
  end: string;
}

export interface OnCallSchedule {
  id: string;
  name: string;
  description: string;
  timezone: string;
  rotations: Rotation[];
  ical_enabled: boolean;
  on_call: Shift | null;
  next: Shift | null;
  created_at: string;
  updated_at: string;
}

export interface RotationInput {
  name?: string;
  kind: RotationKind;
  handoff_time: string;
  handoff_weekday?: number;
  start_at: string;
  end_at?: string;
  member_ids: string[];
}

export interface ScheduleInput {
  name: string;
  description?: string;
  timezone: string;
  rotations: RotationInput[];
}

export interface PreviewResponse {
  from: string;
  to: string;
  timezone: string;
  shifts: Shift[];
}

export interface OnCallOverride {
  id: string;
  user: OnCallUser;
  starts_at: string;
  ends_at: string;
  note: string;
  created_by?: OnCallUser;
  created_at: string;
}

export interface CreateOverrideRequest {
  user_id: string;
  starts_at: string;
  ends_at: string;
  note?: string;
}

export interface ICalToken {
  token: string;
  path: string;
  url?: string;
}

export const WEEKDAYS = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];

/** "Daily at 09:00" / "Weekly, Monday 09:00". */
export function describeRotation(r: Pick<Rotation, 'kind' | 'handoff_time' | 'handoff_weekday'>): string {
  if (r.kind === 'weekly') {
    const day = WEEKDAYS[r.handoff_weekday ?? 1] ?? 'Monday';
    return `Weekly, ${day} ${r.handoff_time}`;
  }
  return `Daily at ${r.handoff_time}`;
}

export function browserTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
  } catch {
    return 'UTC';
  }
}

export function timezoneOptions(): string[] {
  const supported = (Intl as unknown as { supportedValuesOf?: (key: string) => string[] }).supportedValuesOf;
  try {
    return supported ? ['UTC', ...supported('timeZone').filter((tz) => tz !== 'UTC')] : ['UTC'];
  } catch {
    return ['UTC'];
  }
}

const pad2 = (n: number) => String(n).padStart(2, '0');

// The wall-clock fields of an instant in a zone.
function zonedParts(date: Date, tz: string) {
  const parts = new Intl.DateTimeFormat('en-US', {
    timeZone: tz,
    hourCycle: 'h23',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  }).formatToParts(date);
  const get = (type: string) => Number(parts.find((p) => p.type === type)?.value ?? 0);
  return { y: get('year'), mo: get('month'), d: get('day'), h: get('hour') % 24, mi: get('minute'), s: get('second') };
}

// Offset of tz from UTC at an instant, in milliseconds.
function zoneOffset(date: Date, tz: string): number {
  const p = zonedParts(date, tz);
  return Date.UTC(p.y, p.mo - 1, p.d, p.h, p.mi, p.s) - Math.floor(date.getTime() / 1000) * 1000;
}

/**
 * The instant at a wall-clock time (`YYYY-MM-DDTHH:mm`, a datetime-local
 * value) in a zone, as an ISO string. A time skipped by a DST change maps to
 * the instant the same distance past the change.
 */
export function zonedLocalToISO(local: string, tz: string): string | null {
  const m = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/.exec(local);
  if (!m) return null;
  const wall = Date.UTC(+m[1], +m[2] - 1, +m[3], +m[4], +m[5]);
  try {
    let guess = wall - zoneOffset(new Date(wall), tz);
    // Re-check once: the offset at the result can differ across a change.
    guess = wall - zoneOffset(new Date(guess), tz);
    return new Date(guess).toISOString();
  } catch {
    return null;
  }
}

/** An instant as a datetime-local value in a zone (inverse of zonedLocalToISO). */
export function isoToZonedLocal(iso: string, tz: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return '';
  try {
    const p = zonedParts(date, tz);
    return `${p.y}-${pad2(p.mo)}-${pad2(p.d)}T${pad2(p.h)}:${pad2(p.mi)}`;
  } catch {
    return '';
  }
}

/** Shift time in the schedule's zone, `Mon 3 Nov, 09:00`. */
export function formatZoned(iso: string, tz: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return '—';
  try {
    const p = zonedParts(date, tz);
    const weekday = WEEKDAYS[new Date(Date.UTC(p.y, p.mo - 1, p.d)).getUTCDay()].slice(0, 3);
    const month = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'][p.mo - 1];
    return `${weekday} ${p.d} ${month}, ${pad2(p.h)}:${pad2(p.mi)}`;
  } catch {
    return iso;
  }
}

/** Form-side validation of a schedule; the API stays the authority. */
export function scheduleInputError(input: ScheduleInput): string | null {
  if (!input.name.trim()) return 'Name the schedule.';
  if (input.rotations.length === 0) return 'Add at least one rotation.';
  for (const [i, r] of input.rotations.entries()) {
    const problem = rotationProblem(r);
    if (!problem) continue;
    return input.rotations.length > 1
      ? `Rotation ${i + 1}: ${problem}`
      : problem.charAt(0).toUpperCase() + problem.slice(1);
  }
  return null;
}

function rotationProblem(r: RotationInput): string | null {
  if (!/^([01]\d|2[0-3]):[0-5]\d$/.test(r.handoff_time)) return 'set a handoff time (HH:MM).';
  if (r.kind === 'weekly' && (r.handoff_weekday === undefined || r.handoff_weekday < 0 || r.handoff_weekday > 6)) {
    return 'choose the handoff day.';
  }
  if (!r.start_at) return 'set when the rotation starts.';
  if (r.end_at && new Date(r.end_at) <= new Date(r.start_at)) return 'the end must be after the start.';
  if (r.member_ids.length === 0) return 'add at least one member.';
  return null;
}

/** The feed's absolute URL: the API's when PUBLIC_BASE_URL is set, else this origin's. */
export function feedURL(token: ICalToken, origin: string): string {
  return token.url || `${origin.replace(/\/$/, '')}${token.path}`;
}
