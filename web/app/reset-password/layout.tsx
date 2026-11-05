import type { Metadata } from 'next';
import type { ReactNode } from 'react';

// Route title only ("Reset password · Probara"); the page itself is a client component.
export const metadata: Metadata = { title: 'Reset password' };

export default function Layout({ children }: { children: ReactNode }) {
  return children;
}
