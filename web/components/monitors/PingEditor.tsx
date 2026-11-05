'use client';

import type { PingFormFields } from '@/lib/monitor-form/ping';
import { FormInput, FormPanel } from './form-kit';

/** The ping monitor's Check section. The host sits in MonitorForm's Target section. */
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
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
        <FormInput
          label="Echoes per check"
          type="number"
          inputMode="numeric"
          min={1}
          max={10}
          value={data.ping_count}
          onChange={(v) => onChange({ ping_count: v })}
          placeholder="1"
          error={errors.ping_count}
          hint="1–10 ICMP echo requests"
          className="input tabular-nums"
        />
        <FormInput
          label="Interval (ms)"
          type="number"
          inputMode="numeric"
          min={200}
          max={10000}
          value={data.ping_interval_ms}
          onChange={(v) => onChange({ ping_interval_ms: v })}
          placeholder="1000"
          error={errors.ping_interval_ms}
          hint="Gap between echoes"
          className="input tabular-nums"
        />
        <FormInput
          label="Packet size (bytes)"
          type="number"
          inputMode="numeric"
          min={24}
          max={1472}
          value={data.ping_packet_size}
          onChange={(v) => onChange({ ping_packet_size: v })}
          placeholder="24"
          error={errors.ping_packet_size}
          hint="Echo payload, 24–1472"
          className="input tabular-nums"
        />
      </div>
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
        <FormInput
          label="Max packet loss (%)"
          type="number"
          inputMode="decimal"
          min={0}
          max={99.99}
          step="any"
          value={data.ping_max_packet_loss_pct}
          onChange={(v) => onChange({ ping_max_packet_loss_pct: v })}
          placeholder="Only 100% fails"
          error={errors.ping_max_packet_loss_pct}
          hint="Fail when more echoes than this go unanswered"
          className="input tabular-nums"
        />
        <FormInput
          label="Fail above avg RTT (ms)"
          type="number"
          inputMode="numeric"
          min={1}
          value={data.ping_max_rtt_ms}
          onChange={(v) => onChange({ ping_max_rtt_ms: v })}
          placeholder="Off"
          error={errors.ping_max_rtt_ms}
          hint="Compared with the average round trip"
          className="input tabular-nums"
        />
        <FormInput
          label="Warn above avg RTT (ms)"
          type="number"
          inputMode="numeric"
          min={1}
          value={data.ping_warn_rtt_ms}
          onChange={(v) => onChange({ ping_warn_rtt_ms: v })}
          placeholder="Off"
          error={errors.ping_warn_rtt_ms}
          hint="Flags the result; the check still passes"
          className="input tabular-nums"
        />
      </div>
    </FormPanel>
  );
}
