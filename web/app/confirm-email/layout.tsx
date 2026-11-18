import type { Metadata } from 'next';
import type { ReactNode } from 'react';

// Route title only ("Confirm email address · Probara"); the page itself is a client
// component outside the app shell (lib/public-routes.ts). Never indexed, and
// no referrer leaves it: the link that opens it carries a credential.
export const metadata: Metadata = {
  title: 'Confirm email address',
  robots: { index: false, follow: false },
  referrer: 'no-referrer',
};

export default function Layout({ children }: { children: ReactNode }) {
  return children;
}
