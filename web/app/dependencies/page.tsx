'use client';

import dynamic from 'next/dynamic';
import PageHeader from '@/components/ui/PageHeader';

// React Flow touches window at module scope — load client-side only.
const DependencyGraphView = dynamic(
  () => import('@/components/dependencies/DependencyGraphView'),
  { ssr: false }
);

export default function DependenciesPage() {
  return (
    <div className="space-y-6">
      <PageHeader
        title="Dependencies"
        subtitle="How your monitors depend on each other — alerts use this map to point at the likely root cause"
      />
      <DependencyGraphView />
    </div>
  );
}
