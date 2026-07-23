import type { Metadata } from 'next';
import { notFound } from 'next/navigation';
import { DocsArticle } from '@/components/docs/DocsArticle';
import { DOC_PAGES, getDocPage } from '@/lib/docs/content';

type DocumentationPageProps = {
  params: Promise<{ slug: string }>;
};

export const dynamicParams = false;

export function generateStaticParams() {
  return DOC_PAGES.map((page) => ({ slug: page.slug }));
}

export async function generateMetadata({
  params,
}: DocumentationPageProps): Promise<Metadata> {
  const { slug } = await params;
  const page = getDocPage(slug);

  if (!page) {
    return {};
  }

  return {
    title: page.title,
    description: page.description,
    keywords: page.keywords,
    openGraph: {
      title: `${page.title} · Probara documentation`,
      description: page.description,
      type: 'article',
    },
  };
}

export default async function DocumentationPage({ params }: DocumentationPageProps) {
  const { slug } = await params;
  const page = getDocPage(slug);

  if (!page) {
    notFound();
  }

  return <DocsArticle page={page} />;
}
