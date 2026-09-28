'use client';

import { useEffect, useState } from 'react';
import { Copy, X } from 'lucide-react';
import { getLocationDeployInfo } from '@/lib/api';
import type { Location, LocationDeployInfo } from '@/lib/types';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import IconButton from '@/components/ui/IconButton';
import ModalPortal from '@/components/ui/ModalPortal';

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
    <ModalPortal>
      <div className="glass-scrim fixed inset-0 z-50 flex items-center justify-center p-4">
        <div className="glass-overlay flex max-h-[90vh] w-full max-w-2xl flex-col">
          <div className="flex items-start justify-between gap-4 px-6 pt-5">
            <div>
              <h2 className="text-base font-semibold text-white">
                Deploy worker — {location.name}
              </h2>
              <p className="mt-1 text-[13px] text-slate-400">
                Run a worker inside the network you want checks to originate from.
              </p>
            </div>
            <IconButton icon={<X strokeWidth={1.75} />} label="Close" onClick={onClose} />
          </div>

          <div className="min-h-0 flex-1 overflow-y-auto px-6 py-4">
            {loading ? (
              <div className="py-8 text-center text-sm text-slate-500">Loading deploy instructions…</div>
            ) : error ? (
              <Callout tone="danger">{error}</Callout>
            ) : info ? (
              <div className="space-y-5">
                <div>
                  <div className="mb-1.5 flex items-center justify-between">
                    <span className="text-[13px] font-medium text-slate-300">Docker run</span>
                    <Button
                      variant="subtle"
                      size="xs"
                      icon={<Copy strokeWidth={1.75} />}
                      onClick={() => copyToClipboard(info.docker_run_command, 'run')}
                    >
                      {copiedField === 'run' ? 'Copied' : 'Copy'}
                    </Button>
                  </div>
                  <pre className="glass-well overflow-x-auto whitespace-pre-wrap break-all px-3 py-2.5 font-mono text-xs text-slate-200">
                    {info.docker_run_command}
                  </pre>
                </div>

                <div>
                  <div className="mb-1.5 flex items-center justify-between">
                    <span className="text-[13px] font-medium text-slate-300">Docker Compose</span>
                    <Button
                      variant="subtle"
                      size="xs"
                      icon={<Copy strokeWidth={1.75} />}
                      onClick={() => copyToClipboard(info.docker_compose_yaml, 'compose')}
                    >
                      {copiedField === 'compose' ? 'Copied' : 'Copy'}
                    </Button>
                  </div>
                  <pre className="glass-well overflow-x-auto px-3 py-2.5 font-mono text-xs text-slate-200">
                    {info.docker_compose_yaml}
                  </pre>
                </div>

                {envEntries.length > 0 && (
                  <div>
                    <span className="mb-1.5 block text-[13px] font-medium text-slate-300">
                      Running your own image? Set these variables
                    </span>
                    <div className="glass-well overflow-x-auto">
                      <table className="w-full text-left text-xs">
                        <tbody className="divide-y divide-divider">
                          {envEntries.map(([key, value]) => (
                            <tr key={key}>
                              <td className="whitespace-nowrap px-3 py-2 font-mono text-slate-300">{key}</td>
                              <td className="break-all px-3 py-2 font-mono text-slate-400">{value}</td>
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
          </div>

          <div className="flex items-center justify-end px-6 pb-5">
            <Button type="button" variant="ghost" size="sm" onClick={onClose}>
              Close
            </Button>
          </div>
        </div>
      </div>
    </ModalPortal>
  );
}
