import type { Metadata } from 'next';
import type { ReactNode } from 'react';

// Route title only ("Acknowledge alert · Probara"); the page itself is a
// client component. Kept out of search indexes: the URL is a credential.
export const metadata: Metadata = { title: 'Acknowledge alert', robots: { index: false, follow: false } };

export default function Layout({ children }: { children: ReactNode }) {
  return children;
}
