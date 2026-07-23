import type { Metadata } from 'next';
import './globals.css';

const configuredSiteURL = process.env.NEXT_PUBLIC_SITE_URL?.trim();

export const metadata: Metadata = {
  metadataBase: configuredSiteURL ? new URL(configuredSiteURL) : undefined,
  title: {
    default: 'Probara — Monitoring without blind spots',
    template: '%s · Probara',
  },
  description:
    'Self-hosted, Kubernetes-first monitoring for endpoints, protocols, databases, hosts, private locations, incidents, and public status pages.',
  keywords: [
    'uptime monitoring',
    'self-hosted monitoring',
    'Kubernetes monitoring',
    'synthetic monitoring',
    'status pages',
    'private location monitoring',
  ],
  openGraph: {
    title: 'Probara — Monitoring without blind spots',
    description:
      'Monitor every critical path, from public endpoints to private infrastructure, from one self-hosted control plane.',
    type: 'website',
  },
  robots: {
    index: true,
    follow: true,
  },
};

export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="en" data-scroll-behavior="smooth">
      <body>{children}</body>
    </html>
  );
}
