'use client';

import { SipFormFields } from '@/lib/sip-config';
import FormField from '@/components/ui/FormField';
import { FieldHint, FormPanel } from '@/components/monitors/form-kit';
import { type SipFormData, type SetSipFormData } from '@/components/monitors/sip/types';

type SipMethod = SipFormFields['method'];

interface SipCheckPanelProps {
  formData: SipFormData;
  setFormData: SetSipFormData;
  errors: Record<string, string>;
  isEditMode: boolean;
  /** The saved monitor is a test call (edit mode locks the method class). */
  storedIsCall: boolean;
  isCall: boolean;
  /** Ring-only call mode. */
  ring: boolean;
  isTenantAdmin: boolean;
  setMethod: (method: SipMethod) => void;
}

/** SipForm's Check panel: method, SIP domain, credentials and the expected status. */
export default function SipCheckPanel({
  formData,
  setFormData,
  errors,
  isEditMode,
  storedIsCall,
  isCall,
  ring,
  isTenantAdmin,
  setMethod,
}: SipCheckPanelProps) {
  const methodHint =
    formData.method === 'register'
      ? 'Sends a query-style REGISTER (no Contact) that exercises the registrar and its digest authentication without creating or removing any bindings.'
      : formData.method === 'invite'
        ? 'Places a real call through the host, which acts as the outbound proxy, and measures post-dial delay and answer time. Digest credentials answer the trunk’s challenges.'
        : 'Sends a SIP OPTIONS request to test server availability without initiating a call. Answers digest challenges when credentials are configured.';
  const methodOptionDisabled = (m: SipMethod) => isEditMode && (storedIsCall ? m !== 'invite' : m === 'invite');

  return (
    <FormPanel title="Check">
      <FormField label="Method">
        {(control) => (
          <>
            <select
              {...control}
              value={formData.method}
              onChange={(e) => setMethod(e.target.value as SipMethod)}
              className="input"
            >
              <option value="options" disabled={methodOptionDisabled('options')}>OPTIONS: server availability ping</option>
              <option value="register" disabled={methodOptionDisabled('register')}>REGISTER: registrar and authentication probe</option>
              {(isTenantAdmin || formData.method === 'invite') && (
                <option value="invite" disabled={methodOptionDisabled('invite')}>Test call (INVITE): places a real call</option>
              )}
            </select>
            <FieldHint>
              {methodHint}
              {isEditMode && ' A saved monitor cannot switch between a test call and OPTIONS/REGISTER: create a new one.'}
            </FieldHint>
          </>
        )}
      </FormField>

      {(formData.method === 'register' || isCall) && (
        <FormField label="SIP domain" error={errors.domain}>
          {(control) => (
            <>
              <input
                {...control}
                type="text"
                value={formData.domain}
                onChange={(e) => setFormData({ ...formData, domain: e.target.value })}
                placeholder="example.com"
                className="input font-mono"
              />
              {!errors.domain && (
                <FieldHint>
                  {isCall
                    ? 'Domain of the called number and the caller ID: a hostname or IP. Defaults to the host.'
                    : 'Domain of the address-of-record. Defaults to the host.'}
                </FieldHint>
              )}
            </>
          )}
        </FormField>
      )}

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <FormField label="Username" error={errors.username}>
          {(control) => (
            <>
              <input
                {...control}
                type="text"
                value={formData.username}
                onChange={(e) => setFormData({ ...formData, username: e.target.value })}
                placeholder="agent42"
                autoComplete="off"
                className="input"
              />
              <FieldHint>Optional digest auth user</FieldHint>
            </>
          )}
        </FormField>
        <FormField label="Password">
          {(control) => (
            <>
              <input
                {...control}
                type="password"
                value={formData.password}
                onChange={(e) => setFormData({ ...formData, password: e.target.value })}
                autoComplete="new-password"
                className="input"
              />
              <FieldHint>{isEditMode ? 'Leave *** to keep the stored password' : 'Stored encrypted'}</FieldHint>
            </>
          )}
        </FormField>
      </div>

      {!(isCall && ring) && (
        <FormField label="Expected status code" error={errors.expected_status}>
          {(control) => (
            <>
              <input
                {...control}
                type="number"
                value={formData.expected_status}
                onChange={(e) => setFormData({ ...formData, expected_status: e.target.value })}
                placeholder="200"
                min={isCall ? 200 : 100}
                max={699}
                className="input !w-32 tabular-nums"
              />
              {!errors.expected_status && (
                <FieldHint>
                  {isCall
                    ? 'Default: any 2xx answer. Set one exact final response instead, such as 486 for a busy test line.'
                    : 'Default: 200 OK'}
                </FieldHint>
              )}
            </>
          )}
        </FormField>
      )}
    </FormPanel>
  );
}
