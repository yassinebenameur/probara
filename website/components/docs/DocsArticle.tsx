import Link from 'next/link';
import { ArrowLeft, ArrowRight, CircleAlert, CircleCheck, Info } from 'lucide-react';
import { FLAT_DOC_NAVIGATION } from '@/lib/docs/navigation';
import type { DocBlocks, DocPage } from '@/lib/docs/types';
import { CodeBlock } from './CodeBlock';

function InlineText({ text }: { text: string }) {
  const parts = text.split(/(`[^`]+`|\*\*[^*]+\*\*|\[[^\]]+\]\([^()\s]+\))/g);
  return (
    <>
      {parts.map((part, index) => {
        if (part.startsWith('`') && part.endsWith('`')) {
          return <code key={`${part}-${index}`}>{part.slice(1, -1)}</code>;
        }
        if (part.startsWith('**') && part.endsWith('**')) {
          return <strong key={`${part}-${index}`}>{part.slice(2, -2)}</strong>;
        }
        const link = part.match(/^\[([^\]]+)\]\(([^()\s]+)\)$/);
        if (link) {
          const [, label, href] = link;
          return href.startsWith('/') ? (
            <Link key={`${part}-${index}`} className="doc-link" href={href}>
              {label}
            </Link>
          ) : (
            <a
              key={`${part}-${index}`}
              className="doc-link"
              href={href}
              target="_blank"
              rel="noreferrer"
            >
              {label}
            </a>
          );
        }
        return part;
      })}
    </>
  );
}

function Block({ block }: { block: DocBlocks }) {
  switch (block.type) {
    case 'paragraph':
      return (
        <p>
          <InlineText text={block.text} />
        </p>
      );
    case 'list': {
      const List = block.ordered ? 'ol' : 'ul';
      return (
        <List>
          {block.items.map((item) => (
            <li key={item}>
              <InlineText text={item} />
            </li>
          ))}
        </List>
      );
    }
    case 'code':
      return <CodeBlock code={block.code} language={block.language} title={block.title} />;
    case 'callout': {
      const Icon =
        block.tone === 'warning' ? CircleAlert : block.tone === 'success' ? CircleCheck : Info;
      return (
        <aside className={`doc-callout doc-callout--${block.tone || 'info'}`}>
          <Icon size={17} />
          <div>
            <strong>{block.title}</strong>
            <p>
              <InlineText text={block.text} />
            </p>
          </div>
        </aside>
      );
    }
    case 'definitions':
      return (
        <dl className="doc-definitions">
          {block.items.map((item) => (
            <div key={item.term}>
              <dt>
                <InlineText text={item.term} />
              </dt>
              <dd>
                <InlineText text={item.description} />
              </dd>
            </div>
          ))}
        </dl>
      );
    case 'table':
      return (
        <div className="doc-table-wrap">
          <table>
            <thead>
              <tr>
                {block.columns.map((column) => (
                  <th key={column}>{column}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {block.rows.map((row, rowIndex) => (
                <tr key={`${row.join('-')}-${rowIndex}`}>
                  {row.map((cell, cellIndex) => (
                    <td key={`${cell}-${cellIndex}`}>
                      <InlineText text={cell} />
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      );
  }
}

export function DocsArticle({ page }: { page: DocPage }) {
  const navIndex = FLAT_DOC_NAVIGATION.findIndex((item) => item.slug === page.slug);
  const previous = navIndex > 0 ? FLAT_DOC_NAVIGATION[navIndex - 1] : null;
  const next =
    navIndex >= 0 && navIndex < FLAT_DOC_NAVIGATION.length - 1
      ? FLAT_DOC_NAVIGATION[navIndex + 1]
      : null;

  return (
    <div className="docs-article-layout">
      <article className="docs-article">
        <div className="docs-breadcrumb">
          <Link href="/docs/">Docs</Link>
          <span>/</span>
          <span>{page.group}</span>
        </div>
        <header className="docs-article__header">
          <span className="docs-eyebrow">{page.eyebrow}</span>
          <h1>{page.title}</h1>
          <p>{page.description}</p>
          <div className="docs-article__meta">
            <span>{page.readingTime}</span>
          </div>
        </header>

        <div className="docs-mobile-toc">
          <details>
            <summary>On this page</summary>
            <nav>
              {page.sections.map((section) => (
                <a href={`#${section.id}`} key={section.id}>
                  {section.title}
                </a>
              ))}
            </nav>
          </details>
        </div>

        <div className="docs-article__body">
          {page.sections.map((section) => (
            <section id={section.id} key={section.id}>
              <h2>{section.title}</h2>
              {section.intro && (
                <p className="doc-section-intro">
                  <InlineText text={section.intro} />
                </p>
              )}
              {section.blocks.map((block, index) => (
                <Block block={block} key={`${section.id}-${block.type}-${index}`} />
              ))}
            </section>
          ))}
        </div>

        <nav className="docs-pagination" aria-label="Adjacent documentation pages">
          {previous ? (
            <Link href={`/docs/${previous.slug}/`}>
              <ArrowLeft size={15} />
              <span>
                <small>Previous</small>
                <strong>{previous.title}</strong>
              </span>
            </Link>
          ) : (
            <span />
          )}
          {next && (
            <Link href={`/docs/${next.slug}/`}>
              <span>
                <small>Next</small>
                <strong>{next.title}</strong>
              </span>
              <ArrowRight size={15} />
            </Link>
          )}
        </nav>
      </article>

      <aside className="docs-toc">
        <p>On this page</p>
        <nav>
          {page.sections.map((section) => (
            <a href={`#${section.id}`} key={section.id}>
              {section.title}
            </a>
          ))}
        </nav>
        <a
          className="docs-toc__source"
          href="https://github.com/yassinebenameur/probara"
          target="_blank"
          rel="noreferrer"
        >
          View source on GitHub
          <ArrowRight size={12} />
        </a>
      </aside>
    </div>
  );
}
