'use client';

import { sipMediaMinHoldMs, SIP_CALL_MAX_HOLD_MS } from '@/lib/sip-config';
import { pluralize } from '@/lib/format';
import Callout from '@/components/ui/Callout';
import CollapsibleSection from '@/components/ui/CollapsibleSection';
import FormField from '@/components/ui/FormField';
import SegmentedControl from '@/components/ui/SegmentedControl';
import { FieldHint, FormPanel, ToggleRow } from '@/components/monitors/form-kit';
import { type SipFormData, type SetSipFormData, CALL_ADVANCED_FIELDS } from '@/components/monitors/sip/types';

// The call Mode segment: a roving radiogroup (one tab stop, arrow keys move
// the selection), like Tabs and RangePicker.
const CALL_MODES = [
  { mode: 'answer', label: 'Answer and hang up' },
  { mode: 'ring', label: 'Ring only' },
] as const;
const CALL_MODE_OPTIONS = CALL_MODES.map(({ mode, label }) => ({ value: mode, label }));

interface SipCallPanelProps {
  formData: SipFormData;
  setFormData: SetSipFormData;
  errors: Record<string, string>;
  /** Ring-only call mode. */
  ring: boolean;
  callsPerDay: number;
  callLocations: number;
  /** The URI the call dials, when the fields name one. */
  callTarget: string | null;
  /** Why the hold was raised (call quality), shown under the Hold field. */
  holdNote: string;
  setHoldNote: (note: string) => void;
  callAdvancedOpen: boolean;
  setCallAdvancedOpen: (open: boolean) => void;
}

/** SipForm's Call panel (test calls only): the number, mode, hold, thresholds, caller identity and Advanced. */
export default function SipCallPanel({
  formData,
  setFormData,
  errors,
  ring,
  callsPerDay,
  callLocations,
  callTarget,
  holdNote,
  setHoldNote,
  callAdvancedOpen,
  setCallAdvancedOpen,
}: SipCallPanelProps) {
  const thresholdInput = (
    key: 'call_warn_pdd_ms' | 'call_max_pdd_ms' | 'call_warn_answer_ms' | 'call_max_answer_ms',
    label: string,
    placeholder: string,
  ) => (
    <FormField label={label} error={errors[key]}>
      {(control) => (
        <span className="flex items-center gap-2">
          <input
            {...control}
            type="number"
            value={formData[key]}
            onChange={(e) => setFormData({ ...formData, [key]: e.target.value })}
            placeholder={placeholder}
            min={1}
            className="input !w-28 tabular-nums"
          />
          <span className="text-xs text-slate-500">ms</span>
        </span>
      )}
    </FormField>
  );

  return (
    <FormPanel title="Call">
      <Callout tone="warning" title="Each check places a real call">
        About {pluralize(callsPerDay, 'call')} a day
        {callLocations > 1 ? ` (${callLocations} locations)` : ''}, plus Run check and Test. Use a dedicated test
        number that accepts {pluralize(callLocations, 'concurrent call')}; a single-channel number answers 486
        to the others.
      </Callout>

      {formData.call_target_kind === 'number' ? (
        <FormField label="Number to call" required error={errors.call_number}>
          {(control) => (
            <>
              <input
                {...control}
                type="text"
                value={formData.call_number}
                onChange={(e) => setFormData({ ...formData, call_number: e.target.value })}
                placeholder="+33123456789"
                autoComplete="off"
                className="input font-mono"
              />
              {!errors.call_number && (
                <FieldHint>
                  {callTarget ? `Calls ${callTarget}` : 'Digits, letters and + * # . _ ~ -'}
                  {/^\+?[0-9]+$/.test(formData.call_number.trim()) ? ' as a telephone number (user=phone)' : ''}
                </FieldHint>
              )}
            </>
          )}
        </FormField>
      ) : (
        <p className="text-xs text-slate-500">
          Calls the target URI set under Advanced{callTarget ? `: ${callTarget}` : ''}.
        </p>
      )}

      <div>
        <p className="mb-2 text-xs font-medium text-slate-400">Mode</p>
        <SegmentedControl
          className="mb-2"
          label="Call mode"
          options={CALL_MODE_OPTIONS}
          value={formData.call_mode}
          onChange={(mode) => setFormData((prev) => ({ ...prev, call_mode: mode }))}
        />
        <FieldHint>
          {ring
            ? 'Succeeds on the first ringing (18x) and cancels, so the call is never answered or billed. A call answered anyway is hung up at once and flagged.'
            : `Waits for the answer (200), holds the call, then hangs up with BYE. ${
                formData.call_media ? 'Audio is exchanged and measured during the hold.' : 'No audio is sent.'
              }`}
        </FieldHint>
      </div>

      {!ring && (
        <FormField label="Hold" error={errors.call_hold_ms}>
          {(control) => (
            <>
              <span className="flex items-center gap-2">
                <input
                  {...control}
                  type="number"
                  value={formData.call_hold_ms}
                  onChange={(e) => {
                    setFormData({ ...formData, call_hold_ms: e.target.value });
                    setHoldNote('');
                  }}
                  placeholder={formData.call_media ? String(sipMediaMinHoldMs(formData.call_media_expect)) : '0'}
                  min={0}
                  max={SIP_CALL_MAX_HOLD_MS}
                  className="input !w-28 tabular-nums"
                />
                <span className="text-xs text-slate-500">ms</span>
              </span>
              {!errors.call_hold_ms && (
                <FieldHint>
                  {holdNote ||
                    (formData.call_media
                      ? `How long the answered call stays up, its audio measured, before the hang-up: at least ${sipMediaMinHoldMs(formData.call_media_expect)} ms; it comes out of the timeout`
                      : 'How long the answered call stays up before the hang-up; it comes out of the timeout')}
                </FieldHint>
              )}
            </>
          )}
        </FormField>
      )}

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        {thresholdInput('call_warn_pdd_ms', 'Post-dial delay warning', '3000')}
        {thresholdInput('call_max_pdd_ms', 'Post-dial delay failure', '5000')}
        {!ring && thresholdInput('call_warn_answer_ms', 'Answer time warning', '8000')}
        {!ring && thresholdInput('call_max_answer_ms', 'Answer time failure', '12000')}
      </div>
      <FieldHint>
        Post-dial delay runs from the INVITE to the first ringing; answer time to the 200. A warning keeps the check
        up and notes it; a failure threshold fails it.
      </FieldHint>

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
        <FormField label="Caller user" error={errors.call_from_user}>
          {(control) => (
            <>
              <input
                {...control}
                type="text"
                value={formData.call_from_user}
                onChange={(e) => setFormData({ ...formData, call_from_user: e.target.value })}
                placeholder={formData.username.trim() || 'probara'}
                autoComplete="off"
                className="input font-mono"
              />
              {!errors.call_from_user && <FieldHint>Default: the username</FieldHint>}
            </>
          )}
        </FormField>
        <FormField label="Caller name" error={errors.call_from_display}>
          {(control) => (
            <input
              {...control}
              type="text"
              value={formData.call_from_display}
              onChange={(e) => setFormData({ ...formData, call_from_display: e.target.value })}
              placeholder="Probara probe"
              className="input"
            />
          )}
        </FormField>
        <FormField label="Caller domain" error={errors.call_from_domain}>
          {(control) => (
            <>
              <input
                {...control}
                type="text"
                value={formData.call_from_domain}
                onChange={(e) => setFormData({ ...formData, call_from_domain: e.target.value })}
                placeholder={formData.domain.trim() || formData.host.trim() || 'example.com'}
                className="input font-mono"
              />
              {!errors.call_from_domain && <FieldHint>Default: the SIP domain</FieldHint>}
            </>
          )}
        </FormField>
      </div>

      <CollapsibleSection
        variant="row"
        title="Advanced"
        summary={formData.call_target_kind === 'uri' ? 'calls a target URI' : undefined}
        tone={CALL_ADVANCED_FIELDS.some((key) => errors[key]) ? 'danger' : 'default'}
        isOpen={callAdvancedOpen}
        onToggle={setCallAdvancedOpen}
      >
        <div className="space-y-4">
          <ToggleRow
            title="Call a full SIP URI"
            description="Instead of a number at the SIP domain. Every request still goes to the host above."
            checked={formData.call_target_kind === 'uri'}
            onChange={(on) => setFormData({ ...formData, call_target_kind: on ? 'uri' : 'number' })}
          />
          {formData.call_target_kind === 'uri' && (
            <FormField label="Target URI" required error={errors.call_target_uri}>
              {(control) => (
                <>
                  <input
                    {...control}
                    type="text"
                    value={formData.call_target_uri}
                    onChange={(e) => setFormData({ ...formData, call_target_uri: e.target.value })}
                    placeholder="sip:ivr-test@pbx.example.com"
                    autoComplete="off"
                    className="input font-mono"
                  />
                  {!errors.call_target_uri && (
                    <FieldHint>sip: or sips: (TLS only); no ;parameters in the user part, no ?headers or maddr</FieldHint>
                  )}
                </>
              )}
            </FormField>
          )}
          <FormField label="SDP address" error={errors.call_sdp_address}>
            {(control) => (
              <>
                <input
                  {...control}
                  type="text"
                  value={formData.call_sdp_address}
                  onChange={(e) => setFormData({ ...formData, call_sdp_address: e.target.value })}
                  placeholder="203.0.113.10"
                  className="input font-mono"
                />
                {!errors.call_sdp_address && (
                  <FieldHint>The public address a worker behind NAT puts in its audio offer, and where the far end sends audio when call quality is measured.</FieldHint>
                )}
              </>
            )}
          </FormField>
          <ToggleRow
            title="Mark probe calls"
            description="Adds X-Probara-Probe: 1 so the far end can recognise and route test calls"
            checked={formData.call_probe_header}
            onChange={(v) => setFormData({ ...formData, call_probe_header: v })}
          />
        </div>
      </CollapsibleSection>
    </FormPanel>
  );
}
