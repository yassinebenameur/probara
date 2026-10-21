'use client';

import { useRef, useState } from 'react';
import { AlertTriangle, CheckCircle2, Loader2, PhoneCall, PlugZap, XCircle } from 'lucide-react';
import { Monitor, CreateMonitorRequest, UpdateMonitorRequest, SIPMediaExpect, SIPMonitorConfig, Location } from '@/lib/types';
import { testMonitorConfig, TestMonitorConfigResponse } from '@/lib/api';
import {
  buildSipConfig,
  isSipCall,
  parseWhole,
  sipCallsPerDay,
  sipCallTarget,
  sipMediaMinHoldMs,
  validateSipCallFields,
  SipFormFields,
  SipMediaThresholdField,
  SIP_CALL_MAX_HOLD_MS,
  SIP_CALL_MAX_TIMEOUT_SECONDS,
  SIP_CALL_MIN_ANSWER_BUDGET_MS,
  SIP_CALL_MIN_INTERVAL_SECONDS,
  SIP_MEDIA_MAX_JITTER_MS,
  SIP_MEDIA_MAX_MOS,
  SIP_MEDIA_MAX_RTT_MS,
  SIP_MEDIA_MIN_MOS,
} from '@/lib/sip-config';
import { sipCallSummary, sipMediaUnsupportedHint, sipTestWarnings } from '@/lib/sip-metrics';
import { buildSipRequest, initialSipFormState, sipFieldsFromForm } from '@/lib/monitor-form/sip';
import { formatLatency, pluralize } from '@/lib/format';
import { useCurrentUser, useIsTenantAdmin } from '@/components/providers/CurrentUserProvider';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import CollapsibleSection from '@/components/ui/CollapsibleSection';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import FormField from '@/components/ui/FormField';
import FormSection from '@/components/ui/FormSection';
import FormActions from '@/components/ui/FormActions';
import { handleRovingKeyDown, SEGMENT_ITEM, SELECTED_SEGMENT } from '@/components/ui/roving';
import { FOCUS_RING } from '@/components/ui/focus';
import { AlertingSection } from './AlertingSection';
import ScheduleSection from './ScheduleSection';
import DependenciesField from './DependenciesField';
import { FieldHint, FormPanel, LabelsSection, PlanSummary, PlanTarget, ToggleRow, downAfterLabel, everyLabel, revealFirstError, reportConstraintViolation, useClearErrorsOnEdit, useFollowEnabled } from './form-kit';

interface SipFormProps {
  monitor?: Monitor;
  initialData?: CreateMonitorRequest;
  onSubmit: (data: CreateMonitorRequest | UpdateMonitorRequest) => Promise<void>;
  onCancel?: () => void;
  loading?: boolean;
}

type TestState =
  | { phase: 'idle' }
  | { phase: 'running' }
  | { phase: 'done'; result: TestMonitorConfigResponse }
  | { phase: 'request_failed'; message: string };

// Errors cleared by edits to another field (useClearErrorsOnEdit): the call
// schedule bounds follow the method and the hold, the hold follows the
// timeout and mode, a sips: target follows the transport. An error also
// clears when the field that hides its input changes, so a hidden field never
// keeps the footer at "N fields need attention": every call_* field (and the
// test-call domain rule) lives only under method invite, the answer
// thresholds and hold only in answer mode, the nameserver only under
// rfc3263, and the certificate threshold only over TLS (which turning
// "Locate via DNS" off can change along with the resolution). Call quality
// moves the hold floor and refuses a non-2xx expected status, and its
// thresholds live only while it is on, in answer mode, outside Measure only.
const SIP_MEDIA_THRESHOLD_RELATED = ['call_media', 'call_media_expect', 'call_mode', 'method'];
const SIP_RELATED_FIELDS: Record<string, string[]> = {
  timeout_seconds: ['interval_seconds', 'method'],
  interval_seconds: ['method'],
  nameserver: ['resolution'],
  tls_min_days_valid: ['transport', 'resolution'],
  domain: ['method'],
  call_number: ['call_target_kind', 'method'],
  call_target_uri: ['transport', 'call_target_kind', 'method'],
  call_from_user: ['method'],
  call_from_display: ['method'],
  call_from_domain: ['method'],
  call_sdp_address: ['method'],
  call_hold_ms: ['timeout_seconds', 'call_mode', 'method', 'call_media', 'call_media_expect'],
  call_warn_pdd_ms: ['call_max_pdd_ms', 'method'],
  call_max_pdd_ms: ['method'],
  call_warn_answer_ms: ['call_max_answer_ms', 'call_mode', 'method'],
  call_max_answer_ms: ['call_mode', 'method'],
  expected_status: ['call_mode', 'method', 'call_media'],
  call_media_warn_loss_pct: ['call_media_max_loss_pct', ...SIP_MEDIA_THRESHOLD_RELATED],
  call_media_max_loss_pct: SIP_MEDIA_THRESHOLD_RELATED,
  call_media_warn_jitter_ms: ['call_media_max_jitter_ms', ...SIP_MEDIA_THRESHOLD_RELATED],
  call_media_max_jitter_ms: SIP_MEDIA_THRESHOLD_RELATED,
  call_media_warn_mos: ['call_media_min_mos', ...SIP_MEDIA_THRESHOLD_RELATED],
  call_media_min_mos: SIP_MEDIA_THRESHOLD_RELATED,
  call_media_warn_rtt_ms: ['call_media_max_rtt_ms', ...SIP_MEDIA_THRESHOLD_RELATED],
  call_media_max_rtt_ms: SIP_MEDIA_THRESHOLD_RELATED,
};

// Error keys that live in the call's Advanced disclosure, opened on submit
// when one of them fails.
const CALL_ADVANCED_FIELDS = ['call_target_uri', 'call_sdp_address'];

const CALL_INTERVAL_PRESETS = [60, 300, 900, 3600] as const;

// The call Mode segment: a roving radiogroup (one tab stop, arrow keys move
// the selection), like Tabs and RangePicker.
const CALL_MODES = [
  { mode: 'answer', label: 'Answer and hang up' },
  { mode: 'ring', label: 'Ring only' },
] as const;
const CALL_MODE_IDS = CALL_MODES.map((m) => m.mode);

// What a measured call must prove (call.media.expect), the same roving
// segment as the mode.
const MEDIA_EXPECTS: { expect: SIPMediaExpect; label: string; hint: string }[] = [
  {
    expect: 'none',
    label: 'Measure only',
    hint: 'Reports what the audio did and never fails the check on it; problems only add warnings.',
  },
  {
    expect: 'audio',
    label: 'Far end sends audio',
    hint: "Fails when the far end's audio never reaches the probe (one-way audio), or when a failure threshold is crossed.",
  },
  {
    expect: 'two_way',
    label: 'Two-way audio (echo or RTCP)',
    hint: "Also needs the far end's word that the probe's audio arrived: an echo of it, or an RTCP report about it. Needs a hold of at least 6000 ms, as a far end can take that long to send its first report.",
  },
  {
    expect: 'echo',
    label: 'Echo test line',
    hint: "For a number that plays the caller's audio back: sends a 1004 Hz tone and fails unless it comes back.",
  },
];
const MEDIA_EXPECT_IDS = MEDIA_EXPECTS.map((m) => m.expect);
// The hold a call is raised to when call quality is turned on with less:
// above the 3000 ms floor, so the measured window clears the 2 s a quality
// estimate needs with room to spare.
const MEDIA_DEFAULT_HOLD_MS = 5000;

type SipMethod = SipFormFields['method'];

export default function SipForm({
  monitor,
  initialData,
  onSubmit,
  onCancel,
  loading = false,
}: SipFormProps) {
  const isEditMode = Boolean(monitor);
  const { canWrite, loading: userLoading } = useCurrentUser();
  const isTenantAdmin = useIsTenantAdmin();
  const initialSipConfig =
    !isEditMode && initialData?.type === 'sip' ? (initialData.config as SIPMonitorConfig) : undefined;

  const editSipConfig =
    monitor && monitor.type === 'sip' ? (monitor.config as SIPMonitorConfig) : undefined;
  const loadedConfig = editSipConfig ?? initialSipConfig;

  // A saved monitor cannot switch between a test call and OPTIONS/REGISTER
  // (the API refuses it: latency and billing would change meaning under one
  // history), so edit mode locks the method class.
  const storedIsCall = isEditMode && isSipCall('sip', editSipConfig);

  const [formData, setFormData] = useState(() => initialSipFormState(monitor, initialData));

  const [errors, setErrors] = useState<Record<string, string>>({});
  useClearErrorsOnEdit(formData, setErrors, SIP_RELATED_FIELDS);
  useFollowEnabled(monitor?.enabled, setFormData);
  const formRef = useRef<HTMLFormElement>(null);
  const rfc3263 = formData.resolution === 'rfc3263';
  const defaultPort = formData.transport === 'tls' ? 5061 : 5060;
  const isCall = formData.method === 'invite';
  const ring = formData.call_mode === 'ring';
  const [callAdvancedOpen, setCallAdvancedOpen] = useState(
    formData.call_target_kind === 'uri' || Boolean(formData.call_sdp_address) || formData.call_probe_header
  );

  // Test calls are a tenant admin's: anyone else can read the monitor but
  // the API refuses any change, run or test of it. False while identity
  // loads; the read-only notice waits for it.
  const callLocked = isCall && !isTenantAdmin;
  const showCallLockedNotice = callLocked && !userLoading;

  const [test, setTest] = useState<TestState>({ phase: 'idle' });
  const [confirmTestCall, setConfirmTestCall] = useState(false);
  // Shared by the Locations section (onLocationsLoaded) so the test-from
  // picker can name the monitor's locations. '' = default fleet.
  const [availableLocations, setAvailableLocations] = useState<Location[]>([]);
  const [testLocationId, setTestLocationId] = useState('');
  const canTest = isCall ? isTenantAdmin : !userLoading && (!isEditMode || canWrite);

  // One check per location per interval; the default fleet runs it once.
  const callLocations = Math.max(1, formData.location_ids.length);
  const callsPerDay = sipCallsPerDay(formData.interval_seconds, formData.location_ids.length);

  const setLocateViaDNS = (on: boolean) =>
    setFormData((prev) => {
      const prevDefault = prev.transport === 'tls' ? 5061 : 5060;
      if (on) {
        // A port pins A/AAAA resolution, so the conventional default the
        // form pre-filled is cleared and SRV picks the port.
        return { ...prev, resolution: 'rfc3263', port: prev.port === prevDefault ? 0 : prev.port };
      }
      const transport = prev.transport === 'auto' ? 'udp' : prev.transport;
      return {
        ...prev,
        resolution: 'a',
        transport,
        port: prev.port || (transport === 'tls' ? 5061 : 5060),
      };
    });

  const setMethod = (method: SipMethod) =>
    setFormData((prev) => {
      if (method !== 'invite') return { ...prev, method };
      // Start a new test call inside its schedule bounds rather than with
      // two errors waiting on submit.
      return {
        ...prev,
        method,
        interval_seconds: Math.max(prev.interval_seconds, SIP_CALL_MIN_INTERVAL_SECONDS),
        timeout_seconds: Math.min(prev.timeout_seconds, SIP_CALL_MAX_TIMEOUT_SECONDS),
      };
    });

  // Set when turning call quality on (or choosing two-way audio) raised the
  // hold, so the Hold field says why it changed; editing the hold clears it.
  const [holdNote, setHoldNote] = useState('');

  // Applies a call-quality choice, raising a hold below its floor (and the
  // timeout, to fit the hold and the time to answer) rather than leaving an
  // error waiting on submit.
  const setMedia = (patch: { call_media?: boolean; call_media_expect?: SIPMediaExpect }) => {
    const next = { ...formData, ...patch };
    const minHold = sipMediaMinHoldMs(next.call_media_expect);
    const hold = parseWhole(next.call_hold_ms);
    if (!next.call_media || (hold !== null && hold >= minHold)) {
      setFormData(next);
      if (!next.call_media) setHoldNote('');
      return;
    }
    const raised = Math.max(minHold, MEDIA_DEFAULT_HOLD_MS);
    const timeout = Math.min(
      Math.max(next.timeout_seconds, Math.ceil((raised + SIP_CALL_MIN_ANSWER_BUDGET_MS) / 1000)),
      SIP_CALL_MAX_TIMEOUT_SECONDS
    );
    setFormData({ ...next, call_hold_ms: String(raised), timeout_seconds: timeout });
    setHoldNote(
      `Raised to ${raised} ms: ${next.call_media_expect === 'two_way' ? 'two-way audio' : 'measuring call quality'} needs at least ${minHold} ms` +
        (timeout !== next.timeout_seconds ? `, and the timeout to ${timeout} s to fit it` : '')
    );
  };

  const sipFields = (): SipFormFields => sipFieldsFromForm(formData);

  // Built on top of the loaded config so keys this form does not render
  // survive the save (an update replaces the whole stored config).
  const buildConfig = () => buildSipConfig(loadedConfig, sipFields());

  // Everything but naming and the schedule's interval/timeout relation:
  // what a Test needs too.
  const validateTarget = (): Record<string, string> => {
    const newErrors: Record<string, string> = {
      ...validateSipCallFields(sipFields(), formData.interval_seconds, formData.timeout_seconds),
    };
    if (!formData.host.trim()) newErrors.host = 'Host is required';
    if (rfc3263 ? formData.port < 0 || formData.port > 65535 : formData.port < 1 || formData.port > 65535) {
      newErrors.port = 'Port must be between 1 and 65535';
    }
    if (formData.transport === 'tls' && formData.tls_min_days_valid.trim() && parseWhole(formData.tls_min_days_valid) === null) {
      newErrors.tls_min_days_valid = 'Must be a whole number of days, 0 or more';
    }
    if (rfc3263 && /\s/.test(formData.nameserver.trim())) {
      newErrors.nameserver = 'Enter a host or host:port';
    }
    if ((formData.username.trim() === '') !== (formData.password === '')) {
      newErrors.username = 'Username and password must be provided together';
    }
    return newErrors;
  };

  const showErrors = (newErrors: Record<string, string>) => {
    setErrors(newErrors);
    if (CALL_ADVANCED_FIELDS.some((key) => newErrors[key])) setCallAdvancedOpen(true);
    revealFirstError(formRef.current);
  };

  const runTest = async () => {
    setConfirmTestCall(false);
    // Ignore a stale pick if the location was deselected since.
    const chosenLocationId = formData.location_ids.includes(testLocationId) ? testLocationId : '';
    setTest({ phase: 'running' });
    try {
      const result = await testMonitorConfig({
        type: 'sip',
        config: buildConfig(),
        timeout_seconds: Math.min(formData.timeout_seconds || 10, isCall ? SIP_CALL_MAX_TIMEOUT_SECONDS : 30),
        ...(isEditMode && monitor ? { monitor_id: monitor.id } : {}),
        ...(chosenLocationId ? { location_id: chosenLocationId } : {}),
      });
      setTest({ phase: 'done', result });
    } catch (err) {
      setTest({ phase: 'request_failed', message: err instanceof Error ? err.message : 'Test request failed' });
    }
  };

  const handleTest = () => {
    if (!canTest || test.phase === 'running') return;
    const targetErrors = validateTarget();
    // The schedule does not bound a one-off test (its timeout is clamped).
    delete targetErrors.interval_seconds;
    delete targetErrors.timeout_seconds;
    if (Object.keys(targetErrors).length > 0) {
      showErrors(targetErrors);
      return;
    }
    setErrors({});
    // A test of a test call dials for real: confirm first.
    if (isCall) setConfirmTestCall(true);
    else void runTest();
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (callLocked) return;
    if (reportConstraintViolation(formRef.current)) return;
    setErrors({});

    const newErrors: Record<string, string> = validateTarget();
    if (!formData.name.trim()) newErrors.name = 'Name is required';
    if (formData.timeout_seconds >= formData.interval_seconds) {
      newErrors.timeout_seconds = 'Timeout must be less than interval';
    }

    if (Object.keys(newErrors).length > 0) {
      showErrors(newErrors);
      return;
    }

    const requestData: CreateMonitorRequest | UpdateMonitorRequest = buildSipRequest(formData, loadedConfig);

    await onSubmit(requestData);
  };

  const callTarget = isCall ? sipCallTarget(buildConfig()) : null;
  const methodHint =
    formData.method === 'register'
      ? 'Sends a query-style REGISTER (no Contact) that exercises the registrar and its digest authentication without creating or removing any bindings.'
      : formData.method === 'invite'
        ? 'Places a real call through the host, which acts as the outbound proxy, and measures post-dial delay and answer time. Digest credentials answer the trunk’s challenges.'
        : 'Sends a SIP OPTIONS request to test server availability without initiating a call. Answers digest challenges when credentials are configured.';
  const methodOptionDisabled = (m: SipMethod) => isEditMode && (storedIsCall ? m !== 'invite' : m === 'invite');
  const testResultSummary =
    test.phase === 'done' && test.result.status === 'success' ? sipCallSummary(test.result.metrics_data?.sip?.call) : '';
  // A pass can still carry what the operator must see: a ring-mode call that
  // was answered (billable), an unconfirmed BYE or CANCEL, a warn threshold.
  const testWarnings =
    test.phase === 'done' && test.result.status === 'success' ? sipTestWarnings(test.result.metrics_data?.sip) : [];
  // A worker that predates call quality refuses the test before dialing.
  const mediaUnsupported = test.phase === 'done' ? sipMediaUnsupportedHint(test.result.error_message) : '';

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

  // One call-quality threshold. Loss and MOS take decimals; the native
  // min/max (reportConstraintViolation) catch the out-of-range ones first.
  const mediaThresholdInput = (
    key: SipMediaThresholdField,
    label: string,
    placeholder: string,
    unit: string,
    bounds: { min: number; max: number; step: number | 'any' }
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
            min={bounds.min}
            max={bounds.max}
            step={bounds.step}
            className="input !w-28 tabular-nums"
          />
          {unit && <span className="text-xs text-slate-500">{unit}</span>}
        </span>
      )}
    </FormField>
  );
  const measured = isCall && !ring && formData.call_media;
  const mediaExpect = MEDIA_EXPECTS.find((m) => m.expect === formData.call_media_expect) ?? MEDIA_EXPECTS[1];

  return (
    <>
      <form ref={formRef} onSubmit={handleSubmit} noValidate className="space-y-5">
        {showCallLockedNotice && (
          <Callout tone="info" title="Read-only test call">
            Only a tenant admin signed in to Probara can create, change, run or test a SIP test-call monitor, since
            every check places a real call with the trunk&apos;s credentials.
          </Callout>
        )}

        <FormSection title="Target">
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

          <div className="glass-well flex flex-wrap items-center gap-3 px-4 py-3">
            <Button
              type="button"
              onClick={handleTest}
              disabled={!canTest || test.phase === 'running' || loading}
              title={
                !canTest
                  ? isCall
                    ? 'Only a tenant admin can test a SIP test call'
                    : 'Write access is required to test a saved monitor'
                  : undefined
              }
              icon={
                test.phase === 'running' ? (
                  <Loader2 className="h-4 w-4 animate-spin" strokeWidth={1.75} />
                ) : isCall ? (
                  <PhoneCall className="h-4 w-4" strokeWidth={1.75} />
                ) : (
                  <PlugZap className="h-4 w-4" strokeWidth={1.75} />
                )
              }
            >
              {test.phase === 'running' ? 'Testing…' : isCall ? 'Test call' : 'Test'}
            </Button>
            {formData.location_ids.length > 0 && (
              <select
                aria-label="Test from location"
                value={formData.location_ids.includes(testLocationId) ? testLocationId : ''}
                onChange={(e) => setTestLocationId(e.target.value)}
                className="input input-sm !w-auto"
              >
                <option value="">Test from: default fleet</option>
                {availableLocations
                  .filter((loc) => formData.location_ids.includes(loc.id))
                  .map((loc) => (
                    <option key={loc.id} value={loc.id}>Test from: {loc.name}</option>
                  ))}
              </select>
            )}
            {test.phase === 'idle' && (
              <p className="text-xs text-slate-500">
                {isCall
                  ? 'Places one real call from a worker without saving anything.'
                  : 'Runs one check from a worker without saving anything.'}
              </p>
            )}
            {test.phase === 'done' && test.result.status === 'success' && (
              <div className="min-w-0">
                <p
                  className={`flex items-center gap-1.5 text-xs ${testWarnings.length > 0 ? 'text-amber-300' : 'text-emerald-300'}`}
                >
                  {testWarnings.length > 0 ? (
                    <AlertTriangle className="h-3.5 w-3.5 shrink-0" strokeWidth={1.75} />
                  ) : (
                    <CheckCircle2 className="h-3.5 w-3.5 shrink-0" strokeWidth={1.75} />
                  )}
                  {testWarnings.length > 0 ? 'Passed with warnings' : 'Passed'}
                  {typeof test.result.latency_ms === 'number' ? ` in ${formatLatency(test.result.latency_ms)}` : ''}
                  {testResultSummary ? `: ${testResultSummary}` : ''}
                </p>
                {testWarnings.length > 0 && (
                  <p className="truncate text-xs text-amber-300" title={testWarnings.join('; ')}>
                    {testWarnings.join('; ')}
                  </p>
                )}
              </div>
            )}
            {test.phase === 'done' && test.result.status !== 'success' && (
              <div className="min-w-0">
                <p className="flex min-w-0 items-center gap-1.5 text-xs text-rose-300">
                  <XCircle className="h-3.5 w-3.5 shrink-0" strokeWidth={1.75} />
                  <span className="truncate" title={test.result.error_message}>
                    {test.result.error_message || 'Check failed'}
                  </span>
                </p>
                {mediaUnsupported && <p className="text-xs text-amber-300">{mediaUnsupported}</p>}
              </div>
            )}
            {test.phase === 'request_failed' && (
              <p className="flex min-w-0 items-center gap-1.5 text-xs text-amber-300">
                <XCircle className="h-3.5 w-3.5 shrink-0" strokeWidth={1.75} />
                <span className="truncate" title={test.message}>{test.message}</span>
              </p>
            )}
          </div>
        </FormSection>

        <FormSection title="Check">
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
        </FormSection>

        {isCall && (
          <FormSection title="Call">
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
              <div
                className="mb-2 flex w-fit max-w-full gap-0.5 rounded-xl bg-well p-1 shadow-[inset_0_0_0_1px_var(--glass-edge)]"
                role="radiogroup"
                aria-label="Call mode"
                onKeyDown={(e) =>
                  handleRovingKeyDown(e, CALL_MODE_IDS, formData.call_mode, (mode) =>
                    setFormData((prev) => ({ ...prev, call_mode: mode }))
                  )
                }
              >
                {CALL_MODES.map(({ mode, label }) => (
                  <button
                    key={mode}
                    type="button"
                    role="radio"
                    data-roving-id={mode}
                    tabIndex={formData.call_mode === mode ? 0 : -1}
                    aria-checked={formData.call_mode === mode}
                    onClick={() => setFormData((prev) => ({ ...prev, call_mode: mode }))}
                    className={`whitespace-nowrap rounded-lg px-3.5 py-1.5 text-ui font-medium ${SEGMENT_ITEM} ${FOCUS_RING} ${
                      formData.call_mode === mode ? SELECTED_SEGMENT : 'text-slate-400 hover:text-slate-100'
                    }`}
                  >
                    {label}
                  </button>
                ))}
              </div>
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
          </FormSection>
        )}

        {isCall && !ring && (
          <FormSection title="Call quality">
            <ToggleRow
              title="Measure call quality"
              description="Exchange audio with the far end during the hold and measure loss, jitter, a MOS estimate and the round trip"
              checked={formData.call_media}
              onChange={(on) => setMedia({ call_media: on })}
            />
            {formData.call_media && (
              <>
                <Callout tone="info" title="Sends test audio">
                  Sends RTP (about 50 packets a second for the hold) from the port the offer advertises to the address in
                  the far end&apos;s answer, only if this worker&apos;s network policy allows it: a 1004 Hz test tone for an
                  echo test line, silence otherwise. Quality is an E-model estimate, not a listening test.
                </Callout>
                {formData.location_ids.length > 0 && (
                  <Callout tone="warning" title="Location workers need call-quality support">
                    A location worker that predates call-quality measurement fails this monitor before it dials, with{' '}
                    <span className="font-mono">call.media: media analysis is not available yet</span>. Upgrade the
                    workers of the monitor&apos;s locations first.
                  </Callout>
                )}

                <div>
                  <p className="mb-2 text-xs font-medium text-slate-400">Expect</p>
                  <div
                    className="mb-2 flex w-fit max-w-full flex-wrap gap-0.5 rounded-xl bg-well p-1 shadow-[inset_0_0_0_1px_var(--glass-edge)]"
                    role="radiogroup"
                    aria-label="What the call must prove about audio"
                    onKeyDown={(e) =>
                      handleRovingKeyDown(e, MEDIA_EXPECT_IDS, formData.call_media_expect, (expect) =>
                        setMedia({ call_media_expect: expect })
                      )
                    }
                  >
                    {MEDIA_EXPECTS.map(({ expect, label }) => (
                      <button
                        key={expect}
                        type="button"
                        role="radio"
                        data-roving-id={expect}
                        tabIndex={formData.call_media_expect === expect ? 0 : -1}
                        aria-checked={formData.call_media_expect === expect}
                        onClick={() => setMedia({ call_media_expect: expect })}
                        className={`whitespace-nowrap rounded-lg px-3.5 py-1.5 text-ui font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-500/50 ${
                          formData.call_media_expect === expect ? SELECTED_SEGMENT : 'text-slate-400 hover:text-slate-100'
                        }`}
                      >
                        {label}
                      </button>
                    ))}
                  </div>
                  <FieldHint>{mediaExpect.hint}</FieldHint>
                </div>

                {formData.call_media_expect !== 'none' && (
                  <>
                    <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                      {mediaThresholdInput('call_media_warn_loss_pct', 'Loss warning', '1', '%', { min: 0, max: 100, step: 'any' })}
                      {mediaThresholdInput('call_media_max_loss_pct', 'Loss failure', '5', '%', { min: 0, max: 100, step: 'any' })}
                      {mediaThresholdInput('call_media_warn_jitter_ms', 'Jitter warning', '30', 'ms', {
                        min: 1,
                        max: SIP_MEDIA_MAX_JITTER_MS,
                        step: 1,
                      })}
                      {mediaThresholdInput('call_media_max_jitter_ms', 'Jitter failure', '60', 'ms', {
                        min: 1,
                        max: SIP_MEDIA_MAX_JITTER_MS,
                        step: 1,
                      })}
                      {mediaThresholdInput('call_media_warn_mos', 'MOS warning', '4.0', '', {
                        min: SIP_MEDIA_MIN_MOS,
                        max: SIP_MEDIA_MAX_MOS,
                        step: 'any',
                      })}
                      {mediaThresholdInput('call_media_min_mos', 'MOS failure', '3.6', '', {
                        min: SIP_MEDIA_MIN_MOS,
                        max: SIP_MEDIA_MAX_MOS,
                        step: 'any',
                      })}
                      {mediaThresholdInput('call_media_warn_rtt_ms', 'Round-trip warning', '300', 'ms', {
                        min: 1,
                        max: SIP_MEDIA_MAX_RTT_MS,
                        step: 1,
                      })}
                      {mediaThresholdInput('call_media_max_rtt_ms', 'Round-trip failure', '500', 'ms', {
                        min: 1,
                        max: SIP_MEDIA_MAX_RTT_MS,
                        step: 1,
                      })}
                    </div>
                    <FieldHint>
                      All optional. Each applies to every direction the call measured (the far end&apos;s audio, its RTCP
                      reports on the probe&apos;s, an echo), and the worst one decides. Loss is effective loss, jitter the
                      mean RFC 3550 jitter, MOS an estimate where a clean call scores about 4.37, the model&apos;s ceiling
                      (thresholds take {SIP_MEDIA_MIN_MOS} to {SIP_MEDIA_MAX_MOS}), and the round trip the echo&apos;s, else
                      RTCP&apos;s. One packet is 0.2-0.8% of a
                      call this short, so a finer loss threshold is noise. A failure counts as downtime: keep failure
                      thresholds well outside normal variance and use warnings for the band.
                    </FieldHint>
                  </>
                )}
              </>
            )}
          </FormSection>
        )}

        <ScheduleSection
          intervalSeconds={formData.interval_seconds}
          onIntervalChange={(n) => setFormData((prev) => ({ ...prev, interval_seconds: n }))}
          timeoutSeconds={formData.timeout_seconds}
          onTimeoutChange={(n) => setFormData((prev) => ({ ...prev, timeout_seconds: n }))}
          timeoutError={errors.timeout_seconds}
          intervalError={errors.interval_seconds}
          {...(isCall
            ? {
                minIntervalSeconds: SIP_CALL_MIN_INTERVAL_SECONDS,
                intervalPresets: CALL_INTERVAL_PRESETS,
                intervalHint: `At least ${SIP_CALL_MIN_INTERVAL_SECONDS} seconds: each check is a real call`,
                maxTimeoutSeconds: SIP_CALL_MAX_TIMEOUT_SECONDS,
              }
            : {})}
          locations={{
            selectedIds: formData.location_ids,
            onChange: (ids) => setFormData((prev) => ({ ...prev, location_ids: ids })),
            quorum: formData.location_quorum,
            onQuorumChange: (n) => setFormData((prev) => ({ ...prev, location_quorum: n })),
            onLocationsLoaded: setAvailableLocations,
          }}
          enabled={formData.enabled}
          onEnabledChange={(v) => setFormData((prev) => ({ ...prev, enabled: v }))}
          enabledDescription={
            isCall
              ? 'Place a test call on this schedule'
              : `Run SIP ${formData.method === 'register' ? 'REGISTER' : 'OPTIONS'} checks on this schedule`
          }
        />

        <FormPanel
          title="Alerting"
          rows={
            <DependenciesField
              selectedIds={formData.depends_on_ids}
              onChange={(ids) => setFormData((prev) => ({ ...prev, depends_on_ids: ids }))}
              excludeId={monitor?.id}
              error={errors.depends_on_ids}
            />
          }
        >
          <AlertingSection
            isGroup={false}
            intervalSeconds={formData.interval_seconds}
            threshold={formData.consecutive_failures_threshold}
            onThresholdChange={(n) => setFormData({ ...formData, consecutive_failures_threshold: n })}
            mode={formData.notification_mode}
            onModeChange={(m) => setFormData({ ...formData, notification_mode: m })}
            dependencySuppression={formData.dependency_suppression}
            hasDependencies={formData.depends_on_ids.length > 0}
            onDependencySuppressionChange={(d) => setFormData({ ...formData, dependency_suppression: d })}
            customChannels={formData.notification_channels}
            onCustomChannelsChange={(next) => setFormData({ ...formData, notification_channels: next })}
          />
        </FormPanel>

        <LabelsSection
          tags={formData.tags}
          onTagsChange={(v) => setFormData({ ...formData, tags: v })}
          placeholder="production, voip, critical"
        />

        <FormActions
          middle={
            <PlanSummary
              errors={errors}
              parts={
                formData.host.trim()
                  ? isCall
                    ? [
                        everyLabel(formData.interval_seconds),
                        <PlanTarget key="t">
                          call {callTarget || 'number'} via {formData.host.trim()}
                          {formData.port ? `:${formData.port}` : ''}
                        </PlanTarget>,
                        ring ? 'ring only' : 'answer and hang up',
                        ...(measured ? ['measures call quality'] : []),
                        `≈ ${pluralize(callsPerDay, 'call')}/day`,
                        downAfterLabel(formData.consecutive_failures_threshold),
                      ]
                    : [
                        everyLabel(formData.interval_seconds),
                        <PlanTarget key="t">
                          SIP {formData.method.toUpperCase()} {formData.host.trim()}
                          {formData.port ? `:${formData.port}` : ''}
                        </PlanTarget>,
                        ...(rfc3263 && !formData.port ? ['located via SRV'] : []),
                        formData.transport === 'auto' ? 'transport from NAPTR/SRV' : `over ${formData.transport.toUpperCase()}`,
                        downAfterLabel(formData.consecutive_failures_threshold),
                      ]
                  : []
              }
            />
          }
          cancel={onCancel ? { label: 'Cancel', onClick: onCancel, disabled: loading } : undefined}
          submit={{
            label: loading ? 'Saving…' : isEditMode ? 'Save changes' : 'Create monitor',
            loading,
            disabled: loading || callLocked,
            type: 'submit',
          }}
        />
      </form>

      <ConfirmDialog
        open={confirmTestCall}
        tone="default"
        title="Place a real test call?"
        description={
          <>
            This places a real call to <span className="font-mono text-slate-200">{callTarget || 'the configured number'}</span>{' '}
            through {formData.host.trim()}
            {measured ? ' and sends test audio during the hold' : ''}. The carrier may bill it.
          </>
        }
        confirmLabel="Place call"
        onConfirm={() => void runTest()}
        onCancel={() => setConfirmTestCall(false)}
      />
    </>
  );
}
