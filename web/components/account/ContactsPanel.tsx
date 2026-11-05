'use client';

import { useCallback, useEffect, useState } from 'react';
import { Mail, MessageSquare, Plus, Send, Trash2 } from 'lucide-react';
import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
import IconButton from '@/components/ui/IconButton';
import Pill from '@/components/ui/Pill';
import Callout from '@/components/ui/Callout';
import Dialog from '@/components/ui/Dialog';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import FormField from '@/components/ui/FormField';
import Input from '@/components/ui/Input';
import SegmentedControl from '@/components/ui/SegmentedControl';
import { useToast } from '@/components/ui/ToastProvider';
import {
  createMyContact, deleteMyContact, getMyContacts, sendContactCode, testContact, verifyContact,
} from '@/lib/api';
import {
  CONTACT_KIND_LABELS, CONTACT_PLACEHOLDERS, codePending, contactAddressError, resendWait, transportHint,
  transportReady, type ContactKind, type ContactTransports, type UserContact,
} from '@/lib/contacts';
import { formatDateTime } from '@/lib/format';

const KIND_OPTIONS = [
  { value: 'email' as const, label: 'Email' },
  { value: 'sms' as const, label: 'SMS' },
];

/**
 * The signed-in user's verified contacts: where Probara can reach this person
 * directly. Adding one sends a code to the address; it counts only once the
 * code is entered. Email codes go through the installation's mailer, SMS
 * codes through the selected workspace's SMS channel for people.
 */
export default function ContactsPanel() {
  const { showToast } = useToast();
  const [contacts, setContacts] = useState<UserContact[]>([]);
  const [transports, setTransports] = useState<ContactTransports | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [adding, setAdding] = useState(false);
  const [verifying, setVerifying] = useState<UserContact | null>(null);
  const [removing, setRemoving] = useState<UserContact | null>(null);
  const [busy, setBusy] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      setLoading(true);
      setError('');
      const res = await getMyContacts();
      setContacts(res.items || []);
      setTransports(res.transports);
    } catch (err: any) {
      setError(err.message || 'Failed to load contacts');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  const replace = (c: UserContact) => setContacts((prev) => prev.map((x) => (x.id === c.id ? c : x)));

  const sendCode = async (c: UserContact) => {
    setBusy(c.id);
    try {
      const updated = await sendContactCode(c.id);
      replace(updated);
      setVerifying(updated);
      showToast(`Code sent to ${updated.address}`, 'success');
    } catch (err: any) {
      showToast(err.message || 'Failed to send the code', 'error');
    } finally {
      setBusy(null);
    }
  };

  const sendTest = async (c: UserContact) => {
    setBusy(c.id);
    try {
      await testContact(c.id);
      showToast(`Test message sent to ${c.address}`, 'success');
    } catch (err: any) {
      showToast(err.message || 'Failed to send the test', 'error');
    } finally {
      setBusy(null);
    }
  };

  const remove = async () => {
    if (!removing) return;
    setBusy(removing.id);
    try {
      await deleteMyContact(removing.id);
      setContacts((prev) => prev.filter((x) => x.id !== removing.id));
      setRemoving(null);
      showToast('Contact removed', 'success');
    } catch (err: any) {
      showToast(err.message || 'Failed to remove the contact', 'error');
    } finally {
      setBusy(null);
    }
  };

  return (
    <Panel
      title="Contacts"
      subtitle="Verified addresses where you can be reached directly. On-call schedules show who has one; paging people through them comes with escalation policies."
      actions={
        <Button size="xs" icon={<Plus strokeWidth={1.75} />} onClick={() => setAdding(true)} disabled={loading}>
          Add contact
        </Button>
      }
    >
      {loading ? (
        <p className="text-sm text-slate-500">Loading contacts…</p>
      ) : error ? (
        <Callout tone="danger" action={<Button variant="ghost" size="xs" onClick={load}>Retry</Button>}>
          {error}
        </Callout>
      ) : contacts.length === 0 ? (
        <p className="text-sm text-slate-400">No contacts yet. Add an email address or a mobile number and confirm it with the code we send.</p>
      ) : (
        <ul className="-mx-5 -mb-5 divide-y divide-divider">
          {contacts.map((c) => {
            const Icon = c.kind === 'email' ? Mail : MessageSquare;
            const ready = transportReady(c.kind, transports);
            return (
              <li key={c.id} className="flex flex-col gap-3 px-5 py-3.5 sm:flex-row sm:items-center sm:justify-between">
                <div className="flex min-w-0 items-start gap-3">
                  <Icon className="mt-0.5 h-4 w-4 flex-shrink-0 text-slate-500" strokeWidth={1.75} aria-hidden="true" />
                  <div className="min-w-0">
                    <p className="flex flex-wrap items-center gap-2 text-sm font-medium text-white">
                      <span className="truncate font-mono">{c.address}</span>
                      {c.verified ? (
                        <Pill tone="success" size="xs">Verified</Pill>
                      ) : (
                        <Pill tone="warning" size="xs">Unverified</Pill>
                      )}
                    </p>
                    <p className="mt-0.5 flex flex-wrap gap-x-3 text-xs text-slate-500">
                      <span>{CONTACT_KIND_LABELS[c.kind]}</span>
                      {c.label && <span>{c.label}</span>}
                      {c.verified_at && <span>Verified {formatDateTime(c.verified_at)}</span>}
                      {!c.verified && !ready && <span className="text-amber-300">{transportHint(c.kind)}</span>}
                    </p>
                  </div>
                </div>
                <div className="flex flex-shrink-0 items-center gap-2">
                  {c.verified ? (
                    <IconButton
                      icon={<Send strokeWidth={1.75} />}
                      label="Send a test message"
                      onClick={() => sendTest(c)}
                      disabled={busy === c.id || !ready}
                    />
                  ) : codePending(c) ? (
                    <Button size="xs" onClick={() => setVerifying(c)}>Enter code</Button>
                  ) : (
                    <Button size="xs" onClick={() => sendCode(c)} loading={busy === c.id} disabled={!ready}>
                      Send code
                    </Button>
                  )}
                  <IconButton
                    icon={<Trash2 strokeWidth={1.75} />}
                    label="Remove contact"
                    onClick={() => setRemoving(c)}
                    danger
                  />
                </div>
              </li>
            );
          })}
        </ul>
      )}

      {adding && (
        <AddContactDialog
          transports={transports}
          onCancel={() => setAdding(false)}
          onAdded={async (c) => {
            setAdding(false);
            setContacts((prev) => [...prev, c]);
            if (transportReady(c.kind, transports)) await sendCode(c);
          }}
        />
      )}

      {verifying && (
        <VerifyDialog
          contact={verifying}
          onResend={() => sendCode(verifying)}
          onCancel={() => setVerifying(null)}
          onVerified={(c) => {
            replace(c);
            setVerifying(null);
            showToast('Contact verified', 'success');
          }}
        />
      )}

      <ConfirmDialog
        open={removing !== null}
        title="Remove contact?"
        description={removing ? `${removing.address} will no longer be used to reach you.` : ''}
        confirmLabel="Remove"
        tone="danger"
        loading={busy !== null && busy === removing?.id}
        onConfirm={remove}
        onCancel={() => setRemoving(null)}
      />
    </Panel>
  );
}

function AddContactDialog({
  transports,
  onCancel,
  onAdded,
}: {
  transports: ContactTransports | null;
  onCancel: () => void;
  onAdded: (c: UserContact) => void;
}) {
  const [kind, setKind] = useState<ContactKind>('email');
  const [address, setAddress] = useState('');
  const [label, setLabel] = useState('');
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);

  const save = async () => {
    const problem = contactAddressError(kind, address);
    if (problem) {
      setError(problem);
      return;
    }
    setSaving(true);
    setError('');
    try {
      onAdded(await createMyContact({ kind, address: address.trim(), label: label.trim() || undefined }));
    } catch (err: any) {
      setError(err.message || 'Failed to add the contact');
      setSaving(false);
    }
  };

  return (
    <Dialog
      title="Add contact"
      description="We send a code to the address; enter it to verify the contact."
      onClose={onCancel}
      busy={saving}
      footer={
        <>
          <Button variant="ghost" onClick={onCancel} disabled={saving}>Cancel</Button>
          <Button variant="accent" onClick={save} loading={saving}>Add and send code</Button>
        </>
      }
    >
      <form
        noValidate
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault();
          save();
        }}
      >
        <SegmentedControl options={KIND_OPTIONS} value={kind} onChange={(k) => { setKind(k); setError(''); }} label="Contact type" />
        {!transportReady(kind, transports) && <Callout tone="warning">{transportHint(kind)}</Callout>}
        <FormField label={kind === 'email' ? 'Email address' : 'Mobile number'} required error={error}>
          <Input
            type={kind === 'email' ? 'email' : 'tel'}
            autoFocus
            value={address}
            placeholder={CONTACT_PLACEHOLDERS[kind]}
            onChange={(e) => { setAddress(e.target.value); setError(''); }}
          />
        </FormField>
        <FormField label="Label" description="Optional, e.g. Work phone.">
          <Input value={label} maxLength={100} onChange={(e) => setLabel(e.target.value)} />
        </FormField>
      </form>
    </Dialog>
  );
}

function VerifyDialog({
  contact,
  onResend,
  onCancel,
  onVerified,
}: {
  contact: UserContact;
  onResend: () => void;
  onCancel: () => void;
  onVerified: (c: UserContact) => void;
}) {
  const [code, setCode] = useState('');
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);
  const [now, setNow] = useState(() => new Date());
  const wait = resendWait(contact, now);

  useEffect(() => {
    if (wait <= 0) return;
    const t = setInterval(() => setNow(new Date()), 1000);
    return () => clearInterval(t);
  }, [wait]);

  const submit = async () => {
    if (!/^\d{6}$/.test(code.trim())) {
      setError('Enter the 6-digit code.');
      return;
    }
    setSaving(true);
    setError('');
    try {
      onVerified(await verifyContact(contact.id, code.trim()));
    } catch (err: any) {
      setError(err.message || 'Verification failed');
      setSaving(false);
    }
  };

  return (
    <Dialog
      title="Enter the code"
      description={`We sent a 6-digit code to ${contact.address}. It expires in 15 minutes.`}
      onClose={onCancel}
      busy={saving}
      footer={
        <>
          <Button variant="ghost" onClick={onResend} disabled={saving || wait > 0}>
            {wait > 0 ? `Resend in ${wait}s` : 'Resend code'}
          </Button>
          <Button variant="accent" onClick={submit} loading={saving}>Verify</Button>
        </>
      }
    >
      <form
        noValidate
        onSubmit={(e) => {
          e.preventDefault();
          submit();
        }}
      >
        <FormField label="Verification code" required error={error}>
          <Input
            autoFocus
            inputMode="numeric"
            autoComplete="one-time-code"
            maxLength={6}
            value={code}
            className="font-mono tracking-widest"
            onChange={(e) => { setCode(e.target.value.replace(/\D/g, '')); setError(''); }}
          />
        </FormField>
      </form>
    </Dialog>
  );
}
