'use client';

import { useEffect, useState } from 'react';
import Link from 'next/link';
import { ArrowLeft, BookOpen, Plus } from 'lucide-react';
import type { PluginManifest } from '@/lib/types';
import { getAlertChannelPlugins } from '@/lib/api';
import { iconFor } from '@/components/alert-channels/icons';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import Pill from '@/components/ui/Pill';
import PageHeader from '@/components/ui/PageHeader';

const CAPABILITY_LABELS: Record<string, string> = {
  testable: 'Test from UI',
  acknowledge: 'Syncs acknowledgements',
};

export default function AlertChannelCatalogPage() {
  const [plugins, setPlugins] = useState<PluginManifest[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  useEffect(() => {
    (async () => {
      try {
        const items = await getAlertChannelPlugins();
        setPlugins(items ?? []);
      } catch (err: any) {
        setError(err?.message || 'Failed to load plugin catalog');
      } finally {
        setLoading(false);
      }
    })();
  }, []);

  return (
    <div className="space-y-6">
      <PageHeader
        title="Alert channel catalog"
        action={
          <Button variant="ghost" size="sm" icon={<ArrowLeft strokeWidth={1.75} />} asChild>
            <Link href="/settings?tab=channels">Back to channels</Link>
          </Button>
        }
      />

      {loading ? (
        <p className="text-sm text-slate-500">Loading plugins…</p>
      ) : error ? (
        <Callout tone="danger" title="Failed to load">{error}</Callout>
      ) : (
        <div className="grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3">
          {plugins.map((manifest) => (
            <PluginCard key={manifest.type} manifest={manifest} />
          ))}
        </div>
      )}
    </div>
  );
}

function PluginCard({ manifest }: { manifest: PluginManifest }) {
  const Icon = iconFor(manifest.icon_key);

  return (
    <div className="glass flex flex-col gap-4 p-5">
      <div className="flex items-start gap-3">
        <span className="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-xl bg-well-hover text-slate-300">
          <Icon className="h-4 w-4" strokeWidth={1.75} />
        </span>
        <div className="min-w-0 flex-1">
          <div>
            <h3 className="inline text-[15px] font-semibold text-white">{manifest.display_name}</h3>
            <span className="ml-2 whitespace-nowrap text-xs tabular-nums text-slate-500">v{manifest.version}</span>
          </div>
          <p className="mt-1 text-[13px] text-slate-400">{manifest.description}</p>
        </div>
      </div>

      {manifest.capabilities.length > 0 && (
        <div className="flex flex-wrap gap-1.5">
          {manifest.capabilities.map((cap) => (
            <Pill key={cap} tone="neutral" size="xs">
              {CAPABILITY_LABELS[cap] ?? cap}
            </Pill>
          ))}
        </div>
      )}

      <div className="mt-auto flex items-center justify-between gap-3">
        {manifest.docs_url ? (
          <a
            href={manifest.docs_url}
            target="_blank"
            rel="noreferrer"
            className="inline-flex items-center gap-1.5 text-xs text-slate-400 transition-colors hover:text-white"
          >
            <BookOpen strokeWidth={1.75} className="h-3.5 w-3.5" />
            Setup docs
          </a>
        ) : (
          <span />
        )}
        <Button variant="ghost" size="xs" icon={<Plus strokeWidth={1.75} />} asChild>
          <Link href={`/alert-channels/new?type=${encodeURIComponent(manifest.type)}`}>
            Add channel
          </Link>
        </Button>
      </div>
    </div>
  );
}
