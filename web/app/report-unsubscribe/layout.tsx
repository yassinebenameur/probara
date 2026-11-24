import type { Metadata } from 'next';
import type { ReactNode } from 'react';

// Route title only ("Unsubscribe · Probara"); the page itself is a client
// component. Never indexed: every visit carries a credential.
export const metadata: Metadata = {
  title: 'Unsubscribe',
  robots: { index: false, follow: false },
  referrer: 'no-referrer',
};

export default function Layout({ children }: { children: ReactNode }) {
  return children;
}
