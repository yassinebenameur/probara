import type { Metadata } from 'next';
import type { ReactNode } from 'react';

// Route title only ("SLAs · Probara"); the page itself is a client component.
export const metadata: Metadata = { title: 'SLAs' };

export default function Layout({ children }: { children: ReactNode }) {
  return children;
}
