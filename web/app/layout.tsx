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
    <html lang="en" className={`${archivo.variable} ${plexMono.variable}`} suppressHydrationWarning>
      <body suppressHydrationWarning>
        <script
          dangerouslySetInnerHTML={{
            __html:
              "try{document.documentElement.dataset.theme=localStorage.getItem('probara-theme')==='dark'?'dark':'light';}catch(e){document.documentElement.dataset.theme='light';}",
          }}
        />
        <ToastProvider>
          <Layout>{children}</Layout>
        </ToastProvider>
      </body>
    </html>
  );
}
