'use client';

import { FormEvent, useEffect, useId, useState } from 'react';
import { Download, Minus, Plus } from 'lucide-react';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import Dialog from '@/components/ui/Dialog';
import FormField from '@/components/ui/FormField';
import Input from '@/components/ui/Input';
import Select from '@/components/ui/Select';
import { saveDownload } from '@/components/slas/SlaVisuals';
import NamespaceChipsInput from '@/components/discovery/NamespaceChipsInput';
import { CommandBlock, Steps } from '@/components/discovery/ConnectClusterModal';
import { getDiscoveryNamespaces, updateDiscoveryIntegration } from '@/lib/api';
import {
  accessCleanupCommand, accessManifestCommands, ADDED_NAMESPACE_TEXT, collectorAccessManifest, configuredNamespaces, errorMessage,
  integrationEditErrors, integrationPatch, MAX_NAMESPACES, namespaceDelta, namespaceFieldError, parseNamespaces, removedNamespaceText,
  withHomeLocked, type DiscoveryIntegration, type DiscoveryNamespace,
} from '@/lib/discovery';
import { TONE_TEXT } from '@/lib/status-tones';
import type { Location } from '@/lib/types';

type Step = 'edit' | 'review' | 'apply';
const STEPS: Step[] = ['edit', 'review', 'apply'];
const STEP_LABELS = ['Edit', 'Review', 'Apply'];

interface EditIntegrationModalProps {
  integration: DiscoveryIntegration;
  locations: Location[];
  /** Called with the saved integration (no token) as soon as the PATCH succeeds. */
  onSaved: (integration: DiscoveryIntegration) => void;
  onClose: () => void;
}

/**
 * Edit a cluster's name, default location and namespaces. A namespace change
 * goes through Review (what leaves coverage, from GET …/namespaces) and then
 * Apply (the token-free access manifest and the cleanup command), since the
 * collector can only read a new namespace once its Role is applied.
 */
export default function EditIntegrationModal({ integration, locations, onSaved, onClose }: EditIntegrationModalProps) {
  const formId = useId();
  const home = integration.home_namespace;
  const [name, setName] = useState(integration.name);
  const [locationId, setLocationId] = useState(integration.location_id ?? '');
  const [namespaces, setNamespaces] = useState(() => withHomeLocked(home, configuredNamespaces(integration)));
  const [draft, setDraft] = useState('');
  const [attempted, setAttempted] = useState(false);
  const [step, setStep] = useState<Step>('edit');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [saved, setSaved] = useState<{ integration: DiscoveryIntegration; add: string[]; remove: string[] } | null>(null);
  const [downloaded, setDownloaded] = useState(false);

  // Per-namespace service and linked-service counts for the review; a failure leaves the
  // review's wording generic rather than blocking the edit.
  const [counts, setCounts] = useState<Map<string, DiscoveryNamespace> | null>(null);
  useEffect(() => {
    let live = true;
    getDiscoveryNamespaces(integration.id)
      .then((v) => { if (live) setCounts(new Map(v.items.map((item) => [item.namespace, item]))); })
      .catch(() => {});
    return () => { live = false; };
  }, [integration.id]);

  // The page's integration list is not reloaded by its coverage poll, so a
  // namespace promoted since it loaded would still read pending there; the
  // namespaces GET fetched on open is fresher and wins once it loads.
  const isPending = (ns: string) => {
    const row = counts?.get(ns);
    return row ? row.state === 'pending' : (integration.pending_namespaces ?? []).includes(ns);
  };

  const draftNamespaces = parseNamespaces(draft);
  const values = { name, locationId, namespaces: [...new Set([...namespaces, ...draftNamespaces])] };
  const errors = integrationEditErrors({ ...values, namespaces }, draftNamespaces);
  const patch = integrationPatch(integration, values);
  const delta = namespaceDelta(integration, values.namespaces);
  const namespacesChanged = delta.add.length + delta.remove.length > 0;
  // The draft counts toward the limit live; names are checked once committed,
  // and everything once a submit was attempted (as the monitor forms do).
  const namespaceError = attempted ? errors.namespaces : namespaceFieldError(namespaces, draftNamespaces);

  const save = async () => {
    if (!patch) return;
    setSaving(true); setError('');
    try {
      const updated = await updateDiscoveryIntegration(integration.id, patch);
      onSaved(updated);
      if (namespacesChanged) { setSaved({ integration: updated, add: delta.add, remove: delta.remove }); setStep('apply'); }
      else onClose();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setSaving(false);
    }
  };

  const submitEdit = (event: FormEvent) => {
    event.preventDefault();
    setAttempted(true);
    if (errors.name || errors.namespaces || !patch) return;
    if (draftNamespaces.length) { setNamespaces(values.namespaces); setDraft(''); }
    setError('');
    if (namespacesChanged) setStep('review');
    else void save();
  };

  const download = () => {
    if (!saved) return;
    const content = collectorAccessManifest(saved.integration, configuredNamespaces(saved.integration));
    saveDownload({ blob: new Blob([content], { type: 'application/yaml' }), filename: accessManifestCommands(saved.integration).file });
    setDownloaded(true);
  };

  const stepNumber = STEPS.indexOf(step) + 1;
  // A disabled location stays selectable while it is the current one, so the
  // select never shows a value it has no option for.
  const locationOptions = locations.filter((l) => l.enabled || l.id === integration.location_id);

  const footer = step === 'edit' ? <>
    <Button variant="ghost" size="sm" onClick={onClose} disabled={saving}>Cancel</Button>
    <Button type="submit" form={formId} variant="accent" size="sm" disabled={!patch} loading={saving}>{namespacesChanged ? 'Review changes' : 'Save'}</Button>
  </> : step === 'review' ? <>
    <Button variant="ghost" size="sm" onClick={() => { setError(''); setStep('edit'); }} disabled={saving}>Back</Button>
    <Button variant="accent" size="sm" onClick={() => void save()} loading={saving}>Save changes</Button>
  </> : <>
    <Button variant={saved?.add.length ? 'ghost' : 'accent'} size="sm" onClick={onClose}>Done</Button>
    {saved && saved.add.length > 0 && (
      <Button variant="accent" size="sm" icon={<Download strokeWidth={1.75} />} onClick={download}>{downloaded ? 'Download again' : 'Download access manifest'}</Button>
    )}
  </>;

  return (
    <Dialog
      title={`Edit ${integration.name}`}
      description={namespacesChanged || step !== 'edit' ? <Steps step={stepNumber} items={STEP_LABELS} /> : undefined}
      onClose={onClose}
      busy={saving}
      size="lg"
      bodyClassName="dashboard-scroll space-y-5"
      footer={footer}
    >
      {error && <Callout tone="danger">{error}</Callout>}
      {step === 'edit' && (
        <form id={formId} noValidate onSubmit={submitEdit} className="space-y-5">
          <FormField label="Cluster name" required error={attempted ? errors.name : undefined}>
            <Input value={name} maxLength={100} onChange={(e) => setName(e.target.value)} placeholder="e.g. production-eu" />
          </FormField>
          <FormField label="Default private location" description="Pre-selected when you approve checks of cluster-internal targets.">
            <Select value={locationId} onChange={(e) => setLocationId(e.target.value)}>
              <option value="">None — choose per check</option>
              {locationOptions.map((l) => <option key={l.id} value={l.id}>{l.name}</option>)}
            </Select>
          </FormField>
          <FormField
            label="Namespaces"
            required
            description={`Up to ${MAX_NAMESPACES}. ${home} is locked: the collector runs there. A new namespace is scanned once the collector can read it.`}
            error={namespaceError}
          >
            {(field) => (
              <NamespaceChipsInput
                field={field}
                value={namespaces}
                onChange={(next) => setNamespaces(withHomeLocked(home, next))}
                draft={draft}
                onDraftChange={setDraft}
                locked={{ namespace: home, reason: 'The collector runs here' }}
                chipNote={(ns) => isPending(ns) ? 'pending' : undefined}
              />
            )}
          </FormField>
        </form>
      )}

      {step === 'review' && (
        <ul className="space-y-3">
          {delta.add.map((ns) => (
            <ReviewRow key={`add-${ns}`} kind="add" namespace={ns} text={ADDED_NAMESPACE_TEXT} />
          ))}
          {delta.remove.map((ns) => (
            <ReviewRow
              key={`remove-${ns}`}
              kind="remove"
              namespace={ns}
              // Loaded counts with no row for the namespace: nothing was discovered there.
              text={removedNamespaceText(ns, counts ? counts.get(ns) ?? { services: 0, linked_services: 0 } : undefined)}
            />
          ))}
        </ul>
      )}

      {step === 'apply' && saved && (
        <div className="space-y-4">
          {saved.add.length > 0 && (
            <CommandBlock label="1. Apply the access manifest" value={accessManifestCommands(saved.integration).apply} />
          )}
          {saved.remove.length > 0 && (
            <CommandBlock
              label={`${saved.add.length > 0 ? '2. ' : ''}Remove access from ${saved.remove.length === 1 ? 'the removed namespace' : 'removed namespaces'}`}
              value={accessCleanupCommand(saved.integration, saved.remove)}
            />
          )}
          <p className="text-xs text-slate-500">
            {saved.add.length > 0 && 'The access manifest holds only Roles and RoleBindings, no credential: re-apply it whenever the namespaces change. '}
            Takes effect at the next scan (≤5 min).
          </p>
        </div>
      )}
    </Dialog>
  );
}

function ReviewRow({ kind, namespace, text }: { kind: 'add' | 'remove'; namespace: string; text: string }) {
  const Icon = kind === 'add' ? Plus : Minus;
  return (
    <li className="glass-well flex items-start gap-3 px-4 py-3">
      <Icon className={`mt-0.5 h-4 w-4 flex-shrink-0 ${kind === 'add' ? TONE_TEXT.success : TONE_TEXT.warning}`} strokeWidth={2} aria-hidden="true" />
      <div className="min-w-0 text-sm">
        <p className="text-slate-300">
          {kind === 'add' ? 'Adding' : 'Removing'} <span className="font-mono text-white">{namespace}</span>
        </p>
        <p className="mt-0.5 text-xs text-slate-400">{text}</p>
      </div>
    </li>
  );
}
