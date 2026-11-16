'use client';

import {
  PING_DEFAULT_COUNT,
  PING_DEFAULT_INTERVAL_MS,
  PING_MAX_COUNT,
  PING_MAX_INTERVAL_MS,
  PING_MIN_INTERVAL_MS,
  PingFormFields,
} from '@/lib/monitor-form/ping';
import { FormInput, FormPanel } from './form-kit';

/**
 * The ping monitor's Check section: how many echoes a check sends, how far
 * apart, and what fails it. Empty fields keep the defaults. The host sits in
 * MonitorForm's Target section.
 */
export default function PingEditor({
  data,
  onChange,
  errors,
}: {
  data: PingFormFields;
  onChange: (patch: Partial<PingFormFields>) => void;
  errors: Record<string, string>;
}) {
  return (
    <FormPanel title="Check">
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <FormInput
          label="Echoes per check"
          type="number"
          inputMode="numeric"
          min={1}
          max={PING_MAX_COUNT}
          value={data.ping_count}
          onChange={(v) => onChange({ ping_count: v })}
          placeholder={String(PING_DEFAULT_COUNT)}
          error={errors.ping_count}
          hint={`1 to ${PING_MAX_COUNT}. Defaults to ${PING_DEFAULT_COUNT}.`}
          className="input tabular-nums"
        />
        <FormInput
          label="Gap between echoes (ms)"
          type="number"
          inputMode="numeric"
          min={PING_MIN_INTERVAL_MS}
          max={PING_MAX_INTERVAL_MS}
          value={data.ping_interval_ms}
          onChange={(v) => onChange({ ping_interval_ms: v })}
          placeholder={String(PING_DEFAULT_INTERVAL_MS)}
          error={errors.ping_interval_ms}
          hint={`Defaults to ${PING_DEFAULT_INTERVAL_MS}; shortened when the timeout cannot fit every echo and a 1-second wait for the last reply.`}
          className="input tabular-nums"
        />
        <FormInput
          label="Max packet loss (%)"
          type="number"
          inputMode="decimal"
          min={0}
          max={99.99}
          step="any"
          value={data.ping_max_packet_loss_pct}
          onChange={(v) => onChange({ ping_max_packet_loss_pct: v })}
          placeholder="Fail only when every echo is lost"
          error={errors.ping_max_packet_loss_pct}
          hint="Fail when more echoes than this go unanswered."
          className="input tabular-nums"
        />
        <FormInput
          label="Max round trip (ms)"
          type="number"
          inputMode="numeric"
          min={1}
          value={data.ping_max_rtt_ms}
          onChange={(v) => onChange({ ping_max_rtt_ms: v })}
          placeholder="No limit"
          error={errors.ping_max_rtt_ms}
          hint="Fail when the average round trip is slower."
          className="input tabular-nums"
        />
      </div>
    </FormPanel>
  );
}
