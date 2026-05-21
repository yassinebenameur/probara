import Link from 'next/link';
import { ReactNode } from 'react';

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
    <nav aria-label="Breadcrumb" className="mb-2 flex items-center gap-1.5 text-xs text-slate-500">
      {items.map((item, idx) => {
        const isLast = idx === items.length - 1;
        const content: ReactNode = isLast ? (
          <span className="text-slate-200">{item.label}</span>
        ) : item.href ? (
          <Link className="transition-colors hover:text-slate-200" href={item.href}>
            {item.label}
          </Link>
        ) : (
          <span>{item.label}</span>
        );
        return (
          <span key={`${item.label}-${idx}`} className="inline-flex items-center gap-1.5">
            {content}
            {!isLast && <span aria-hidden="true">›</span>}
          </span>
        );
      })}
    </nav>
  );
}
