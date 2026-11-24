import type { Metadata } from 'next';
import type { ReactNode } from 'react';

// Route title only ("Forgot password · Probara"); the page itself is a client
// component outside the app shell (lib/public-routes.ts), reached signed out
// from the sign-in page. Never indexed.
export const metadata: Metadata = {
  title: 'Forgot password',
  robots: { index: false, follow: false },
  referrer: 'no-referrer',
};

export default function Layout({ children }: { children: ReactNode }) {
  return children;
}
