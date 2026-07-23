import Link from 'next/link';
import { ArrowUpRight, Menu } from 'lucide-react';
import { BrandMark } from './BrandMark';

const links = [
  { href: '/#platform', label: 'Platform' },
  { href: '/#monitoring', label: 'Monitoring' },
  { href: '/#deployment', label: 'Deploy' },
  { href: '/docs/', label: 'Docs' },
];

export function SiteHeader({ docs = false }: { docs?: boolean }) {
  const appURL = process.env.NEXT_PUBLIC_APP_URL?.trim();

  return (
    <header className={`site-header ${docs ? 'site-header--docs' : ''}`}>
      <div className="site-header__inner">
        <Link href="/" aria-label="Probara home" className="site-header__brand">
          <BrandMark />
          {docs && <span className="site-header__docs-label">Docs</span>}
        </Link>

        <nav className="site-header__nav" aria-label="Primary navigation">
          {links.map((link) => (
            <Link key={link.href} href={link.href}>
              {link.label}
            </Link>
          ))}
        </nav>

        <div className="site-header__actions">
          {appURL ? (
            <a className="button button--ghost button--small desktop-action" href={appURL}>
              Open dashboard
              <ArrowUpRight size={14} />
            </a>
          ) : (
            <a
              className="button button--ghost button--small desktop-action"
              href="https://github.com/yassinebenameur/probara"
              target="_blank"
              rel="noreferrer"
            >
              GitHub
              <ArrowUpRight size={14} />
            </a>
          )}
          <Link className="button button--primary button--small" href="/docs/getting-started/">
            Get started
          </Link>
          <details className="mobile-menu">
            <summary aria-label="Open navigation">
              <Menu size={20} />
            </summary>
            <div className="mobile-menu__panel">
              {links.map((link) => (
                <Link key={link.href} href={link.href}>
                  {link.label}
                </Link>
              ))}
              {appURL && <a href={appURL}>Open dashboard</a>}
              <a href="https://github.com/yassinebenameur/probara">GitHub</a>
            </div>
          </details>
        </div>
      </div>
    </header>
  );
}
