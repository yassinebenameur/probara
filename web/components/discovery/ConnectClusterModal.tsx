'use client';

import { FormEvent, KeyboardEvent, useEffect, useId, useRef, useState } from 'react';
import { Check, Copy, Download, KeyRound, X } from 'lucide-react';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import FormField from '@/components/ui/FormField';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import Dialog from '@/components/ui/Dialog';
import Input from '@/components/ui/Input';
import Select from '@/components/ui/Select';
import { SELECTED_SEGMENT } from '@/components/ui/roving';
import { FOCUS_RING } from '@/components/ui/focus';
import { saveDownload } from '@/components/slas/SlaVisuals';
import { createDiscoveryIntegration } from '@/lib/api';
import {
  collectorManifest, errorMessage, initialCollectorImage, installCommands, manifestInputErrors, MAX_NAMESPACES, namespaceFieldError,
  parseNamespaces, validNamespace, type DiscoveryIntegration,
} from '@/lib/discovery';
import { TONE_TEXT } from '@/lib/status-tones';
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

  // Removing a chip unmounts its focused × button: focus the chip that took
  // its place, else the one before, else the namespace input (as TagFilter).
  const chipsRef = useRef<HTMLDivElement>(null);
  const draftRef = useRef<HTMLInputElement>(null);
  const refocusChip = useRef<number | null>(null);
  useEffect(() => {
    const index = refocusChip.current;
    if (index === null) return;
    refocusChip.current = null;
    const chips = chipsRef.current?.querySelectorAll<HTMLElement>('[data-namespace-remove]');
    (chips?.[Math.min(index, chips.length - 1)] ?? draftRef.current)?.focus();
  }, [namespaces]);

  // Every entry is kept, even past the limit: the field error says how many to
  // remove and canCreate blocks, rather than silently dropping the overflow
  // (namespaces cannot be changed after creation).
  const commitDraft = () => {
    const added = parseNamespaces(draft);
    if (added.length) setNamespaces((current) => [...new Set([...current, ...added])]);
    setDraft('');
  };
  const onNamespaceKey = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter' || e.key === ',' || e.key === ' ') { e.preventDefault(); commitDraft(); }
    if (e.key === 'Backspace' && !draft && namespaces.length) setNamespaces(namespaces.slice(0, -1));
  };
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
              description={`The collector lists Services and Ingresses in these namespaces only (up to ${MAX_NAMESPACES}). They must already exist.`}
              error={namespaceError}
            >
              {(field) => (
                <div ref={chipsRef} className="input flex min-h-10 flex-wrap items-center gap-1.5 !py-1.5 focus-within:!border-[var(--focus)] focus-within:bg-control-hover focus-within:shadow-[0_0_0_1px_var(--focus)]">
                  {namespaces.map((ns, index) => (
                    // Removable chip; an invalid namespace, or one past the
                    // limit, takes Pill's danger tint.
                    <span key={ns} className={`inline-flex h-6 items-center gap-1 rounded-full pl-2.5 pr-0.5 font-mono text-xs ${validNamespace(ns) && index < MAX_NAMESPACES ? 'bg-well-hover text-slate-200' : `bg-rose-500/[0.13] ${TONE_TEXT.danger}`}`}>
                      {ns}
                      <button type="button" data-namespace-remove="" aria-label={`Remove ${ns}`} className={`inline-flex h-5 w-5 items-center justify-center rounded-full text-slate-400 transition-colors hover:bg-control-hover hover:text-white ${FOCUS_RING}`} onClick={() => { refocusChip.current = index; setNamespaces(namespaces.filter((n) => n !== ns)); }}>
                        <X className="h-3 w-3" strokeWidth={2} aria-hidden="true" />
                      </button>
                    </span>
                  ))}
                  <input
                    {...field}
                    ref={draftRef}
                    className="min-w-[8rem] flex-1 border-0 bg-transparent p-0 font-mono text-sm text-white shadow-none outline-none ring-0 placeholder:font-sans placeholder:text-slate-500 focus:outline-none focus:ring-0 focus-visible:outline-none"
                    value={draft}
                    onChange={(e) => setDraft(e.target.value)}
                    onKeyDown={onNamespaceKey}
                    onBlur={commitDraft}
                    placeholder={namespaces.length ? 'Add another…' : 'Type a namespace and press Enter'}
                  />
                </div>
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
              Runs in <span className="font-mono text-slate-300">{integration.namespaces[0]}</span> with list-only access to Services and Ingresses in {integration.namespaces.length === 1 ? 'that namespace' : `${integration.namespaces.length} namespaces`}. The first scan starts immediately, then every 5 minutes.
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

function Steps({ step }: { step: number }) {
  const items = ['Cluster', 'Install collector'];
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
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try { await navigator.clipboard.writeText(value); setCopied(true); setTimeout(() => setCopied(false), 1800); } catch { /* clipboard unavailable */ }
  };
  return (
    <div>
      <div className="mb-1.5 flex items-center justify-between">
        <span className="text-ui font-medium text-slate-300">{label}</span>
        <Button variant="subtle" size="xs" icon={copied ? <Check strokeWidth={2} /> : <Copy strokeWidth={1.75} />} onClick={() => void copy()}>{copied ? 'Copied' : 'Copy'}</Button>
      </div>
      <pre className="glass-well overflow-x-auto whitespace-pre-wrap break-all px-3 py-2.5 font-mono text-xs text-slate-200">{value}</pre>
    </div>
  );
}
