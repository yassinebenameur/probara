'use client';

import { useEffect, useState } from 'react';
import { Copy } from 'lucide-react';
import { getLocationDeployInfo } from '@/lib/api';
import type { Location, LocationDeployInfo } from '@/lib/types';
import Button from '@/components/ui/Button';

interface LocationDeployModalProps {
  location: Location;
  onClose: () => void;
}

export function LocationDeployModal({ location, onClose }: LocationDeployModalProps) {
  const [info, setInfo] = useState<LocationDeployInfo | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [copiedField, setCopiedField] = useState('');

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError('');
    getLocationDeployInfo(location.id)
      .then((data) => {
        if (!cancelled) setInfo(data);
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : 'Failed to load deploy instructions');
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [location.id]);

  const copyToClipboard = (value: string, field: string) => {
    if (!value) return;
    navigator.clipboard.writeText(value);
    setCopiedField(field);
    setTimeout(() => setCopiedField(''), 2000);
  };

  const envEntries = info ? Object.entries(info.env) : [];

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-slate-950/70 backdrop-blur-sm p-4">
      <div className="max-h-[90vh] w-full max-w-2xl overflow-y-auto rounded-xl border border-white/[0.08] bg-slate-900/95 p-6 shadow-2xl">
        {/* Header */}
        <div className="mb-5 flex items-start justify-between">
          <div>
            <h2 className="text-base font-semibold text-white">
              Deploy worker — {location.name}
            </h2>
            <p className="mt-1 text-xs text-slate-500">
              Run a worker inside the network you want checks to originate from.
            </p>
          </div>
          <button
            onClick={onClose}
            className="text-slate-400 hover:text-white transition-colors"
            aria-label="Close"
          >
            ×
          </button>
        </div>

        {loading ? (
          <div className="py-8 text-center text-sm text-slate-500">Loading deploy instructions…</div>
        ) : error ? (
          <p className="rounded border border-rose-500/20 bg-rose-500/10 px-3 py-2 text-xs text-rose-300">
            {error}
          </p>
        ) : info ? (
          <div className="space-y-5">
            <div>
              <div className="mb-1.5 flex items-center justify-between">
                <span className="text-xs font-medium text-slate-400">Docker run</span>
                <Button
                  variant="ghost"
                  size="xs"
                  icon={<Copy strokeWidth={1.75} />}
                  onClick={() => copyToClipboard(info.docker_run_command, 'run')}
                >
                  {copiedField === 'run' ? 'Copied' : 'Copy'}
                </Button>
              </div>
              <pre className="overflow-x-auto rounded-lg border border-white/[0.06] bg-slate-950/60 px-3 py-2.5 font-mono text-xs text-slate-200 whitespace-pre-wrap break-all">
                {info.docker_run_command}
              </pre>
            </div>

            <div>
              <div className="mb-1.5 flex items-center justify-between">
                <span className="text-xs font-medium text-slate-400">Docker Compose</span>
                <Button
                  variant="ghost"
                  size="xs"
                  icon={<Copy strokeWidth={1.75} />}
                  onClick={() => copyToClipboard(info.docker_compose_yaml, 'compose')}
                >
                  {copiedField === 'compose' ? 'Copied' : 'Copy'}
                </Button>
              </div>
              <pre className="overflow-x-auto rounded-lg border border-white/[0.06] bg-slate-950/60 px-3 py-2.5 font-mono text-xs text-slate-200">
                {info.docker_compose_yaml}
              </pre>
            </div>

            {envEntries.length > 0 && (
              <div>
                <span className="mb-1.5 block text-xs font-medium text-slate-400">
                  Running your own image? Set these variables
                </span>
                <div className="overflow-x-auto rounded-lg border border-white/[0.06] bg-slate-950/60">
                  <table className="w-full text-left text-xs">
                    <tbody>
                      {envEntries.map(([key, value]) => (
                        <tr key={key} className="border-b border-white/[0.04] last:border-b-0">
                          <td className="px-3 py-2 font-mono text-slate-300 whitespace-nowrap">{key}</td>
                          <td className="px-3 py-2 font-mono text-slate-400 break-all">{value}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </div>
            )}

            <p className="text-xs text-slate-500">
              Once the worker connects over NATS, its status flips to Connected.
            </p>
          </div>
        ) : null}

        {/* Footer */}
        <div className="mt-5 flex items-center justify-end">
          <Button type="button" variant="ghost" size="sm" onClick={onClose}>
            Close
          </Button>
        </div>
      </div>
    </div>
  );
}
