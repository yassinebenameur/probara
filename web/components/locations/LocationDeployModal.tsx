'use client';

import { useEffect, useState } from 'react';
import { Copy } from 'lucide-react';
import { getLocationDeployInfo } from '@/lib/api';
import type { Location, LocationDeployInfo } from '@/lib/types';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import Dialog from '@/components/ui/Dialog';

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
    <Dialog
      onClose={onClose}
      size="lg"
      title={`Deploy worker — ${location.name}`}
      description="Run a worker inside the network you want checks to originate from."
      bodyClassName=""
      footer={
        <Button type="button" variant="ghost" size="sm" onClick={onClose}>
          Close
        </Button>
      }
    >
      {loading ? (
        <div role="status" className="py-8 text-center text-sm text-slate-500">Loading deploy instructions…</div>
      ) : error ? (
        <Callout tone="danger">{error}</Callout>
      ) : info ? (
        <div className="space-y-5">
          <div>
            <div className="mb-1.5 flex items-center justify-between">
              <span className="text-ui font-medium text-slate-300">Docker run</span>
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
              <span className="text-ui font-medium text-slate-300">Docker Compose</span>
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
              <span className="mb-1.5 block text-ui font-medium text-slate-300">
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
    </Dialog>
  );
}
