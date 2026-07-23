import Link from 'next/link';
import { ArrowLeft, SearchX } from 'lucide-react';
import { SiteFooter } from '@/components/SiteFooter';
import { SiteHeader } from '@/components/SiteHeader';

export default function NotFoundPage() {
  return (
    <>
      <SiteHeader docs />
      <main className="not-found">
        <span className="not-found__icon">
          <SearchX size={24} />
        </span>
        <span className="docs-eyebrow">404 · Not found</span>
        <h1>This signal went missing.</h1>
        <p>
          The requested page does not exist in this build. Return to the documentation
          index or start again from the landing page.
        </p>
        <div>
          <Link className="button button--primary" href="/docs/">
            <ArrowLeft size={15} />
            Documentation
          </Link>
          <Link className="button button--secondary" href="/">
            Probara home
          </Link>
        </div>
      </main>
      <SiteFooter />
    </>
  );
}
