'use client';

import Pill from '@/components/ui/Pill';

// Tag color palette - vibrant colors that work on dark backgrounds
const TAG_COLORS = [
  { bg: 'bg-rose-500/20', text: 'text-rose-400', border: 'border-rose-500/30' },
  { bg: 'bg-amber-500/20', text: 'text-amber-400', border: 'border-amber-500/30' },
  { bg: 'bg-lime-500/20', text: 'text-lime-400', border: 'border-lime-500/30' },
  { bg: 'bg-emerald-500/20', text: 'text-emerald-400', border: 'border-emerald-500/30' },
  { bg: 'bg-teal-500/20', text: 'text-teal-400', border: 'border-teal-500/30' },
  { bg: 'bg-cyan-500/20', text: 'text-cyan-400', border: 'border-cyan-500/30' },
  { bg: 'bg-sky-500/20', text: 'text-sky-400', border: 'border-sky-500/30' },
  { bg: 'bg-blue-500/20', text: 'text-blue-400', border: 'border-blue-500/30' },
  { bg: 'bg-indigo-500/20', text: 'text-indigo-400', border: 'border-indigo-500/30' },
  { bg: 'bg-violet-500/20', text: 'text-violet-400', border: 'border-violet-500/30' },
  { bg: 'bg-purple-500/20', text: 'text-purple-400', border: 'border-purple-500/30' },
  { bg: 'bg-fuchsia-500/20', text: 'text-fuchsia-400', border: 'border-fuchsia-500/30' },
  { bg: 'bg-pink-500/20', text: 'text-pink-400', border: 'border-pink-500/30' },
  { bg: 'bg-orange-500/20', text: 'text-orange-400', border: 'border-orange-500/30' },
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
      className={`inline-flex rounded-full transition-all focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-500/40 ${
        selected ? 'ring-1 ring-offset-1 ring-offset-slate-900 ring-white/20' : 'hover:brightness-125'
      }`}
    >
      {pill}
    </button>
  );
}
