'use client';

import { FormEvent, useId, useState } from 'react';
import { Check, Copy, Download, KeyRound } from 'lucide-react';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import FormField from '@/components/ui/FormField';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import Dialog from '@/components/ui/Dialog';
import Input from '@/components/ui/Input';
import Select from '@/components/ui/Select';
import { SELECTED_SEGMENT } from '@/components/ui/roving';
import NamespaceChipsInput from '@/components/discovery/NamespaceChipsInput';
import { saveDownload } from '@/components/slas/SlaVisuals';
import { createDiscoveryIntegration } from '@/lib/api';
import {
  collectorManifest, configuredNamespaces, initialCollectorImage, installCommands, manifestInputErrors, MAX_NAMESPACES, namespaceFieldError,
  parseNamespaces, validNamespace, type DiscoveryIntegration,
} from '@/lib/discovery';
import { errorMessage } from '@/lib/errors';
import type { Location } from '@/lib/types';
import { useClipboard } from '@/lib/useClipboard';

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
  const formId = useId();
  const [integration, setIntegration] = useState<(DiscoveryIntegration & { token: string }) | null>(install ?? null);
  const [name, setName] = useState('');
  const [namespaces, setNamespaces] = useState<string[]>([]);
  const [draft, setDraft] = useState('');
  const [locationId, setLocationId] = useState('');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [origin, setOrigin] = useState(defaultOrigin);
  // The published repository with the tag left to type (a remembered published
  // tag is dropped: it names the previous release), or a remembered own build.
  const [image, setImage] = useState(() => initialCollectorImage(remembered(IMAGE_KEY)));
  const [touched, setTouched] = useState(false);
  const [downloaded, setDownloaded] = useState(false);
  const [confirmClose, setConfirmClose] = useState(false);

  const draftNamespaces = parseNamespaces(draft);
  const allNamespaces = [...new Set([...namespaces, ...draftNamespaces])];
  // The draft counts toward the limit but is name-checked only once committed
  // (no error after each hyphen of `kube-system`); submit still refuses it.
  const namespaceError = namespaceFieldError(namespaces, draftNamespaces);
  const canCreate = !saving && name.trim() !== '' && allNamespaces.length > 0 && allNamespaces.every(validNamespace) && !namespaceError;

  const create = async (event: FormEvent) => {
    event.preventDefault();
    if (!canCreate) return;
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
      saveDownload({ blob: new Blob([content], { type: 'application/yaml' }), filename: installCommands(integration).file });
      remember(ORIGIN_KEY, new URL(origin).origin); remember(IMAGE_KEY, image.trim());
      setDownloaded(true);
    } catch (e) {
      setError(errorMessage(e));
    }
  };
  const requestClose = () => { if (integration && !downloaded) setConfirmClose(true); else onClose(); };

  const step = integration ? 2 : 1;
  // Step 2 opens on the first field still to fill: a missing origin, else a
  // prefilled image that still needs its tag.
  const focusImage = !fieldErrors.origin && fieldErrors.image !== undefined && image.trim().endsWith(':');
  const commands = integration ? installCommands(integration) : null;
  const readable = integration ? configuredNamespaces(integration).length : 0;

  return (
    <>
      <Dialog
        title={install ? `Reinstall collector · ${install.name}` : 'Connect a Kubernetes cluster'}
        description={install ? undefined : <Steps step={step} />}
        onClose={requestClose}
        busy={saving}
        size="lg"
        bodyClassName="dashboard-scroll space-y-5"
        footer={step === 1 ? <>
          <Button variant="ghost" size="sm" onClick={onClose} disabled={saving}>Cancel</Button>
          <Button type="submit" form={formId} variant="accent" size="sm" disabled={!canCreate} loading={saving}>Create and continue</Button>
        </> : <>
          <Button variant="ghost" size="sm" onClick={requestClose}>{downloaded ? 'Done' : 'Close'}</Button>
          <Button variant="accent" size="sm" icon={<Download strokeWidth={1.75} />} onClick={download}>{downloaded ? 'Download again' : 'Download manifest'}</Button>
        </>}
      >
        {error && <Callout tone="danger">{error}</Callout>}
        {step === 1 ? (
          <form id={formId} noValidate onSubmit={(e) => void create(e)} className="space-y-5">
            <FormField label="Cluster name" required>
              <Input value={name} maxLength={100} onChange={(e) => setName(e.target.value)} placeholder="e.g. production-eu" autoFocus />
            </FormField>
            <FormField
              label="Namespaces"
              required
              description={`The collector lists Services and Ingresses in these namespaces only (up to ${MAX_NAMESPACES}) and runs in the first one. They must already exist; you can add or remove them later.`}
              error={namespaceError}
            >
              {(field) => (
                <NamespaceChipsInput field={field} value={namespaces} onChange={setNamespaces} draft={draft} onDraftChange={setDraft} />
              )}
            </FormField>
            <FormField label="Default private location" description="Pre-selected when you approve checks of cluster-internal targets. Optional.">
              <Select value={locationId} onChange={(e) => setLocationId(e.target.value)}>
                <option value="">None — choose per check</option>
                {locations.filter((l) => l.enabled).map((l) => <option key={l.id} value={l.id}>{l.name}</option>)}
              </Select>
            </FormField>
            <p className="text-xs text-slate-500">Nothing is monitored automatically: you review every suggestion before a monitor is created.</p>
          </form>
        ) : integration && commands && (
          <div className="space-y-5">
            <Callout tone="warning" icon={<KeyRound strokeWidth={1.75} />}>
              The manifest contains a collector credential that is shown <strong className="font-semibold text-white">only now</strong>. Download it before closing — otherwise you will have to rotate the credential. Keep the file private and delete it after applying.
            </Callout>
            <div className="grid gap-4 sm:grid-cols-2">
              <FormField label="Probara API origin" required error={touched ? fieldErrors.origin : undefined} description="HTTPS origin the cluster can reach.">
                <Input className="font-mono" value={origin} onChange={(e) => setOrigin(e.target.value)} placeholder="https://probara.example.com" autoFocus={!origin.trim()} />
              </FormField>
              <FormField label="Collector image" required error={touched ? fieldErrors.image : undefined} description="The discovery-collector image tagged with this release’s version.">
                <Input
                  className="font-mono"
                  value={image}
                  onChange={(e) => setImage(e.target.value)}
                  placeholder="ghcr.io/…/probara-discovery-collector:tag"
                  autoFocus={focusImage}
                  // Caret after the prefilled repository, ready for the tag.
                  onFocus={(e) => { const el = e.currentTarget; if (el.value.endsWith(':')) el.setSelectionRange(el.value.length, el.value.length); }}
                />
              </FormField>
            </div>
            <div className="space-y-3">
              <CommandBlock label="1. Apply the manifest" value={commands.apply} />
              <CommandBlock label="2. Check the first scan" value={commands.logs} />
            </div>
            <p className="text-xs text-slate-500">
              Runs in <span className="font-mono text-slate-300">{integration.home_namespace}</span> with list-only access to Services and Ingresses in {readable === 1 ? 'that namespace' : `${readable} namespaces`}. The first scan starts immediately, then every 5 minutes.
            </p>
          </div>
        )}
      </Dialog>
      <ConfirmDialog
        open={confirmClose}
        title="Close without the manifest?"
        description="The collector credential cannot be shown again. You can rotate it later to get a new manifest."
        confirmLabel="Close anyway"
        onConfirm={() => { setConfirmClose(false); onClose(); }}
        onCancel={() => setConfirmClose(false)}
      />
    </>
  );
}

/** Step indicator for the discovery dialogs' description line; `step` is 1-based. */
export function Steps({ step, items = ['Cluster', 'Install collector'] }: { step: number; items?: string[] }) {
  return (
    <ol className="flex items-center gap-3 text-xs">
      {items.map((label, i) => {
        const n = i + 1;
        const state = n < step ? 'done' : n === step ? 'current' : 'todo';
        return (
          <li key={label} className="flex items-center gap-1.5">
            <span className={`inline-flex h-5 w-5 items-center justify-center rounded-full text-2xs font-semibold tabular-nums ${state === 'todo' ? 'border border-edge-control text-slate-500' : SELECTED_SEGMENT}`}>
              {state === 'done' ? <Check className="h-3 w-3" strokeWidth={2.25} aria-hidden="true" /> : n}
            </span>
            <span className={state === 'current' ? 'font-medium text-white' : 'text-slate-500'} aria-current={state === 'current' ? 'step' : undefined}>{label}</span>
          </li>
        );
      })}
    </ol>
  );
}

export function CommandBlock({ label, value }: { label: string; value: string }) {
  const { copied, copy } = useClipboard();
  return (
    <div>
      <div className="mb-1.5 flex items-center justify-between">
        <span className="text-ui font-medium text-slate-300">{label}</span>
        <Button variant="subtle" size="xs" icon={copied ? <Check strokeWidth={2} /> : <Copy strokeWidth={1.75} />} onClick={() => void copy(value)}>{copied ? 'Copied' : 'Copy'}</Button>
      </div>
      <pre className="glass-well overflow-x-auto whitespace-pre-wrap break-all px-3 py-2.5 font-mono text-xs text-slate-200">{value}</pre>
    </div>
  );
}
