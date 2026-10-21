import { formatDate, formatJitter, formatLatency, formatLossPct, formatMOS, pluralize } from './format';
import type { TimedSnapshot } from './metric-snapshots';
import { lossTone, mosTone, thresholdTone } from './status-tones';
import type {
  CheckResult,
  SIPCallMetrics,
  SIPMediaConfig,
  SIPMediaMetrics,
  SIPMetrics,
  SIPMetricsEnvelope,
  TLSCertInfo,
} from './types';

// Facts the monitor detail page's Server panel shows for a SIP monitor, read
// from the worker's metrics_data.sip envelope (worker sip_metrics.go).

export interface SIPFact {
  key: string;
  label: string;
  value: string;
  tone?: 'warning' | 'note';
  /** Rendered as a copy control: an identifier to paste into server logs. */
  copyable?: boolean;
  /** Rendered as an info tip next to the value: how the value was derived. */
  tip?: string;
}

/** The newest result that carries a SIP envelope (older results may not). */
export function latestSIPMetrics(results: CheckResult[]): SIPMetrics | null {
  for (const result of results) {
    const sip = (result.metrics_data as SIPMetricsEnvelope | undefined)?.sip;
    if (sip) return sip;
  }
  return null;
}

/** `486 Busy Here (Q.850 17 User busy)`; empty when no final arrived. */
export function sipResponseLabel(m: SIPMetrics): string {
  if (!m.status_code) return '';
  let label = [m.status_code, m.reason].filter(Boolean).join(' ');
  const rh = m.reason_header;
  if (rh) {
    const cause = [rh.protocol, rh.cause, rh.text].filter((part) => part !== undefined && part !== '').join(' ');
    if (cause) label += ` (${cause})`;
  }
  return label;
}

/**
 * `expires in 12 days (Nov 1, 2026)`; empty without an expiry. Days come
 * from the worker's snapshot (whole days, truncated), the same number the
 * status page shows.
 */
export function certificateExpiryLabel(tls: TLSCertInfo): string {
  const days = tls.days_until_expiry;
  if (days === undefined) return '';
  const when = tls.not_after ? ` (${formatDate(tls.not_after)})` : '';
  if (days < 0) return `expired ${pluralize(-days, 'day')} ago${when}`;
  return `expires in ${pluralize(days, 'day')}${when}`;
}

/**
 * The facts for one envelope. minDaysValid is the monitor's
 * tls_min_days_valid: a certificate inside it reads as a warning, matching
 * the tls_expiry alert the alerter opens for it. media is the monitor's
 * call.media, whose thresholds tone the call-quality rows.
 */
export function sipFacts(m: SIPMetrics, minDaysValid?: number, media?: SIPMediaConfig): SIPFact[] {
  const facts: SIPFact[] = [];
  if (m.server) facts.push({ key: 'server', label: 'Answered by', value: m.server });
  const address = sipAddressLabel(m);
  if (address) facts.push({ key: 'address', label: 'Address', value: address });
  const attempts = m.resolution?.attempts ?? [];
  if (attempts.length > 1) {
    // The failover path, so a dead or refusing server shows even when the
    // check passed on the next one.
    facts.push({
      key: 'attempts',
      label: 'Tried',
      value: attempts.map((a) => `${a.address} ${a.outcome}`).join(' → '),
      tone: 'note',
    });
  }
  if (m.transport) {
    const transport = m.transport.toUpperCase();
    facts.push({
      key: 'transport',
      label: 'Transport',
      value: m.tls?.version ? `${transport} · ${m.tls.version}` : transport,
    });
  }
  const expiry = m.tls ? certificateExpiryLabel(m.tls) : '';
  if (expiry) {
    const days = m.tls?.days_until_expiry ?? 0;
    const warn = days < 0 || (minDaysValid !== undefined && days < minDaysValid);
    facts.push({ key: 'certificate', label: 'Certificate', value: expiry, tone: warn ? 'warning' : undefined });
  }

  const response = sipResponseLabel(m);
  if (response) facts.push({ key: 'response', label: 'Response', value: `${m.method} ${response}` });

  const timing = [
    m.timing.dns_ms !== undefined ? `DNS ${formatLatency(m.timing.dns_ms)}` : '',
    m.timing.connect_ms !== undefined ? `connect ${formatLatency(m.timing.connect_ms)}` : '',
    m.timing.tls_handshake_ms !== undefined ? `TLS ${formatLatency(m.timing.tls_handshake_ms)}` : '',
    m.timing.response_ms !== undefined ? `response ${formatLatency(m.timing.response_ms)}` : '',
  ].filter(Boolean);
  if (timing.length > 0) facts.push({ key: 'timing', label: 'Timing', value: timing.join(', ') });

  if (m.auth) {
    const scheme = m.auth.algorithm ? `digest ${m.auth.algorithm}` : 'digest';
    facts.push({
      key: 'auth',
      label: 'Auth',
      value: m.auth.rounds > 0 ? `${m.auth.challenge} ${scheme}` : `${m.auth.challenge} challenge, not answered`,
    });
  }
  if (m.retransmissions > 0) {
    facts.push({ key: 'retransmissions', label: 'Retransmits', value: pluralize(m.retransmissions, 'resend'), tone: 'note' });
  }
  if (m.stray_messages > 0) {
    facts.push({ key: 'stray', label: 'Ignored', value: pluralize(m.stray_messages, 'unmatched response'), tone: 'note' });
  }
  if (m.call) facts.push(...sipCallFacts(m.call, media));
  // The server's own Warning header (RFC 3261 §20.43): diagnostic text, not
  // a Probara threshold warning, so it reads as a note.
  if (m.warning) facts.push({ key: 'sip-warning', label: 'SIP warning', value: m.warning, tone: 'note' });
  // The check's own warnings (a warn threshold, an unconfirmed teardown, a
  // ring-mode call that was answered): the status stayed, the operator
  // should still know.
  // The Teardown row already shows this exact warning; any other teardown
  // warning (a forked dialog's BYE unconfirmed after a CANCEL that was not)
  // still gets its own row. The worker de-duplicates warnings, so skipping
  // on equality skips it once.
  const shownTeardownWarning =
    m.call?.teardown === 'bye_unconfirmed'
      ? 'teardown: BYE unconfirmed'
      : m.call?.teardown === 'cancel_unconfirmed'
        ? 'teardown: CANCEL unconfirmed'
        : undefined;
  (m.warnings ?? []).forEach((warning, i) => {
    if (warning === shownTeardownWarning) return;
    facts.push({ key: `warning-${i}`, label: 'Warning', value: warning, tone: 'warning' });
  });
  if (m.latency_warn_ms !== undefined && !(m.warnings && m.warnings.length > 0)) {
    facts.push({ key: 'latency-warn', label: 'Warning', value: `over the ${formatLatency(m.latency_warn_ms)} warning threshold`, tone: 'warning' });
  }
  if (m.call_id) facts.push({ key: 'call-id', label: 'Call-ID', value: m.call_id, copyable: true });
  return facts;
}

/**
 * The call's own facts (metrics_data.sip.call): post-dial delay with what
 * ended it, answer time, the negotiated codec, the measured audio (with
 * call.media; mediaConfig tones it) and how the call was torn down. An
 * unconfirmed teardown is a warning: the far end may still hold a billed
 * call open.
 */
export function sipCallFacts(call: SIPCallMetrics, mediaConfig?: SIPMediaConfig): SIPFact[] {
  const facts: SIPFact[] = [];
  if (call.pdd_ms !== undefined) {
    const source = call.pdd_source ? ` (${call.pdd_source})` : '';
    facts.push({ key: 'pdd', label: 'Post-dial delay', value: `${formatLatency(call.pdd_ms)}${source}` });
  }
  if (call.answer_ms !== undefined) {
    const hold = call.hold_ms > 0 ? `, held ${formatLatency(call.hold_ms)}` : '';
    facts.push({ key: 'answer', label: 'Answered in', value: `${formatLatency(call.answer_ms)}${hold}` });
  } else if (call.mode === 'ring' && call.pdd_ms !== undefined) {
    facts.push({ key: 'answer', label: 'Call', value: 'rang, cancelled before answer (ring mode)' });
  }
  const media = [
    call.sdp_answer?.codec ?? '',
    call.early_media ? 'early media' : '',
  ].filter(Boolean);
  if (media.length > 0) facts.push({ key: 'media', label: 'Media', value: media.join(' · ') });
  if (call.media) facts.push(...sipMediaFacts(call.media, mediaConfig));
  if (call.forked_answers > 0) {
    facts.push({ key: 'forks', label: 'Forked', value: `${pluralize(call.forked_answers, 'extra answer')}, each hung up`, tone: 'note' });
  }
  const teardown = sipTeardownLabel(call);
  if (teardown) {
    const unconfirmed = call.teardown === 'bye_unconfirmed' || call.teardown === 'cancel_unconfirmed';
    facts.push({ key: 'teardown', label: 'Teardown', value: teardown, tone: unconfirmed ? 'warning' : undefined });
  }
  return facts;
}

/** How the call ended, in words; empty for "none" (nothing to tear down). */
export function sipTeardownLabel(call: SIPCallMetrics): string {
  const bye = call.bye_ms !== undefined ? ` in ${formatLatency(call.bye_ms)}` : '';
  switch (call.teardown) {
    case 'bye_ok':
      return `BYE confirmed${bye}`;
    case 'bye_481':
      return 'BYE answered 481: the far end had already ended the call';
    case 'bye_unconfirmed':
      return 'BYE unconfirmed: the far end may still hold the call';
    case 'cancelled':
      return 'CANCEL confirmed (487)';
    case 'cancel_unconfirmed':
      return 'CANCEL unconfirmed: the far end may still be ringing';
    case 'answered_after_cancel':
      return `answered after CANCEL, hung up with BYE${bye}`;
    case 'remote_bye':
      return 'the callee hung up first';
    case 'no_provisional':
      return 'no response, nothing to cancel';
    case 'none':
    case '':
      return '';
    default: {
      const code = /^bye_(\d{3})$/.exec(call.teardown)?.[1];
      return code ? `BYE answered ${code}` : call.teardown;
    }
  }
}

/**
 * One line for a test-call Test result: `ringing after 850 ms, answered in
 * 4.2 s`. Empty without call metrics.
 */
export function sipCallSummary(call: SIPCallMetrics | undefined): string {
  if (!call) return '';
  const parts: string[] = [];
  // pdd_source 200: answered without ringing, so the PDD is the answer time.
  if (call.pdd_ms !== undefined && call.pdd_source !== '200') {
    parts.push(`ringing after ${formatLatency(call.pdd_ms)}`);
  }
  if (call.answer_ms !== undefined) parts.push(`answered in ${formatLatency(call.answer_ms)}`);
  const mos = call.media ? sipMediaWorst(call.media).mos : undefined;
  if (mos) parts.push(`MOS ${formatMOS(mos.value)}`);
  return parts.join(', ');
}

// ── Call quality (metrics_data.sip.call.media) ─────────────────────────────

/** The error a worker that predates call quality gives a call.media config. */
export const SIP_MEDIA_UNSUPPORTED_ERROR = 'call.media: media analysis is not available yet';

/**
 * The evidence bases a measured call can report, in reading order: the far
 * end's stream as the probe received it, the far end's RTCP reports on the
 * probe's stream, and the probe's own audio echoed back.
 */
export const SIP_MEDIA_BASES = [
  { key: 'inbound', short: 'in', name: 'Inbound' },
  { key: 'outbound', short: 'out', name: 'Outbound (RTCP)' },
  { key: 'round_trip', short: 'round trip', name: 'Round trip (echo)' },
] as const;
export type SIPMediaBasis = (typeof SIP_MEDIA_BASES)[number]['key'];

// Why an answer was unusable or its address refused (media.detail), in words.
const SIP_MEDIA_DETAIL: Record<string, string> = {
  no_sdp: 'no SDP',
  no_audio: 'no audio stream',
  profile: 'not RTP/AVP',
  inactive: 'inactive',
  no_address: 'no address',
  codec_not_offered: 'neither PCMU nor PCMA',
  family: 'another IP family',
  recvonly: 'recvonly',
  sendonly: 'sendonly',
  hostname: 'a hostname, never resolved',
  self: "the probe's own port",
  broadcast: "a broadcast address of the worker's own network",
  port: 'a port below 1024',
  unspecified: 'no usable address',
  multicast: 'a multicast or broadcast address',
  link_local: 'a link-local address',
  loopback: 'a loopback address',
  blocked_by_policy: "blocked by the worker's network policy",
  private_behind_public: 'a private address behind a public peer',
};

const mediaDetail = (code: string | undefined) => (code ? SIP_MEDIA_DETAIL[code] ?? code : '');

/**
 * The far-end packets or echoes that show audio flowed in a direction, and
 * the echoes the worker needs before it judges the echo's round trip rather
 * than RTCP's (sipMediaEvidence in worker/internal/worker/sip_media.go).
 */
const SIP_MEDIA_EVIDENCE = 5;

/**
 * Whether the worker judged the call's quality figures at all: it estimates
 * them whatever its own load, but skips every quality threshold when it was
 * too busy to time media (`tx.reliable: false`), so such figures are not a
 * verdict and get no tone, chart point or headline.
 */
export function sipMediaJudged(media: SIPMediaMetrics): boolean {
  return media.tx?.reliable !== false;
}

/**
 * One basis's figures that the quality thresholds read, or undefined when
 * the call did not measure it well enough to estimate (the worker sets
 * quality only for a long enough window): the MOS, the loss the loss
 * thresholds read (effective loss; RTCP's network loss outbound) and the
 * mean jitter. Whether they were judged is sipMediaJudged.
 */
export function sipMediaBasis(
  media: SIPMediaMetrics,
  basis: SIPMediaBasis
): { mos: number; lossPct: number; jitterMs?: number } | undefined {
  switch (basis) {
    case 'inbound': {
      const b = media.inbound;
      return b?.quality ? { mos: b.quality.mos_cqe, lossPct: b.effective_loss_pct, jitterMs: b.jitter_ms } : undefined;
    }
    case 'outbound': {
      const b = media.outbound;
      return b?.quality ? { mos: b.quality.mos_cqe, lossPct: b.loss_pct, jitterMs: b.jitter_ms } : undefined;
    }
    case 'round_trip': {
      const b = media.round_trip;
      return b?.quality ? { mos: b.quality.mos_cqe, lossPct: b.effective_loss_pct, jitterMs: b.jitter_ms } : undefined;
    }
  }
}

type BasisFigure = { value: number; basis: SIPMediaBasis };

/**
 * The worst figure any judged basis reported, with its basis: the lowest
 * MOS, the highest loss and the highest mean jitter, as the thresholds
 * judge every basis and the worst one decides. Each is undefined when no
 * basis measured it, and all are when the worker did not judge the run
 * (sipMediaJudged).
 */
export function sipMediaWorst(media: SIPMediaMetrics): { mos?: BasisFigure; loss?: BasisFigure; jitter?: BasisFigure } {
  const worst: { mos?: BasisFigure; loss?: BasisFigure; jitter?: BasisFigure } = {};
  if (!sipMediaJudged(media)) return worst;
  for (const { key } of SIP_MEDIA_BASES) {
    const b = sipMediaBasis(media, key);
    if (!b) continue;
    if (!worst.mos || b.mos < worst.mos.value) worst.mos = { value: b.mos, basis: key };
    if (!worst.loss || b.lossPct > worst.loss.value) worst.loss = { value: b.lossPct, basis: key };
    if (b.jitterMs !== undefined && (!worst.jitter || b.jitterMs > worst.jitter.value)) {
      worst.jitter = { value: b.jitterMs, basis: key };
    }
  }
  return worst;
}

/**
 * The measured audio's facts: why nothing was measured, else the MOS, loss
 * and jitter of every basis the call could judge (`4.31 in · 4.36 out`), the
 * round trip, whether audio flowed both ways, and a latched destination.
 * mediaConfig is the monitor's call.media: a MOS or loss past its warn or
 * failure threshold (status-tones' defaults without one), or a jitter or
 * round trip past its own, reads as a warning, as does two-way evidence
 * short of what `expect` asks. Figures from a run the worker was too busy to
 * time are shown untoned with a note, since it judged none of them.
 */
export function sipMediaFacts(media: SIPMediaMetrics, mediaConfig?: SIPMediaConfig): SIPFact[] {
  const facts: SIPFact[] = [];
  const detail = mediaDetail(media.detail);
  switch (media.status) {
    case 'unusable_answer':
      facts.push({
        key: 'media-status',
        label: 'Call audio',
        value: `far end answered without a usable audio stream${detail ? ` (${detail})` : ''}`,
        tone: 'warning',
      });
      return facts;
    case 'refused':
      // Refused with nothing latched: the probe sent nothing, but audio from
      // the named address still counts and is judged once enough arrived.
      if (!media.inbound || media.inbound.expected - media.inbound.lost < SIP_MEDIA_EVIDENCE) {
        facts.push({
          key: 'media-status',
          label: 'Call audio',
          value: `far end's media address refused${detail ? ` (${detail})` : ''}, nothing measured`,
          tone: 'warning',
        });
        return facts;
      }
      facts.push({
        key: 'media-status',
        label: 'Call audio',
        value: `far end's media address refused${detail ? ` (${detail})` : ''}: probe audio not sent, inbound measured`,
        tone: 'warning',
      });
      break;
    case 'not_held':
      facts.push({ key: 'media-status', label: 'Call audio', value: 'not measured: the call never reached its hold', tone: 'note' });
      return facts;
    case 'interrupted':
      facts.push({ key: 'media-status', label: 'Call audio', value: 'not measured: the worker shut down mid-call', tone: 'note' });
      return facts;
    case 'truncated':
      facts.push({
        key: 'media-status',
        label: 'Call audio',
        value: `only ${formatLatency(media.window_ms)} measured: quality not judged`,
        tone: 'note',
      });
      break;
  }

  const bases = SIP_MEDIA_BASES.flatMap(({ key, short }) => {
    const b = sipMediaBasis(media, key);
    return b ? [{ key, short, ...b }] : [];
  });
  // A figure past its threshold reads as a warning, but only when the
  // worker judged the run.
  const judged = sipMediaJudged(media);
  const flag = (past: boolean): SIPFact['tone'] => (judged && past ? 'warning' : undefined);
  if (bases.length > 0) {
    const quality = [media.inbound?.quality, media.outbound?.quality, media.round_trip?.quality];
    const unmeasuredDelay = quality.some((q) => q?.delay_source === 'unmeasured');
    const assumptions = (media.assumptions ?? []).map((a) =>
      a === 'g711_plc' ? 'assumes the far end conceals lost packets (G.711 PLC)' : `assumes ${a}`
    );
    const notes = [
      'estimate',
      ...(unmeasuredDelay ? ['delay unmeasured'] : []),
      ...(judged ? [] : ['not judged: the worker was too busy to time media']),
    ];
    facts.push({
      key: 'media-mos',
      label: 'Call quality',
      value: `MOS ${bases.map((b) => `${formatMOS(b.mos)} ${b.short}`).join(' · ')} (${notes.join(', ')})`,
      tone: judged
        ? flag(bases.some((b) => mosTone(b.mos, mediaConfig?.warn_mos, mediaConfig?.min_mos) !== 'good'))
        : 'note',
      tip: [media.model, ...assumptions].filter(Boolean).join('; ') || undefined,
    });
    facts.push({
      key: 'media-loss',
      label: 'Loss',
      value: bases.map((b) => `${formatLossPct(b.lossPct)} ${b.short}`).join(' · '),
      tone: flag(bases.some((b) => lossTone(b.lossPct, mediaConfig?.warn_loss_pct, mediaConfig?.max_loss_pct) !== 'good')),
    });
    const jitter = bases.filter((b) => b.jitterMs !== undefined);
    if (jitter.length > 0) {
      const inboundMax = media.inbound?.jitter_max_ms;
      facts.push({
        key: 'media-jitter',
        label: 'Jitter',
        value: jitter
          .map((b) => {
            const max = b.key === 'inbound' && inboundMax !== undefined ? ` (max ${formatJitter(inboundMax)})` : '';
            return `${formatJitter(b.jitterMs)} ${b.short}${max}`;
          })
          .join(' · '),
        tone: flag(
          jitter.some(
            (b) => thresholdTone(b.jitterMs, mediaConfig?.warn_jitter_ms, mediaConfig?.max_jitter_ms) !== 'good'
          )
        ),
      });
    }
  }

  // The round trip the RTT thresholds judge: the echo's median once enough
  // echoes matched, else RTCP's.
  const echo = media.round_trip;
  const rtt =
    echo?.rtt_ms && echo.matched >= SIP_MEDIA_EVIDENCE
      ? { ms: echo.rtt_ms.p50, value: `${formatLatency(echo.rtt_ms.p50)} (echo, p95 ${formatLatency(echo.rtt_ms.p95)})` }
      : media.outbound?.rtt_ms !== undefined
        ? { ms: media.outbound.rtt_ms, value: `${formatLatency(media.outbound.rtt_ms)} (RTCP)` }
        : undefined;
  if (rtt) {
    facts.push({
      key: 'media-rtt',
      label: 'Round trip',
      value: rtt.value,
      tone: flag(thresholdTone(rtt.ms, mediaConfig?.warn_rtt_ms, mediaConfig?.max_rtt_ms) !== 'good'),
    });
  }

  if (media.two_way) {
    const evidence = (media.two_way_evidence ?? []).filter((e) => e !== 'rx').map((e) => (e === 'rtcp' ? 'RTCP' : e));
    const value =
      media.two_way === 'verified'
        ? `verified${evidence.length > 0 ? ` (${evidence.join(', ')})` : ''}`
        : media.two_way === 'inbound_only'
          ? "inbound only: nothing showed the far end heard the probe"
          : media.two_way === 'outbound_only'
            ? 'outbound only: no audio from the far end'
            : 'none: no audio either way';
    // What expect asked of the call: far-end audio for audio, both ways for
    // two_way and echo; measure-only asks nothing.
    const short =
      media.expect === 'audio'
        ? media.two_way === 'outbound_only' || media.two_way === 'none'
        : media.expect !== 'none' && media.two_way !== 'verified';
    facts.push({ key: 'media-two-way', label: 'Two-way audio', value, tone: short ? 'warning' : undefined });
  }

  if (media.latched === 'port') {
    facts.push({ key: 'media-latched', label: 'Audio sent to', value: "the port the far end's audio came from (latched)", tone: 'note' });
  } else if (media.latched === 'peer') {
    facts.push({
      key: 'media-latched',
      label: 'Audio sent to',
      value: "the signalling peer (latched: the answer's address was refused)",
      tone: 'note',
    });
  }
  return facts;
}

/**
 * The upgrade hint for a check a pre-call-quality worker refused (its error
 * names SIP_MEDIA_UNSUPPORTED_ERROR); empty for any other error.
 */
export function sipMediaUnsupportedHint(errorMessage: string | undefined): string {
  if (!errorMessage?.includes(SIP_MEDIA_UNSUPPORTED_ERROR)) return '';
  return 'This worker predates call-quality measurement: upgrade it, or turn call quality off';
}

/**
 * Where the newest check, per location, was refused by a worker that
 * predates call quality, as an upgrade hint naming those locations (the
 * default fleet when a result has none); empty when no location's latest
 * check was. results are newest first.
 */
export function sipMediaOutdatedWorkersHint(results: CheckResult[]): string {
  const seen = new Set<string>();
  const outdated: string[] = [];
  for (const result of results) {
    const key = result.location_id ?? '';
    if (seen.has(key)) continue;
    seen.add(key);
    if (sipMediaUnsupportedHint(result.error_message)) outdated.push(result.location_name || key || 'the default fleet');
  }
  if (outdated.length === 0) return '';
  const many = outdated.length > 1;
  return `Upgrade the worker${many ? 's' : ''} at ${outdated.join(', ')}: ${many ? 'they predate' : 'it predates'} call-quality measurement and ${many ? 'refuse' : 'refuses'} this monitor`;
}

/**
 * The warnings a Test result should not hide behind a green "Passed": the
 * worker's own (a ring-mode call that was answered and is billable, an
 * unconfirmed BYE or CANCEL, a warn threshold), else the warn threshold
 * alone, as sipFacts reads latency_warn_ms. Empty for a clean pass.
 */
export function sipTestWarnings(m: SIPMetrics | undefined): string[] {
  const warnings = m?.warnings ?? [];
  if (warnings.length > 0) return warnings;
  if (m?.latency_warn_ms !== undefined) return [`over the ${formatLatency(m.latency_warn_ms)} warning threshold`];
  return [];
}

/**
 * `203.0.113.7:5061 (sbc2.example.com via SRV, after failover)`: the address
 * that decided the check, and how DNS led there when it was not the
 * monitor's own host (an SRV target, reached through NAPTR or not; a
 * failover).
 */
export function sipAddressLabel(m: SIPMetrics): string {
  const target = m.target;
  if (!target?.address) return '';
  const notes: string[] = [];
  if (target.resolved_via === 'srv') notes.push(`${target.host} via SRV`);
  if (target.resolved_via === 'naptr') notes.push(`${target.host} via NAPTR→SRV`);
  if (target.failover) notes.push('after failover');
  return notes.length > 0 ? `${target.address} (${notes.join(', ')})` : target.address;
}

/** metrics_data.sip.call of one snapshot, or undefined (metric-snapshots pickSnapshots). */
export function pickSIPCall(metricsData: unknown): SIPCallMetrics | undefined {
  return (metricsData as SIPMetricsEnvelope | null | undefined)?.sip?.call ?? undefined;
}

type Point = [number, number];

/**
 * The test-call timing series the detail page charts (SipCallPanels), one
 * point per check that measured it:
 * - pdd: post-dial delay from the INVITE that drew it (the authenticated one
 *   when challenged) to the first 18x, or to the 200 when none came;
 * - answer: from that INVITE to the 200;
 * - setupPdd / setup: the same ends measured from the first INVITE, so they
 *   include the digest round trip. Empty unless some check was challenged
 *   (they then differ from pdd / answer), so an unchallenged trunk draws one
 *   line per panel.
 * Checks that stopped before the event (no ringing, no answer, no response)
 * add no point, which leaves a gap in the line.
 */
export function sipCallSeries(snapshots: TimedSnapshot<SIPCallMetrics>[]): {
  pdd: Point[];
  setupPdd: Point[];
  answer: Point[];
  setup: Point[];
} {
  const pdd: Point[] = [];
  const setupPdd: Point[] = [];
  const answer: Point[] = [];
  const setup: Point[] = [];
  let pddDiffers = false;
  let answerDiffers = false;
  for (const { ts, m } of snapshots) {
    if (m.pdd_ms !== undefined) {
      pdd.push([ts, m.pdd_ms]);
      const viaSetup = m.setup_pdd_ms ?? m.pdd_ms;
      setupPdd.push([ts, viaSetup]);
      if (viaSetup !== m.pdd_ms) pddDiffers = true;
    }
    if (m.answer_ms !== undefined) {
      answer.push([ts, m.answer_ms]);
      const viaSetup = m.setup_ms ?? m.answer_ms;
      setup.push([ts, viaSetup]);
      if (viaSetup !== m.answer_ms) answerDiffers = true;
    }
  }
  return {
    pdd,
    setupPdd: pddDiffers ? setupPdd : [],
    answer,
    setup: answerDiffers ? setup : [],
  };
}

/**
 * The test-call quality series the detail page charts (SipCallPanels), per
 * evidence basis: the MOS estimate, the loss the thresholds judge and the
 * mean jitter. A check adds a point to a basis only when it measured that
 * basis well enough to estimate its quality on a run the worker judged
 * (sipMediaJudged), the same gates the thresholds use, so a call cut short
 * or timed by an overloaded worker leaves a gap rather than a noisy spike.
 */
export function sipMediaSeries(
  snapshots: TimedSnapshot<SIPCallMetrics>[]
): Record<'mos' | 'loss' | 'jitter', Record<SIPMediaBasis, Point[]>> {
  const empty = (): Record<SIPMediaBasis, Point[]> => ({ inbound: [], outbound: [], round_trip: [] });
  const series = { mos: empty(), loss: empty(), jitter: empty() };
  for (const { ts, m } of snapshots) {
    if (!m.media || !sipMediaJudged(m.media)) continue;
    for (const { key } of SIP_MEDIA_BASES) {
      const b = sipMediaBasis(m.media, key);
      if (!b) continue;
      series.mos[key].push([ts, b.mos]);
      series.loss[key].push([ts, b.lossPct]);
      if (b.jitterMs !== undefined) series.jitter[key].push([ts, b.jitterMs]);
    }
  }
  return series;
}

/**
 * The warn / max thresholds of one call timing as chart reference lines
 * (`warn 3.0 s`, `fail 5.0 s`), in the MongoDB panels' wording. format
 * labels other units (a MOS, a loss percentage); for MOS pass min_mos as
 * the failure line.
 */
export function sipCallThresholdLines(
  warn?: number,
  fail?: number,
  format: (value: number) => string = formatLatency
): { y: number; label: string }[] {
  const lines: { y: number; label: string }[] = [];
  if (warn !== undefined && warn > 0) lines.push({ y: warn, label: `warn ${format(warn)}` });
  if (fail !== undefined && fail > 0) lines.push({ y: fail, label: `fail ${format(fail)}` });
  return lines;
}
