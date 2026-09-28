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
      <p className="text-sm text-slate-500">No alert channel plugins available.</p>
    );
  }

  return (
    <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
      {manifests.map((manifest) => {
        const Icon = iconFor(manifest.icon_key);
        const selected = manifest.type === selectedType;
        return (
          <button
            type="button"
            key={manifest.type}
            disabled={disabled}
            onClick={() => onSelect(manifest)}
            className={`glass-well flex items-start gap-3 p-3 text-left transition-colors ${
              selected ? 'bg-cyan-500/[0.08] ring-1 ring-inset ring-cyan-500/45' : ''
            } ${
              disabled
                ? `cursor-not-allowed ${selected ? '' : 'opacity-40'}`
                : `cursor-pointer ${selected ? '' : 'hover:bg-well-hover'}`
            }`}
          >
            <Icon
              className={`mt-0.5 h-4 w-4 shrink-0 ${selected ? 'text-cyan-400' : 'text-slate-400'}`}
              strokeWidth={1.75}
            />
            <div className="min-w-0">
              <p className={`text-[13px] font-medium ${selected ? 'text-white' : 'text-slate-200'}`}>
                {manifest.display_name}
              </p>
              <p className="mt-0.5 text-xs leading-snug text-slate-500">{manifest.description}</p>
            </div>
          </button>
        );
      })}
    </div>
  );
}
