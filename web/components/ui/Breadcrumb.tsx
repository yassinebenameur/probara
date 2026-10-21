import Link from 'next/link';
import { ReactNode } from 'react';
import { FOCUS_RING } from './focus';

export interface BreadcrumbItem {
  label: string;
  href?: string;
}

interface BreadcrumbProps {
  items: BreadcrumbItem[];
}

export default function Breadcrumb({ items }: BreadcrumbProps) {
  if (items.length === 0) return null;
  return (
    <nav aria-label="Breadcrumb" className="mb-2 min-w-0 text-xs text-slate-500">
      <ol className="flex min-w-0 items-center gap-1.5">
        {items.map((item, idx) => {
          const isLast = idx === items.length - 1;
          const content: ReactNode = isLast ? (
            <span className="truncate text-slate-300" aria-current="page">{item.label}</span>
          ) : item.href ? (
            <Link className={`rounded transition-colors duration-fast hover:text-slate-200 ${FOCUS_RING}`} href={item.href}>
              {item.label}
            </Link>
          ) : (
            <span>{item.label}</span>
          );
          return (
            <li key={`${item.label}-${idx}`} className="inline-flex min-w-0 items-center gap-1.5">
              {content}
              {!isLast && <span aria-hidden="true" className="text-slate-500">/</span>}
            </li>
          );
        })}
      </ol>
    </nav>
  );
}
