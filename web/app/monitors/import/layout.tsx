import type { Metadata } from 'next';
import type { ReactNode } from 'react';

// Route title only ("Import monitors · Probara"); the page itself is a client component.
// Absolute: a parent segment's string title stops the root template here.
export const metadata: Metadata = { title: { absolute: 'Import monitors · Probara' } };

export default function Layout({ children }: { children: ReactNode }) {
  return children;
}
