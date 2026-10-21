import { ReactNode } from 'react';
import Link from 'next/link';
import Button from './Button';

/** The one next step: rendered as the accent button. */
export interface EmptyStateAction {
  label: string;
  href?: string;
  onClick?: () => void;
  icon?: ReactNode;
}

interface EmptyStateProps {
  icon?: ReactNode;
  title: string;
  /** One sentence of direction. */
  description?: ReactNode;
  /**
   * A single action. Prefer the object form (an accent button); a node is
   * still accepted for existing callers. While the empty state carries the
   * action, hide the duplicate in the page header.
   */
  action?: ReactNode | EmptyStateAction;
  /** `inline` drops the glass panel, for an empty list inside a panel. */
  variant?: 'panel' | 'inline';
  /** Heading level for the title: 2 under a page h1 (default), 3 inside a titled panel. */
  headingLevel?: 2 | 3;
}

function isActionConfig(value: unknown): value is EmptyStateAction {
  return typeof value === 'object' && value !== null && !('type' in value) && 'label' in value;
}

export default function EmptyState({
  icon,
  title,
  description,
  action,
  variant = 'panel',
  headingLevel = 2,
}: EmptyStateProps) {
  let renderedAction: ReactNode = null;
  if (isActionConfig(action)) {
    renderedAction = action.href ? (
      <Button variant="accent" icon={action.icon} asChild>
        <Link href={action.href}>{action.label}</Link>
      </Button>
    ) : (
      <Button variant="accent" icon={action.icon} onClick={action.onClick}>
        {action.label}
      </Button>
    );
  } else if (action) {
    renderedAction = action as ReactNode;
  }

  const Heading = headingLevel === 3 ? 'h3' : 'h2';

  // Fades in so it does not hard-cut over the skeleton it replaces.
  return (
    <div
      className={`${variant === 'panel' ? 'glass py-14' : 'py-10'} flex animate-fade-in flex-col items-center justify-center px-6 text-center`}
    >
      {icon && (
        <span
          className="mb-4 inline-flex h-11 w-11 items-center justify-center rounded-full bg-well-hover text-slate-400 [&_svg]:h-5 [&_svg]:w-5"
          aria-hidden="true"
        >
          {icon}
        </span>
      )}
      <Heading className="text-title font-semibold text-white">{title}</Heading>
      {description && (
        <p className="mt-1.5 max-w-sm text-sm text-slate-400">{description}</p>
      )}
      {renderedAction && <div className="mt-5">{renderedAction}</div>}
    </div>
  );
}
