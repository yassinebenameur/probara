import type { Metadata } from 'next';
import type { ReactNode } from 'react';

// Route title only ("Maintenance · Probara"); the page itself is a client component.
export const metadata: Metadata = { title: 'Maintenance' };

export default function Layout({ children }: { children: ReactNode }) {
  return children;
}
