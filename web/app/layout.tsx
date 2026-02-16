import type { Metadata } from 'next';
import './globals.css';
import Layout from '@/components/layout/Layout';

export const metadata: Metadata = {
  title: 'Probara',
  description: 'Monitoring Platform Dashboard',
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html lang="en">
      <body suppressHydrationWarning>
        <Layout>{children}</Layout>
      </body>
    </html>
  );
}
