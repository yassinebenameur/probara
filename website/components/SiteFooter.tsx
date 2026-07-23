import Link from 'next/link';
import { ArrowUpRight } from 'lucide-react';
import { BrandMark } from './BrandMark';

const footerGroups = [
  {
    title: 'Product',
    links: [
      { label: 'Platform', href: '/#platform' },
      { label: 'Monitor types', href: '/#monitoring' },
      { label: 'Deployment', href: '/#deployment' },
      { label: 'Changelog', href: 'https://github.com/yassinebenameur/probara/blob/dev/CHANGELOG.md' },
    ],
  },
  {
    title: 'Documentation',
    links: [
      { label: 'Getting started', href: '/docs/getting-started/' },
      { label: 'Monitor reference', href: '/docs/monitors/' },
      { label: 'Configuration', href: '/docs/configuration/' },
      { label: 'API reference', href: '/docs/api/' },
    ],
  },
  {
    title: 'Project',
    links: [
      { label: 'GitHub', href: 'https://github.com/yassinebenameur/probara' },
      { label: 'GPL-3.0 license', href: 'https://github.com/yassinebenameur/probara/blob/dev/LICENSE' },
      { label: 'Issues', href: 'https://github.com/yassinebenameur/probara/issues' },
      { label: 'Releases', href: 'https://github.com/yassinebenameur/probara/releases' },
    ],
  },
];

export function SiteFooter() {
  return (
    <footer className="site-footer">
      <div className="site-footer__grid">
        <div className="site-footer__about">
          <Link href="/" aria-label="Probara home">
            <BrandMark />
          </Link>
          <p>
            Self-hosted monitoring for the systems your users and operators depend on.
          </p>
          <span className="site-footer__license">Open source · GPL-3.0</span>
        </div>
        {footerGroups.map((group) => (
          <div key={group.title} className="site-footer__group">
            <h2>{group.title}</h2>
            {group.links.map((link) => {
              const external = link.href.startsWith('http');
              return external ? (
                <a key={link.label} href={link.href} target="_blank" rel="noreferrer">
                  {link.label}
                  <ArrowUpRight size={12} />
                </a>
              ) : (
                <Link key={link.label} href={link.href}>
                  {link.label}
                </Link>
              );
            })}
          </div>
        ))}
      </div>
      <div className="site-footer__bottom">
        <span>Built for teams that would rather own their monitoring.</span>
        <span>© {new Date().getFullYear()} Probara contributors</span>
      </div>
    </footer>
  );
}
