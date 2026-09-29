'use client';

import { KeyboardEvent, useState } from 'react';
import { Check, Copy, Download, KeyRound, X } from 'lucide-react';
import Button from '@/components/ui/Button';
import FormField from '@/components/ui/FormField';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import ModalPortal from '@/components/ui/ModalPortal';
import DiscoveryModal from './DiscoveryModal';
import { createDiscoveryIntegration } from '@/lib/api';
import {
  collectorManifest, errorMessage, installCommands, manifestInputErrors, parseNamespaces, validNamespace,
  type DiscoveryIntegration,
} from '@/lib/discovery';
import type { Location } from '@/lib/types';

// Per-viewer convenience only: the origin and image are not secrets and are
// re-entered on every install otherwise. Storage can be unavailable.
const ORIGIN_KEY = 'probara.discovery.origin';
const IMAGE_KEY = 'probara.discovery.image';
function remembered(key: string): string {
  try { return window.localStorage.getItem(key) || ''; } catch { return ''; }
}
function remember(key: string, value: string) {
  try { window.localStorage.setItem(key, value); } catch { /* ignore */ }
}
function defaultOrigin(): string {
  const saved = remembered(ORIGIN_KEY);
  if (saved) return saved;
  return typeof window !== 'undefined' && window.location.protocol === 'https:' ? window.location.origin : '';
}

interface ConnectClusterModalProps {
  /** Set to skip the details step: the credential was just rotated. */
  install?: DiscoveryIntegration & { token: string };
  locations: Location[];
  onCreated: (integration: DiscoveryIntegration) => void;
  onClose: () => void;
}

export default function ConnectClusterModal({ install, locations, onCreated, onClose }: ConnectClusterModalProps) {
  const [integration, setIntegration] = useState<(DiscoveryIntegration & { token: string }) | null>(install ?? null);
  const [name, setName] = useState('');
  const [namespaces, setNamespaces] = useState<string[]>([]);
  const [draft, setDraft] = useState('');
  const [locationId, setLocationId] = useState('');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [origin, setOrigin] = useState(defaultOrigin);
  const [image, setImage] = useState(() => remembered(IMAGE_KEY));
  const [touched, setTouched] = useState(false);
  const [downloaded, setDownloaded] = useState(false);
  const [confirmClose, setConfirmClose] = useState(false);

  const invalidNamespaces = namespaces.filter((ns) => !validNamespace(ns));
  const commitDraft = () => {
    const added = parseNamespaces(draft);
    if (added.length) setNamespaces((current) => [...new Set([...current, ...added])].slice(0, 20));
    setDraft('');
  };
  const onNamespaceKey = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter' || e.key === ',' || e.key === ' ') { e.preventDefault(); commitDraft(); }
    if (e.key === 'Backspace' && !draft && namespaces.length) setNamespaces(namespaces.slice(0, -1));
  };
  const allNamespaces = [...new Set([...namespaces, ...parseNamespaces(draft)])];
  const canCreate = !saving && name.trim() !== '' && allNamespaces.length > 0 && allNamespaces.length <= 20 && allNamespaces.every(validNamespace);

  const create = async () => {
    setSaving(true); setError('');
    try {
      const created = await createDiscoveryIntegration({ name: name.trim(), namespaces: allNamespaces, location_id: locationId || null });
      if (!created.token) throw new Error('The server did not return a collector credential.');
      setIntegration({ ...created, token: created.token });
      onCreated(created);
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setSaving(false);
    }
  };

  const fieldErrors = manifestInputErrors(origin, image);
  const download = () => {
    setTouched(true);
    if (!integration || fieldErrors.origin || fieldErrors.image) return;
    try {
      const content = collectorManifest(integration, origin, image, integration.token);
      const url = URL.createObjectURL(new Blob([content], { type: 'application/yaml' }));
      const a = document.createElement('a');
      a.href = url; a.download = installCommands(integration).file; a.click();
      URL.revokeObjectURL(url);
      remember(ORIGIN_KEY, new URL(origin).origin); remember(IMAGE_KEY, image.trim());
      setDownloaded(true);
    } catch (e) {
      setError(errorMessage(e));
    }
  };
  const requestClose = () => { if (integration && !downloaded) setConfirmClose(true); else onClose(); };

  const step = integration ? 2 : 1;
  const commands = integration ? installCommands(integration) : null;

  return (
    <>
      <DiscoveryModal
        title={install ? `Reinstall collector · ${install.name}` : 'Connect a Kubernetes cluster'}
        subtitle={!install && <Steps step={step} />}
        onClose={requestClose}
        locked={saving}
        width="lg"
        footer={step === 1 ? <>
          <Button variant="ghost" onClick={onClose} disabled={saving}>Cancel</Button>
          <Button variant="accent" onClick={() => void create()} disabled={!canCreate} loading={saving}>Create and continue</Button>
        </> : <>
          <Button variant="ghost" onClick={requestClose}>{downloaded ? 'Done' : 'Close'}</Button>
          <Button variant="accent" icon={<Download strokeWidth={1.75} />} onClick={download}>{downloaded ? 'Download again' : 'Download manifest'}</Button>
        </>}
      >
        {error && <p role="alert" className="mb-4 rounded-lg border border-rose-500/20 bg-rose-500/10 px-3 py-2 text-xs text-rose-300">{error}</p>}
        {step === 1 ? (
          <div className="space-y-5">
            <FormField label="Cluster name" required>
              <input className="input" value={name} maxLength={100} onChange={(e) => setName(e.target.value)} placeholder="e.g. production-eu" autoFocus />
            </FormField>
            <FormField
              label="Namespaces"
              required
              description="The collector lists Services and Ingresses in these namespaces only (up to 20). They must already exist."
              error={invalidNamespaces.length ? `Not a valid namespace: ${invalidNamespaces.join(', ')}` : undefined}
            >
              <div className="input flex min-h-[42px] flex-wrap items-center gap-1.5 !py-1.5">
                {namespaces.map((ns) => (
                  <span key={ns} className={`inline-flex items-center gap-1 rounded-full border px-2 py-0.5 font-mono text-xs ${validNamespace(ns) ? 'border-white/10 bg-white/[0.04] text-slate-200' : 'border-rose-500/40 bg-rose-500/10 text-rose-200'}`}>
                    {ns}
                    <button type="button" aria-label={`Remove ${ns}`} className="text-slate-500 hover:text-white" onClick={() => setNamespaces(namespaces.filter((n) => n !== ns))}>
                      <X className="h-3 w-3" aria-hidden="true" />
                    </button>
                  </span>
                ))}
                <input
                  aria-label="Add namespace"
                  className="min-w-[8rem] flex-1 border-0 bg-transparent font-mono text-sm text-slate-200 shadow-none outline-none ring-0 placeholder:font-sans placeholder:text-slate-500 focus:outline-none focus:ring-0 focus-visible:outline-none"
                  value={draft}
                  onChange={(e) => setDraft(e.target.value)}
                  onKeyDown={onNamespaceKey}
                  onBlur={commitDraft}
                  placeholder={namespaces.length ? 'Add another…' : 'Type a namespace and press Enter'}
                />
              </div>
            </FormField>
            <FormField label="Default private location" description="Pre-selected when you approve checks of cluster-internal targets. Optional.">
              <select className="input" value={locationId} onChange={(e) => setLocationId(e.target.value)}>
                <option value="">None — choose per check</option>
                {locations.filter((l) => l.enabled).map((l) => <option key={l.id} value={l.id}>{l.name}</option>)}
              </select>
            </FormField>
            <p className="text-xs text-slate-500">Nothing is monitored automatically: you review every suggestion before a monitor is created.</p>
          </div>
        ) : integration && commands && (
          <div className="space-y-5">
            <div className="flex gap-3 rounded-lg border border-amber-500/25 bg-amber-500/[0.08] px-3 py-2.5 text-xs text-amber-200">
              <KeyRound className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
              <p>The manifest contains a collector credential that is shown <strong>only now</strong>. Download it before closing — otherwise you will have to rotate the credential. Keep the file private and delete it after applying.</p>
            </div>
            <div className="grid gap-4 sm:grid-cols-2">
              <FormField label="Probara API origin" required error={touched ? fieldErrors.origin : undefined} description="HTTPS origin the cluster can reach.">
                <input className="input" value={origin} onChange={(e) => setOrigin(e.target.value)} placeholder="https://probara.example.com" />
              </FormField>
              <FormField label="Collector image" required error={touched ? fieldErrors.image : undefined} description="The discovery-collector image from this release.">
                <input className="input font-mono" value={image} onChange={(e) => setImage(e.target.value)} placeholder="ghcr.io/…/probara-discovery-collector:tag" />
              </FormField>
            </div>
            <div className="space-y-3">
              <CommandBlock label="1. Apply the manifest" value={commands.apply} />
              <CommandBlock label="2. Check the first scan" value={commands.logs} />
            </div>
            <p className="text-xs text-slate-500">
              Runs in <span className="font-mono text-slate-400">{integration.namespaces[0]}</span> with list-only access to Services and Ingresses in {integration.namespaces.length === 1 ? 'that namespace' : `${integration.namespaces.length} namespaces`}. The first scan starts immediately, then every 5 minutes.
            </p>
          </div>
        )}
      </DiscoveryModal>
      <ModalPortal>
      <ConfirmDialog
        open={confirmClose}
        title="Close without the manifest?"
        description="The collector credential cannot be shown again. You can rotate it later to get a new manifest."
        confirmLabel="Close anyway"
        onConfirm={() => { setConfirmClose(false); onClose(); }}
        onCancel={() => setConfirmClose(false)}
      />
      </ModalPortal>
    </>
  );
}

function Steps({ step }: { step: number }) {
  const items = ['Cluster', 'Install collector'];
  return (
    <ol className="mt-1 flex items-center gap-3">
      {items.map((label, i) => {
        const n = i + 1;
        const state = n < step ? 'done' : n === step ? 'current' : 'todo';
        return (
          <li key={label} className="flex items-center gap-1.5">
            <span className={`inline-flex h-4 w-4 items-center justify-center rounded-full text-[10px] font-semibold ${state === 'todo' ? 'border border-white/15 text-slate-500' : 'bg-cyan-500 text-white'}`}>
              {state === 'done' ? <Check className="h-2.5 w-2.5" aria-hidden="true" /> : n}
            </span>
            <span className={state === 'current' ? 'text-slate-200' : 'text-slate-500'}>{label}</span>
          </li>
        );
      })}
    </ol>
  );
}

export function CommandBlock({ label, value }: { label: string; value: string }) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try { await navigator.clipboard.writeText(value); setCopied(true); setTimeout(() => setCopied(false), 1800); } catch { /* clipboard unavailable */ }
  };
  return (
    <div>
      <div className="mb-1.5 flex items-center justify-between">
        <span className="text-xs font-medium text-slate-400">{label}</span>
        <Button variant="ghost" size="xs" icon={copied ? <Check strokeWidth={2} /> : <Copy strokeWidth={1.75} />} onClick={() => void copy()}>{copied ? 'Copied' : 'Copy'}</Button>
      </div>
      <pre className="overflow-x-auto whitespace-pre-wrap break-all rounded-lg border border-white/[0.06] bg-slate-950/60 px-3 py-2.5 font-mono text-xs text-slate-200">{value}</pre>
    </div>
  );
}
