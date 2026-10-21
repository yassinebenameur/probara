'use client';

import Pill from '@/components/ui/Pill';
import { FOCUS_RING } from '@/components/ui/focus';

// Categorical tag hues. A tag is a neutral pill whose hue lives in a small
// dot only, so a list of tags reads calm (no rainbow of tinted pills) and the
// text keeps full contrast in both themes. Status hues (rose, amber, emerald),
// pink (reads as rose) and the orange accent are left out so a tag never
// reads as a state or an action. Keep the length at 9 so existing tags keep
// their colour. Dots use the -400 step (darker in light, lighter in dark) so
// the hue clears 3:1 on a pill on a hovered row in both themes; lime, the
// lightest hue, needs -300.
const TAG_COLORS = [
  { dot: 'bg-lime-300', bg: 'bg-lime-500/[0.13]', text: 'text-lime-300', border: 'border-transparent' },
  { dot: 'bg-teal-400', bg: 'bg-teal-500/[0.13]', text: 'text-teal-300', border: 'border-transparent' },
  { dot: 'bg-sky-400', bg: 'bg-sky-500/[0.13]', text: 'text-sky-300', border: 'border-transparent' },
  { dot: 'bg-blue-400', bg: 'bg-blue-500/[0.13]', text: 'text-blue-300', border: 'border-transparent' },
  { dot: 'bg-indigo-400', bg: 'bg-indigo-500/[0.13]', text: 'text-indigo-300', border: 'border-transparent' },
  { dot: 'bg-violet-400', bg: 'bg-violet-500/[0.13]', text: 'text-violet-300', border: 'border-transparent' },
  { dot: 'bg-purple-400', bg: 'bg-purple-500/[0.13]', text: 'text-purple-300', border: 'border-transparent' },
  { dot: 'bg-fuchsia-400', bg: 'bg-fuchsia-500/[0.13]', text: 'text-fuchsia-300', border: 'border-transparent' },
  { dot: 'bg-slate-400', bg: 'bg-slate-500/[0.13]', text: 'text-slate-300', border: 'border-transparent' },
];

// Hash function to get consistent color for a tag
export function getTagColor(tag: string) {
  let hash = 0;
  for (let i = 0; i < tag.length; i++) {
    hash = tag.charCodeAt(i) + ((hash << 5) - hash);
  }
  return TAG_COLORS[Math.abs(hash) % TAG_COLORS.length];
}

// Tag pill: a neutral Pill with the tag's hue in a dot. When onClick is
// provided, the wrapping span becomes a focusable toggle button with a
// selected ring. The ring is neutral: orange is for actions, never for
// "selected".
export default function TagPill({
  tag,
  size = 'sm',
  onClick,
  selected = false,
}: {
  tag: string;
  size?: 'xs' | 'sm';
  onClick?: () => void;
  selected?: boolean;
}) {
  const color = getTagColor(tag);
  const pill = (
    <Pill tone="neutral" size={size} className="max-w-[12rem]">
      <span className={`h-1.5 w-1.5 flex-shrink-0 rounded-full ${color.dot}`} aria-hidden="true" />
      <span className="truncate">{tag}</span>
    </Pill>
  );
  if (!onClick) return pill;
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={selected}
      className={`inline-flex rounded-full transition active:scale-[.98] ${FOCUS_RING} ${
        selected ? 'ring-1 ring-slate-300' : 'hover:ring-1 hover:ring-slate-500/60'
      }`}
    >
      {pill}
    </button>
  );
}
