import Link from 'next/link';
import { ChevronDown } from 'lucide-react';
import { DOC_NAVIGATION } from '@/lib/docs/navigation';
import { DocsSearch } from './DocsSearch';

function NavigationLinks() {
  return (
    <>
      <Link className="docs-sidebar__overview" href="/docs/">
        Documentation overview
      </Link>
      {DOC_NAVIGATION.map((group) => (
        <div className="docs-sidebar__group" key={group.title}>
          <h2>{group.title}</h2>
          {group.items.map((item) => (
            <Link href={`/docs/${item.slug}/`} key={item.slug}>
              {item.title}
            </Link>
          ))}
        </div>
      ))}
    </>
  );
}

export function DocsSidebar() {
  return (
    <aside className="docs-sidebar">
      <DocsSearch />
      <nav aria-label="Documentation navigation">
        <NavigationLinks />
      </nav>
      <details className="docs-sidebar__mobile-nav">
        <summary>
          Browse documentation
          <ChevronDown size={14} />
        </summary>
        <nav aria-label="Mobile documentation navigation">
          <NavigationLinks />
        </nav>
      </details>
    </aside>
  );
}
