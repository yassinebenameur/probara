'use client';

import { SIPMediaExpect } from '@/lib/types';
import { SipMediaThresholdField, SIP_MEDIA_MAX_JITTER_MS, SIP_MEDIA_MAX_MOS, SIP_MEDIA_MAX_RTT_MS, SIP_MEDIA_MIN_MOS } from '@/lib/sip-config';
import Callout from '@/components/ui/Callout';
import FormField from '@/components/ui/FormField';
import SegmentedControl from '@/components/ui/SegmentedControl';
import { FieldHint, FormPanel, ToggleRow } from '@/components/monitors/form-kit';
import { type SipFormData, type SetSipFormData } from '@/components/monitors/sip/types';

// What a measured call must prove (call.media.expect), a SegmentedControl
// like the mode.
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
const MEDIA_EXPECT_OPTIONS = MEDIA_EXPECTS.map(({ expect, label }) => ({ value: expect, label }));

interface SipCallQualityPanelProps {
  formData: SipFormData;
  setFormData: SetSipFormData;
  errors: Record<string, string>;
  /** Applies a call-quality choice, raising the hold and timeout to fit it. */
  setMedia: (patch: { call_media?: boolean; call_media_expect?: SIPMediaExpect }) => void;
}

/** SipForm's Call quality panel (answered test calls only): the toggle, what audio must prove, and thresholds. */
export default function SipCallQualityPanel({ formData, setFormData, errors, setMedia }: SipCallQualityPanelProps) {
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
  const mediaExpect = MEDIA_EXPECTS.find((m) => m.expect === formData.call_media_expect) ?? MEDIA_EXPECTS[1];

  return (
    <FormPanel title="Call quality">
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
            <SegmentedControl
              className="mb-2"
              wrap
              label="What the call must prove about audio"
              options={MEDIA_EXPECT_OPTIONS}
              value={formData.call_media_expect}
              onChange={(expect) => setMedia({ call_media_expect: expect })}
            />
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
    </FormPanel>
  );
}
