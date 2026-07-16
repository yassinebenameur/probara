'use client';

import type { PluginManifest } from '@/lib/types';
import { iconFor } from './icons';

interface PluginCatalogProps {
  manifests: PluginManifest[];
  selectedType?: string;
  onSelect: (manifest: PluginManifest) => void;
  disabled?: boolean;
}

export default function PluginCatalog({
  manifests,
  selectedType,
  onSelect,
  disabled = false,
}: PluginCatalogProps) {
  if (manifests.length === 0) {
    return (
      <p className="text-sm text-slate-400">No alert channel plugins available.</p>
    );
  }

  return (
    <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
      {manifests.map((manifest) => {
        const Icon = iconFor(manifest.icon_key);
        const selected = manifest.type === selectedType;
        return (
          <button
            type="button"
            key={manifest.type}
            disabled={disabled}
            onClick={() => onSelect(manifest)}
            className={`flex items-start gap-3 rounded-lg border p-3 text-left transition-all ${
              selected
                ? 'border-cyan-500/50 bg-cyan-500/10'
                : 'border-white/[0.06] bg-slate-900/40 hover:border-white/[0.1] hover:bg-slate-900/60'
            } ${disabled ? 'opacity-50 cursor-not-allowed' : 'cursor-pointer'}`}
          >
            <div
              className={`rounded-lg p-2 ${
                selected ? 'bg-cyan-500/20 text-cyan-300' : 'bg-slate-800/60 text-slate-400'
              }`}
            >
              <Icon className="h-4 w-4" strokeWidth={1.75} />
            </div>
            <div className="min-w-0">
              <p
                className={`text-sm font-medium ${
                  selected ? 'text-white' : 'text-slate-300'
                }`}
              >
                {manifest.display_name}
              </p>
              <p className="mt-0.5 text-xs text-slate-500">{manifest.description}</p>
            </div>
          </button>
        );
      })}
    </div>
  );
}
