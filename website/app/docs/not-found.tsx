import Link from 'next/link';
import { ArrowLeft, SearchX } from 'lucide-react';

export default function DocumentationNotFoundPage() {
  return (
    <div className="not-found not-found--docs">
      <span className="not-found__icon">
        <SearchX size={24} />
      </span>
      <span className="docs-eyebrow">404 · Documentation page not found</span>
      <h1>This reference page does not exist.</h1>
      <p>
        Browse the documentation index to find the current guides, references, and
        operational notes.
      </p>
      <Link className="button button--primary" href="/docs/">
        <ArrowLeft size={15} />
        Documentation index
      </Link>
    </div>
  );
}
