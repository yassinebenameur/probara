import { DocsSidebar } from '@/components/docs/DocsSidebar';
import { SiteFooter } from '@/components/SiteFooter';
import { SiteHeader } from '@/components/SiteHeader';

export default function DocumentationLayout({ children }: { children: React.ReactNode }) {
  return (
    <>
      <SiteHeader docs />
      <div className="docs-shell">
        <DocsSidebar />
        <main className="docs-main">{children}</main>
      </div>
      <SiteFooter />
    </>
  );
}
