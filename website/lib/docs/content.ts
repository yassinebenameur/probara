import type { DocPage } from './types';
import { PRODUCT_DOC_PAGES } from './pages';
import { CONFIGURATION_PAGE } from './pages/configuration';
import { DEPLOYMENT_PAGE } from './pages/deployment';
import { OPERATIONS_PAGE } from './pages/operations';
import { SECURITY_PAGE } from './pages/security';

export const DOC_PAGES: DocPage[] = [
  ...PRODUCT_DOC_PAGES,
  CONFIGURATION_PAGE,
  DEPLOYMENT_PAGE,
  OPERATIONS_PAGE,
  SECURITY_PAGE,
];

const DOC_PAGE_BY_SLUG = new Map(DOC_PAGES.map((page) => [page.slug, page]));

export function getDocPage(slug: string) {
  return DOC_PAGE_BY_SLUG.get(slug);
}
