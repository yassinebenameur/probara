'use client';

import { useRef, useState } from 'react';
import { Monitor, CreateMonitorRequest, UpdateMonitorRequest, SIPMediaExpect, SIPMonitorConfig, Location } from '@/lib/types';
import { useConfigTest } from './useConfigTest';
import { buildSipConfig, isSipCall, parseWhole, sipCallsPerDay, sipCallTarget, sipMediaMinHoldMs, validateSipCallFields, SipFormFields, SIP_CALL_MAX_TIMEOUT_SECONDS, SIP_CALL_MIN_ANSWER_BUDGET_MS, SIP_CALL_MIN_INTERVAL_SECONDS } from '@/lib/sip-config';
import { buildSipRequest, initialSipFormState, sipFieldsFromForm } from '@/lib/monitor-form/sip';
import { pluralize } from '@/lib/format';
import { useCurrentUser, useIsTenantAdmin } from '@/components/providers/CurrentUserProvider';
import Callout from '@/components/ui/Callout';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import ScheduleSection from './ScheduleSection';
import { LabelsSection, PlanSummary, PlanTarget, downAfterLabel, everyLabel, revealFirstError, reportConstraintViolation, useClearErrorsOnEdit, useFollowEnabled, FormActionBar } from './form-kit';
import { CALL_ADVANCED_FIELDS } from '@/components/monitors/sip/types';
import SipTargetPanel from './sip/SipTargetPanel';
import SipTestRow from './sip/SipTestRow';
import SipCheckPanel from './sip/SipCheckPanel';
import SipCallPanel from './sip/SipCallPanel';
import SipCallQualityPanel from './sip/SipCallQualityPanel';
import SipAlertingPanel from './sip/SipAlertingPanel';

interface SipFormProps {
  monitor?: Monitor;
  initialData?: CreateMonitorRequest;
  onSubmit: (data: CreateMonitorRequest | UpdateMonitorRequest) => Promise<void>;
  onCancel?: () => void;
  loading?: boolean;
}

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

const CALL_INTERVAL_PRESETS = [60, 300, 900, 3600] as const;

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

  const configTest = useConfigTest({
    monitorId: isEditMode && monitor ? monitor.id : undefined,
    locationIds: formData.location_ids,
  });
  const test = configTest.state;
  const [confirmTestCall, setConfirmTestCall] = useState(false);
  // Shared by the Locations section (onLocationsLoaded) so the test-from
  // picker can name the monitor's locations. '' = default fleet.
  const [availableLocations, setAvailableLocations] = useState<Location[]>([]);
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
    await configTest.run({
      type: 'sip',
      config: buildConfig(),
      timeout_seconds: Math.min(formData.timeout_seconds || 10, isCall ? SIP_CALL_MAX_TIMEOUT_SECONDS : 30),
    });
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
  const measured = isCall && !ring && formData.call_media;

  return (
    <>
      <form ref={formRef} onSubmit={handleSubmit} noValidate data-form-stack className="space-y-5">
        {showCallLockedNotice && (
          <Callout tone="info" title="Read-only test call">
            Only a tenant admin signed in to Probara can create, change, run or test a SIP test-call monitor, since
            every check places a real call with the trunk&apos;s credentials.
          </Callout>
        )}

        <SipTargetPanel
          formData={formData}
          setFormData={setFormData}
          errors={errors}
          rfc3263={rfc3263}
          defaultPort={defaultPort}
          setLocateViaDNS={setLocateViaDNS}
        >
          <SipTestRow
            formData={formData}
            configTest={configTest}
            handleTest={handleTest}
            canTest={canTest}
            loading={loading}
            isCall={isCall}
            availableLocations={availableLocations}
          />
        </SipTargetPanel>

        <SipCheckPanel
          formData={formData}
          setFormData={setFormData}
          errors={errors}
          isEditMode={isEditMode}
          storedIsCall={storedIsCall}
          isCall={isCall}
          ring={ring}
          isTenantAdmin={isTenantAdmin}
          setMethod={setMethod}
        />

        {isCall && (
          <SipCallPanel
            formData={formData}
            setFormData={setFormData}
            errors={errors}
            ring={ring}
            callsPerDay={callsPerDay}
            callLocations={callLocations}
            callTarget={callTarget}
            holdNote={holdNote}
            setHoldNote={setHoldNote}
            callAdvancedOpen={callAdvancedOpen}
            setCallAdvancedOpen={setCallAdvancedOpen}
          />
        )}

        {isCall && !ring && (
          <SipCallQualityPanel formData={formData} setFormData={setFormData} errors={errors} setMedia={setMedia} />
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

        <SipAlertingPanel formData={formData} setFormData={setFormData} errors={errors} monitor={monitor} />

        <LabelsSection
          tags={formData.tags}
          onTagsChange={(v) => setFormData({ ...formData, tags: v })}
          placeholder="production, voip, critical"
        />

        <FormActionBar
          summary={
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
