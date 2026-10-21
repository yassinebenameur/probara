import type { Metadata } from 'next';
import type { ReactNode } from 'react';

// Route title only ("Connect · Probara"); the page itself is a client component.
export const metadata: Metadata = { title: 'Connect' };

export default function Layout({ children }: { children: ReactNode }) {
  return children;
}
