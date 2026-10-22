import type { Metadata } from 'next';
import { Archivo, IBM_Plex_Mono } from 'next/font/google';
import './globals.css';
import Layout from '@/components/layout/Layout';
import { ToastProvider } from '@/components/ui/ToastProvider';

const archivo = Archivo({
  subsets: ['latin'],
  display: 'swap',
  variable: '--font-sans',
  axes: ['wdth'],
});

const plexMono = IBM_Plex_Mono({
  subsets: ['latin'],
  weight: ['400', '500', '600'],
  display: 'swap',
  variable: '--font-mono',
});

// Route segments name themselves (a server layout.tsx exporting `title`, or
// useDocumentTitle on client detail pages); the template appends the brand.
export const metadata: Metadata = {
  title: { template: '%s · Probara', default: 'Probara' },
  description: 'Monitoring Platform Dashboard',
};

// Runs before first paint. A stored 'light' / 'dark' wins; otherwise the
// theme follows the OS and keeps following it (ThemeToggle's "system" mode
// clears the key). Re-applied on OS scheme changes and on storage events, so
// another tab's choice lands here too.
const THEME_BOOT = `(function(){var d=document.documentElement,m=window.matchMedia?matchMedia('(prefers-color-scheme: dark)'):null;function a(){var v=null;try{v=localStorage.getItem('probara-theme')}catch(e){}d.dataset.theme=v==='dark'||v==='light'?v:(m&&m.matches?'dark':'light')}a();if(m&&m.addEventListener)m.addEventListener('change',a);window.addEventListener('storage',function(e){if(e.key===null||e.key==='probara-theme')a()})})();`;

// Also before first paint: the desktop sidebar's collapsed rail
// (components/layout/useSidebarCollapsed.ts). The attribute alone drives the
// sidebar width in CSS, so a collapsed sidebar never paints expanded first.
const SIDEBAR_BOOT = `(function(){try{if(localStorage.getItem('probara-sidebar')==='collapsed')document.documentElement.dataset.sidebar='collapsed'}catch(e){}})();`;

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html lang="en" className={`${archivo.variable} ${plexMono.variable}`} suppressHydrationWarning>
      <body suppressHydrationWarning>
        <script dangerouslySetInnerHTML={{ __html: THEME_BOOT + SIDEBAR_BOOT }} />
        <ToastProvider>
          <Layout>{children}</Layout>
        </ToastProvider>
      </body>
    </html>
  );
}
