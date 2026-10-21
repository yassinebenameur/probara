import type { Metadata } from 'next';
import type { ReactNode } from 'react';

// Route title only ("New user · Probara"); the page itself is a client component.
// Absolute: a parent segment's string title stops the root template here.
export const metadata: Metadata = { title: { absolute: 'New user · Probara' } };

export default function Layout({ children }: { children: ReactNode }) {
  return children;
}
