'use client';

import { ChevronDown, ShieldCheck } from 'lucide-react';
import FormField from '@/components/ui/FormField';
import Pill from '@/components/ui/Pill';
import { FieldHint } from '@/components/monitors/form-kit';
import { FOCUS_RING_INSET } from '@/components/ui/focus';
import { type DatabaseFormData, type SetDatabaseFormData, type ClearedDatabaseSecrets } from '@/components/monitors/database/types';

interface DatabaseTlsMaterialProps {
  formData: DatabaseFormData;
  setFormData: SetDatabaseFormData;
  errors: Record<string, string>;
  /** A stored client key exists and was not cleared. */
  hasStoredClientKey: boolean;
  cleared: ClearedDatabaseSecrets;
  setCleared: (cleared: ClearedDatabaseSecrets) => void;
  hasTLSMaterial: boolean;
  certsOpen: boolean;
  setCertsOpen: (open: boolean) => void;
}

/** The Target panel's TLS certificates disclosure: CA, client certificate and client key (PEM). */
export default function DatabaseTlsMaterial({
  formData,
  setFormData,
  errors,
  hasStoredClientKey,
  cleared,
  setCleared,
  hasTLSMaterial,
  certsOpen,
  setCertsOpen,
}: DatabaseTlsMaterialProps) {
  return (
    <div className="glass-well">
      <button
        type="button"
        onClick={() => setCertsOpen(!certsOpen)}
        className={`flex w-full items-center justify-between gap-3 rounded-[inherit] px-4 py-3 text-left transition-colors hover:bg-well-hover ${FOCUS_RING_INSET}`}
        aria-expanded={certsOpen}
        aria-controls="db-tls-material"
      >
        <span className="flex min-w-0 flex-wrap items-center gap-2 text-ui font-medium text-slate-200">
          <ShieldCheck className="h-4 w-4 text-slate-400" strokeWidth={1.75} />
          Custom certificates
          <span className="text-xs font-normal text-slate-500">private CA / mutual TLS</span>
          {hasTLSMaterial && (
            <Pill tone="neutral" size="xs">
              configured
            </Pill>
          )}
        </span>
        <ChevronDown
          className={`h-4 w-4 shrink-0 text-slate-400 transition-transform duration-base ease-standard motion-reduce:transition-none ${certsOpen ? 'rotate-180' : ''}`}
          strokeWidth={1.75}
          aria-hidden="true"
        />
      </button>
      {certsOpen && (
        <div id="db-tls-material" className="space-y-4 px-4 pb-4 pt-1">
          <FormField label="CA certificate (PEM, optional)">
            {(control) => (
              <>
                <textarea
                  {...control}
                  rows={4}
                  value={formData.tls_ca_pem}
                  onChange={(e) => setFormData({ ...formData, tls_ca_pem: e.target.value })}
                  placeholder={'-----BEGIN CERTIFICATE-----\n…\n-----END CERTIFICATE-----'}
                  spellCheck={false}
                  className="input min-h-[88px] font-mono"
                />
                <FieldHint>Verify a server certificate signed by a private or internal CA</FieldHint>
              </>
            )}
          </FormField>
          <FormField label="Client certificate (PEM, optional)">
            {(control) => (
              <>
                <textarea
                  {...control}
                  rows={4}
                  value={formData.tls_client_cert_pem}
                  onChange={(e) => setFormData({ ...formData, tls_client_cert_pem: e.target.value })}
                  placeholder={'-----BEGIN CERTIFICATE-----\n…\n-----END CERTIFICATE-----'}
                  spellCheck={false}
                  className="input min-h-[88px] font-mono"
                />
                <FieldHint>Only for mutual TLS / X.509 authentication</FieldHint>
              </>
            )}
          </FormField>
          {(formData.tls_client_cert_pem.trim() !== '' || formData.tls_client_key_pem !== '' || hasStoredClientKey) && (
            <FormField
              label="Client key (PEM)"
              error={errors.tls_client_key_pem}
            >
              {hasStoredClientKey && !formData.tls_client_key_pem && (
                <button
                  type="button"
                  onClick={() => setCleared({ ...cleared, clientKey: true })}
                  className="mb-1.5 text-xs text-slate-400 underline-offset-2 hover:text-white hover:underline"
                >
                  Clear the stored key
                </button>
              )}
              <textarea
                rows={4}
                aria-label="Client key (PEM)"
                value={formData.tls_client_key_pem}
                onChange={(e) => setFormData({ ...formData, tls_client_key_pem: e.target.value })}
                placeholder={hasStoredClientKey ? 'Unchanged — paste a new key to replace' : '-----BEGIN PRIVATE KEY-----\n…\n-----END PRIVATE KEY-----'}
                spellCheck={false}
                className="input min-h-[88px] font-mono"
              />
              <p className="mt-1 text-xs text-slate-500">
                {hasStoredClientKey
                  ? 'Stored encrypted — leave blank to keep the current key'
                  : 'Stored encrypted, never displayed again'}
              </p>
            </FormField>
          )}
        </div>
      )}
    </div>
  );
}
