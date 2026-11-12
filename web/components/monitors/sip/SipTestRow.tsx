'use client';

import { AlertTriangle, CheckCircle2, Loader2, PhoneCall, PlugZap, XCircle } from 'lucide-react';
import { Location } from '@/lib/types';
import { useConfigTest } from '@/components/monitors/useConfigTest';
import { sipCallSummary, sipMediaUnsupportedHint, sipTestWarnings } from '@/lib/sip-metrics';
import { formatLatency } from '@/lib/format';
import Button from '@/components/ui/Button';
import { type SipFormData } from '@/components/monitors/sip/types';

interface SipTestRowProps {
  formData: SipFormData;
  configTest: ReturnType<typeof useConfigTest>;
  handleTest: () => void;
  canTest: boolean;
  /** The form is saving. */
  loading: boolean;
  isCall: boolean;
  availableLocations: Location[];
}

/** SipForm's Test row: the button, the location to test from and the last result. */
export default function SipTestRow({
  formData,
  configTest,
  handleTest,
  canTest,
  loading,
  isCall,
  availableLocations,
}: SipTestRowProps) {
  const test = configTest.state;
  const testResultSummary =
    test.phase === 'done' && test.result.status === 'success' ? sipCallSummary(test.result.metrics_data?.sip?.call) : '';
  // A pass can still carry what the operator must see: a ring-mode call that
  // was answered (billable), an unconfirmed BYE or CANCEL, a warn threshold.
  const testWarnings =
    test.phase === 'done' && test.result.status === 'success' ? sipTestWarnings(test.result.metrics_data?.sip) : [];
  // A worker that predates call quality refuses the test before dialing.
  const mediaUnsupported = test.phase === 'done' ? sipMediaUnsupportedHint(test.result.error_message) : '';

  return (
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
          value={configTest.locationId}
          onChange={(e) => configTest.setLocationId(e.target.value)}
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
  );
}
