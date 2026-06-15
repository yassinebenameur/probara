'use client';

import dynamic from 'next/dynamic';
import { useState } from 'react';

import PageHeader from '@/components/ui/PageHeader';
import AISuggestions from '@/components/dependencies/AISuggestions';

// React Flow touches window at module scope — load client-side only.
const DependencyGraphView = dynamic(
  () => import('@/components/dependencies/DependencyGraphView'),
  { ssr: false }
);

export default function DependenciesPage() {
  // Bump to force the graph to re-fetch after a suggestion is accepted.
  const [refreshKey, setRefreshKey] = useState(0);
  return (
    <div className="space-y-6">
      <PageHeader
        title="Dependencies"
        subtitle="How your monitors depend on each other — alerts use this map to point at the likely root cause"
      />
      <AISuggestions onAccepted={() => setRefreshKey((k) => k + 1)} />
      <DependencyGraphView key={refreshKey} />
    </div>
  );
}
