'use client';

import dynamic from 'next/dynamic';
import { useState } from 'react';

import PageHeader from '@/components/ui/PageHeader';
import { useCanWrite, useCurrentUser } from '@/components/providers/CurrentUserProvider';
import { useAISuggestions } from '@/components/dependencies/AISuggestions';

// React Flow touches window at module scope — load client-side only.
const DependencyGraphView = dynamic(
  () => import('@/components/dependencies/DependencyGraphView'),
  { ssr: false }
);

export default function DependenciesPage() {
  // Bump to force the graph to re-fetch after a suggestion is accepted.
  const [refreshKey, setRefreshKey] = useState(0);
  const suggestions = useAISuggestions(() => setRefreshKey((k) => k + 1));
  // Suggestions only lead to writes (accepting adds a dependency), so the
  // trigger is a write affordance like the rest of the graph editing.
  const canWrite = useCanWrite();
  const { loading: userLoading } = useCurrentUser();
  return (
    <div className="space-y-6">
      <PageHeader
        title="Dependencies"
        subtitle="How your monitors depend on each other. Alerts use this map to point at the likely root cause."
      />
      {suggestions.results}
      <DependencyGraphView
        refreshToken={refreshKey}
        canWrite={canWrite}
        permissionsLoading={userLoading}
        renderAIButton={canWrite ? suggestions.renderButton : undefined}
      />
    </div>
  );
}
