'use client';

import { useEffect, useState } from 'react';
import Link from 'next/link';
import { ArrowLeft, ArrowRight, BookOpen, Plus } from 'lucide-react';
import type { PluginManifest } from '@/lib/types';
import { getAlertChannelPlugins } from '@/lib/api';
import { iconFor } from '@/components/alert-channels/icons';
import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
import Pill from '@/components/ui/Pill';
import PageHeader from '@/components/ui/PageHeader';

const CAPABILITY_LABELS: Record<string, string> = {
  rendered_alert: 'Rendered',
  raw_event: 'Native format',
  testable: 'Test from UI',
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
    <div className="flex flex-col gap-4">
      <PageHeader
        title="Alert channel catalog"
        subtitle="Browse the integrations bundled with this Probara installation."
        action={
          <Button variant="ghost" size="sm" icon={<ArrowLeft strokeWidth={1.75} />} asChild>
            <Link href="/alert-channels">Back to channels</Link>
          </Button>
        }
      />

      {loading ? (
        <Panel title="Catalog" subtitle="Loading">
          <div className="text-sm text-slate-500">Loading plugins…</div>
        </Panel>
      ) : error ? (
        <Panel title="Catalog" subtitle="Failed to load">
          <div className="text-sm text-rose-400">{error}</div>
        </Panel>
      ) : (
        <div className="grid grid-cols-1 gap-3 md:grid-cols-2 lg:grid-cols-3">
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
    <div className="flex flex-col gap-3 rounded-lg border border-white/[0.06] bg-slate-900/40 p-4 transition hover:border-white/[0.12] hover:bg-slate-900/60">
      <div className="flex items-start gap-3">
        <div className="rounded-lg bg-slate-800/70 p-2 text-cyan-300">
          <Icon className="h-5 w-5" strokeWidth={1.75} />
        </div>
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <h3 className="text-sm font-medium text-white">{manifest.display_name}</h3>
            <span className="text-[10px] uppercase tracking-wide text-slate-500">
              v{manifest.version}
            </span>
          </div>
          <p className="mt-1 text-xs text-slate-400">{manifest.description}</p>
        </div>
      </div>

      <div className="flex flex-wrap gap-1.5">
        {manifest.capabilities.map((cap) => (
          <Pill key={cap} tone="neutral" size="xs">
            {CAPABILITY_LABELS[cap] ?? cap}
          </Pill>
        ))}
      </div>

      <div className="mt-auto flex items-center justify-between border-t border-white/[0.05] pt-3">
        {manifest.docs_url ? (
          <a
            href={manifest.docs_url}
            target="_blank"
            rel="noreferrer"
            className="inline-flex items-center gap-1 text-xs text-slate-400 hover:text-cyan-300"
          >
            <BookOpen strokeWidth={1.75} className="h-3.5 w-3.5" />
            Setup docs
          </a>
        ) : (
          <span className="text-xs text-slate-600">No docs link</span>
        )}
        <Button
          variant="ghost"
          size="xs"
          icon={<Plus strokeWidth={1.75} />}
          asChild
        >
          <Link href={`/alert-channels/new?type=${encodeURIComponent(manifest.type)}`}>
            Add channel
            <ArrowRight strokeWidth={1.75} className="h-3 w-3 ml-1" />
          </Link>
        </Button>
      </div>
    </div>
  );
}
