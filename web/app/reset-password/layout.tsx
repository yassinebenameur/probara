import type { Metadata } from 'next';
import type { ReactNode } from 'react';

// Route title only ("Reset password · Probara"); the page itself is a client
// component outside the app shell (lib/public-routes.ts). Never indexed, and
// no referrer leaves it: the link that opens it carries a credential.
export const metadata: Metadata = {
  title: 'Reset password',
  robots: { index: false, follow: false },
  referrer: 'no-referrer',
};

export default function Layout({ children }: { children: ReactNode }) {
  return children;
}
