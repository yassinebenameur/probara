import type { Metadata } from 'next';
import type { ReactNode } from 'react';

// Route title only ("Status pages · Probara"); the page itself is a client component.
export const metadata: Metadata = { title: 'Status pages' };

export default function Layout({ children }: { children: ReactNode }) {
  return children;
}
