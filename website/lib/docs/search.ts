import { DOC_PAGES } from './content';
import type { DocBlocks, DocSection } from './types';

export type DocSearchEntry = {
  href: string;
  page: string;
  group: string;
  section?: string;
  haystack: string;
};

const HAYSTACK_LIMIT = 1200;

function codeTerms(text: string): string[] {
  return [...text.matchAll(/`([^`]+)`/g)].map((match) => match[1]);
}

function blockTerms(block: DocBlocks): string[] {
  switch (block.type) {
    case 'paragraph':
      return codeTerms(block.text);
    case 'list':
      return block.items.flatMap(codeTerms);
    case 'callout':
      return [block.title, ...codeTerms(block.text)];
    case 'definitions':
      return block.items.flatMap((item) => [item.term, ...codeTerms(item.description)]);
    case 'table':
      return block.rows.flatMap((row) => [row[0] ?? '', ...row.slice(1).flatMap(codeTerms)]);
    default:
      return [];
  }
}

function sectionHaystack(section: DocSection, pageTitle: string): string {
  const terms = [...new Set(section.blocks.flatMap(blockTerms))];
  return [section.title, section.intro ?? '', pageTitle, ...terms]
    .join(' ')
    .replace(/[`*[\]()]/g, ' ')
    .replace(/\s+/g, ' ')
    .toLowerCase()
    .slice(0, HAYSTACK_LIMIT);
}

export function buildSearchIndex(): DocSearchEntry[] {
  return DOC_PAGES.flatMap((page) => {
    const base = `/docs/${page.slug}/`;
    return [
      {
        href: base,
        page: page.title,
        group: page.group,
        haystack: `${page.title} ${page.description} ${page.keywords.join(' ')}`.toLowerCase(),
      },
      ...page.sections.map((section) => ({
        href: `${base}#${section.id}`,
        page: page.title,
        group: page.group,
        section: section.title,
        haystack: sectionHaystack(section, page.title),
      })),
    ];
  });
}
