'use client';

import Pill from '@/components/ui/Pill';

// Categorical tag hues. Status hues (rose, amber, emerald), pink (reads as
// rose) and the orange accent are left out so a tag never reads as a state or
// an action. Keep the length at 9 so existing tags keep their colour.
const TAG_COLORS = [
  { bg: 'bg-lime-500/[0.13]', text: 'text-lime-300', border: 'border-transparent' },
  { bg: 'bg-teal-500/[0.13]', text: 'text-teal-300', border: 'border-transparent' },
  { bg: 'bg-sky-500/[0.13]', text: 'text-sky-300', border: 'border-transparent' },
  { bg: 'bg-blue-500/[0.13]', text: 'text-blue-300', border: 'border-transparent' },
  { bg: 'bg-indigo-500/[0.13]', text: 'text-indigo-300', border: 'border-transparent' },
  { bg: 'bg-violet-500/[0.13]', text: 'text-violet-300', border: 'border-transparent' },
  { bg: 'bg-purple-500/[0.13]', text: 'text-purple-300', border: 'border-transparent' },
  { bg: 'bg-fuchsia-500/[0.13]', text: 'text-fuchsia-300', border: 'border-transparent' },
  { bg: 'bg-slate-500/[0.13]', text: 'text-slate-300', border: 'border-transparent' },
];

// Hash function to get consistent color for a tag
export function getTagColor(tag: string) {
  let hash = 0;
  for (let i = 0; i < tag.length; i++) {
    hash = tag.charCodeAt(i) + ((hash << 5) - hash);
  }
  return TAG_COLORS[Math.abs(hash) % TAG_COLORS.length];
}

// Tag pill — renders a colored Pill using the per-tag color palette. When onClick
// is provided, the wrapping span becomes a focusable button with a selected ring.
// The ring is neutral: orange is for actions, never for "selected".
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
    <Pill tone="tag" size={size} color={color}>
      {tag}
    </Pill>
  );
  if (!onClick) return pill;
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={selected}
      className={`inline-flex rounded-full transition focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-500/50 ${
        selected ? 'ring-1 ring-slate-300/80' : 'opacity-75 hover:opacity-100'
      }`}
    >
      {pill}
    </button>
  );
}
