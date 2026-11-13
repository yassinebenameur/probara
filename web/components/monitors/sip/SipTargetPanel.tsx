'use client';

import { type ReactNode } from 'react';
import { SipFormFields } from '@/lib/sip-config';
import FormField from '@/components/ui/FormField';
import { FieldHint, FormPanel, ToggleRow } from '@/components/monitors/form-kit';
import { type SipFormData, type SetSipFormData } from '@/components/monitors/sip/types';

interface SipTargetPanelProps {
  formData: SipFormData;
  setFormData: SetSipFormData;
  errors: Record<string, string>;
  /** Locate via DNS (RFC 3263) is on. */
  rfc3263: boolean;
  defaultPort: number;
  setLocateViaDNS: (on: boolean) => void;
  /** The Test row, at the end of the panel. */
  children: ReactNode;
}

/** SipForm's Target panel: name, host, DNS resolution, port, transport and TLS. */
export default function SipTargetPanel({
  formData,
  setFormData,
  errors,
  rfc3263,
  defaultPort,
  setLocateViaDNS,
  children,
}: SipTargetPanelProps) {
  return (
    <FormPanel title="Target">
      <FormField label="Monitor name" required error={errors.name}>
        <input
          type="text"
          value={formData.name}
          onChange={(e) => setFormData({ ...formData, name: e.target.value })}
          placeholder="My SIP server"
          className="input"
        />
      </FormField>

      <FormField label="Host" required error={errors.host}>
        {(control) => (
          <>
            <input
              {...control}
              type="text"
              value={formData.host}
              onChange={(e) => setFormData({ ...formData, host: e.target.value })}
              placeholder={rfc3263 ? 'example.com' : 'sip.example.com'}
              className="input font-mono"
            />
            <FieldHint>
              {rfc3263 ? 'SIP domain whose NAPTR/SRV records name the servers, or a hostname or IP' : 'Hostname or IP'}
            </FieldHint>
          </>
        )}
      </FormField>

      <ToggleRow
        title="Locate server via DNS (NAPTR/SRV)"
        description="RFC 3263: find the servers from the domain's NAPTR and SRV records and fail over between them"
        checked={rfc3263}
        onChange={setLocateViaDNS}
      />

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <FormField label="Port" required={!rfc3263} error={errors.port}>
          {(control) => (
            <>
              <input
                {...control}
                type="number"
                value={rfc3263 && formData.port === 0 ? '' : formData.port}
                onChange={(e) =>
                  setFormData({ ...formData, port: parseInt(e.target.value) || (rfc3263 ? 0 : defaultPort) })
                }
                placeholder={rfc3263 ? 'from SRV' : undefined}
                min={1}
                max={65535}
                className="input tabular-nums"
              />
              <FieldHint>
                {rfc3263
                  ? 'Leave empty to use SRV; a port skips SRV and dials the host directly'
                  : 'Default: 5060 (5061 for TLS)'}
              </FieldHint>
            </>
          )}
        </FormField>
        <FormField label="Transport">
          <select
            value={formData.transport}
            onChange={(e) => {
              const transport = e.target.value as SipFormFields['transport'];
              setFormData({
                ...formData,
                transport,
                // Track the conventional default port unless the user set a
                // custom one. Under SRV an empty port stays empty.
                port: rfc3263
                  ? formData.port
                  : transport === 'tls' && formData.port === 5060
                  ? 5061
                  : transport !== 'tls' && formData.port === 5061
                  ? 5060
                  : formData.port,
              });
            }}
            className="input"
          >
            {rfc3263 && <option value="auto">Auto (from NAPTR/SRV)</option>}
            <option value="udp">UDP</option>
            <option value="tcp">TCP</option>
            <option value="tls">TLS</option>
          </select>
        </FormField>
      </div>

      {rfc3263 && (
        <FormField label="DNS server" error={errors.nameserver}>
          {(control) => (
            <>
              <input
                {...control}
                type="text"
                value={formData.nameserver}
                onChange={(e) => setFormData({ ...formData, nameserver: e.target.value })}
                placeholder="10.0.0.2 or resolver.internal:5353"
                className="input font-mono"
              />
              <FieldHint>Optional. Defaults to the worker&apos;s resolver.</FieldHint>
            </>
          )}
        </FormField>
      )}

      {formData.transport === 'tls' && (
        <>
          <ToggleRow
            title="Skip certificate verification"
            description="For lab servers with self-signed certificates"
            checked={formData.tls_skip_verify}
            onChange={(v) => setFormData({ ...formData, tls_skip_verify: v })}
            tone="warning"
          />
          <FormField label="TLS server name (SNI)">
            {(control) => (
              <>
                <input
                  {...control}
                  type="text"
                  value={formData.tls_server_name}
                  onChange={(e) => setFormData({ ...formData, tls_server_name: e.target.value })}
                  placeholder="sip.example.com"
                  className="input font-mono"
                />
                <FieldHint>Overrides the name used for SNI and certificate verification. Defaults to the host.</FieldHint>
              </>
            )}
          </FormField>
          <FormField label="Certificate valid for at least" error={errors.tls_min_days_valid}>
            {(control) => (
              <>
                <span className="flex items-center gap-2">
                  <input
                    {...control}
                    type="number"
                    value={formData.tls_min_days_valid}
                    onChange={(e) => setFormData({ ...formData, tls_min_days_valid: e.target.value })}
                    placeholder="14"
                    min={0}
                    className="input !w-28 tabular-nums"
                  />
                  <span className="text-xs text-slate-500">days</span>
                </span>
                {!errors.tls_min_days_valid && (
                  <FieldHint>Open a certificate-expiry alert when fewer days remain; the check itself still passes</FieldHint>
                )}
              </>
            )}
          </FormField>
        </>
      )}

      {children}
    </FormPanel>
  );
}
