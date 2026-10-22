import type { Metadata } from 'next';
import type { ReactNode } from 'react';

// Route title only ("Alert channels · Probara"); the page itself is a client component.
export const metadata: Metadata = { title: 'Alert channels' };

export default function Layout({ children }: { children: ReactNode }) {
  return children;
}
