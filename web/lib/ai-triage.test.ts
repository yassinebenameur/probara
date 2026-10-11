import assert from 'node:assert/strict';
import test from 'node:test';
import {
  TRIAGE_PAGING_NOTE, buildTriageLabelRequest, markTriageLabelled, mergeAlertUpdate, parseSettingInput,
  triageMenuCopy, triagePresentation, triageSettingsForSave, validateTriageSettings,
} from './ai-triage';
import { labelErrorMessage } from './ai-labels';
import { AI_CONFIDENCE_TONE, aiConfidenceTone } from './status-tones';
import type { Alert, AlertTriage } from './types';

function triage(over: Partial<AlertTriage>): AlertTriage {
  return {
    id: 't1',
    status: 'ready',
    hypothesis: 'dns_failure',
    title: 'DNS resolution failure',
    confidence: 'high',
    summary: 'DNS lookups failed from every location.',
    skip_reason: null,
    error_message: null,
    created_at: '2026-10-11T09:00:00Z',
    completed_at: '2026-10-11T09:00:05Z',
    labelled: false,
    labelable: true,
    ...over,
  };
}

function alert(over: Partial<Alert>): Alert {
  return {
    id: 'a1',
    tenant_id: 'ten',
    monitor_id: 'm1',
    status: 'active',
    triggered_at: '2026-10-11T08:59:00Z',
    failure_count: 3,
    kind: 'availability',
    created_at: '2026-10-11T08:59:00Z',
    updated_at: '2026-10-11T08:59:00Z',
    ...over,
  };
}

test('aiConfidenceTone maps levels and falls back to neutral', () => {
  assert.equal(aiConfidenceTone('high'), 'success');
  assert.equal(aiConfidenceTone('medium'), 'info');
  assert.equal(aiConfidenceTone('low'), 'neutral');
  assert.equal(aiConfidenceTone(null), 'neutral');
  assert.equal(aiConfidenceTone('bogus'), 'neutral');
  // Confidence is never toned as an alarm.
  assert.ok(!Object.values(AI_CONFIDENCE_TONE).some((t) => t === 'danger' || t === 'warning'));
});

test('no triage row renders nothing', () => {
  assert.equal(triagePresentation(undefined).visible, false);
  assert.equal(triagePresentation(null).visible, false);
});

test('ready supported triage names the suspected cause with its confidence', () => {
  const p = triagePresentation(triage({}));
  assert.deepEqual(p, {
    visible: true,
    label: 'Suspected DNS resolution failure · high',
    tone: 'success',
    tooltip: `DNS lookups failed from every location.\n\n${TRIAGE_PAGING_NOTE}`,
    muted: false,
  });
  assert.equal(triagePresentation(triage({ confidence: 'medium' })).tone, 'info');
  assert.equal(triagePresentation(triage({ confidence: 'low' })).tone, 'neutral');
  // A missing title falls back to the catalogue key; a missing confidence drops the suffix.
  assert.equal(triagePresentation(triage({ title: null, confidence: null })).label, 'Suspected dns_failure');
  // No summary: the tooltip is just the paging note.
  assert.equal(triagePresentation(triage({ summary: null })).tooltip, TRIAGE_PAGING_NOTE);
});

test('ready abstention reads as no clear cause', () => {
  const p = triagePresentation(triage({ hypothesis: 'insufficient_evidence', title: 'Insufficient evidence', confidence: 'low' }));
  assert.equal(p.visible, true);
  assert.equal(p.label, 'No clear cause');
  assert.equal(p.tone, 'neutral');
  assert.equal(p.muted, false);
  assert.match(p.tooltip, /DNS lookups failed/);
});

test('pending, failed and skipped states are muted', () => {
  const pending = triagePresentation(triage({ status: 'pending', hypothesis: null, title: null, confidence: null, summary: null, completed_at: null, labelable: false }));
  assert.equal(pending.label, 'Triaging…');
  assert.equal(pending.muted, true);
  assert.equal(pending.visible, true);

  const failed = triagePresentation(triage({ status: 'failed', hypothesis: null, error_message: 'local AI unavailable or returned invalid decisions', labelable: false }));
  assert.equal(failed.label, 'Triage failed');
  assert.equal(failed.tooltip, 'local AI unavailable or returned invalid decisions');
  assert.equal(failed.muted, true);
  assert.equal(triagePresentation(triage({ status: 'failed', error_message: null })).tooltip, 'Automatic triage failed.');

  for (const reason of ['cooldown', 'resolved', 'disabled'] as const) {
    const p = triagePresentation(triage({ status: 'skipped', skip_reason: reason, hypothesis: null, labelable: false }));
    assert.equal(p.visible, true, reason);
    assert.equal(p.label, 'Not triaged');
    assert.equal(p.muted, true);
    assert.match(p.tooltip, /^Not triaged: /);
  }
  assert.match(triagePresentation(triage({ status: 'skipped', skip_reason: 'cooldown' })).tooltip, /cooldown/);
});

test('skipped not_modeled triage is hidden', () => {
  assert.equal(triagePresentation(triage({ status: 'skipped', skip_reason: 'not_modeled' })).visible, false);
});

test('mergeAlertUpdate keeps the listed triage when a stream event carries none', () => {
  const t = triage({});
  const prev = alert({ triage: t });
  const incoming = alert({ status: 'acknowledged' });
  const merged = mergeAlertUpdate(prev, incoming);
  assert.equal(merged.status, 'acknowledged');
  assert.equal(merged.triage, t);
  // A REST response that carries triage wins.
  const fresh = triage({ labelled: true });
  assert.equal(mergeAlertUpdate(prev, alert({ triage: fresh })).triage, fresh);
  // Nothing to keep: the incoming object is returned as is.
  const bare = alert({});
  assert.equal(mergeAlertUpdate(undefined, bare), bare);
  assert.equal(mergeAlertUpdate(alert({}), bare), bare);
});

test('buildTriageLabelRequest sends the label only when correcting', () => {
  assert.deepEqual(buildTriageLabelRequest('confirmed'), { verdict: 'confirmed' });
  assert.deepEqual(buildTriageLabelRequest('confirmed', 'dns_failure'), { verdict: 'confirmed' });
  assert.deepEqual(
    buildTriageLabelRequest('corrected', 'tls_problem', { d1: 'dns_failure', d2: '' }),
    { verdict: 'corrected', label: 'tls_problem', answers: { d1: 'dns_failure' } },
  );
});

test('validateTriageSettings mirrors the API ranges with operator copy', () => {
  assert.deepEqual(validateTriageSettings({ triage_delay_seconds: 60, triage_cooldown_seconds: 900, triage_max_per_hour: 60 }), {});
  assert.deepEqual(validateTriageSettings({ triage_delay_seconds: 0, triage_cooldown_seconds: 0, triage_max_per_hour: 1 }), {});
  assert.deepEqual(validateTriageSettings({ triage_delay_seconds: 3600, triage_cooldown_seconds: 86400, triage_max_per_hour: 600 }), {});
  assert.deepEqual(validateTriageSettings({ triage_delay_seconds: -1, triage_cooldown_seconds: 86401, triage_max_per_hour: 0 }), {
    triage_delay_seconds: 'Must be a whole number from 0 to 3600',
    triage_cooldown_seconds: 'Must be a whole number from 0 to 86400',
    triage_max_per_hour: 'Must be a whole number from 1 to 600',
  });
  assert.deepEqual(Object.keys(validateTriageSettings({ triage_delay_seconds: 3601, triage_cooldown_seconds: -1, triage_max_per_hour: 601 })).length, 3);
  // Fractions and empty inputs are rejected (the API takes integers).
  assert.ok(validateTriageSettings({ triage_delay_seconds: 1.5, triage_cooldown_seconds: 900, triage_max_per_hour: 60 }).triage_delay_seconds);
  assert.ok(validateTriageSettings({ triage_delay_seconds: parseSettingInput(''), triage_cooldown_seconds: 900, triage_max_per_hour: 60 }).triage_delay_seconds);
  assert.equal(parseSettingInput(' 120 '), 120);
  assert.ok(Number.isNaN(parseSettingInput('abc')));
});

test('labelErrorMessage maps the triage 409 codes', () => {
  assert.equal(
    labelErrorMessage({ error: 'triage_not_ready', message: 'triage_not_ready' }, 'x'),
    'This alert has no completed triage to label yet.',
  );
  assert.equal(
    labelErrorMessage({ error: 'triage_not_labelable', message: '' }, 'x'),
    'This triage sent no decisions to the model, so it cannot be labelled.',
  );
  assert.equal(labelErrorMessage({ error: 'validation_error', message: 'verdict must be "confirmed" or "corrected"' }, 'x'), 'verdict must be "confirmed" or "corrected"');
});

test('markTriageLabelled patches the current row and keeps its live state', () => {
  // The list as it is now: a live event resolved a1 after the label action began.
  const live = alert({ status: 'resolved', resolved_at: '2026-10-11T09:05:00Z', triage: triage({}) });
  const other = alert({ id: 'a2', triage: triage({ id: 't2' }) });
  const bare = alert({ id: 'a3' });
  const next = markTriageLabelled([live, other, bare], 'a1');
  assert.equal(next[0].status, 'resolved');
  assert.equal(next[0].resolved_at, '2026-10-11T09:05:00Z');
  assert.equal(next[0].triage?.labelled, true);
  assert.equal(next[1], other);
  assert.equal(next[2], bare);
  // No triage on the row: nothing to flag.
  assert.equal(markTriageLabelled([bare], 'a3')[0], bare);
  // The input list is not mutated.
  assert.equal(live.triage?.labelled, false);
});

test('triageMenuCopy does not offer to confirm a cause for an abstention', () => {
  assert.deepEqual(triageMenuCopy(triage({})), { confirm: 'Confirm cause', correct: 'Correct cause…', change: 'Change label…' });
  const abstain = triageMenuCopy(triage({ hypothesis: 'insufficient_evidence' }));
  assert.equal(abstain.confirm, 'Confirm no cause');
  assert.equal(abstain.correct, 'Name the cause…');
  assert.deepEqual(triageMenuCopy(triage({ hypothesis: null })), abstain);
  // Short enough for the fixed 160px RowMenu (about 110px of 13px text).
  for (const copy of [triageMenuCopy(triage({})), abstain]) {
    for (const text of Object.values(copy)) assert.ok(text.length <= 16, text);
  }
});

test('triageSettingsForSave skips locked fields and sends the server values', () => {
  const server = { auto_triage_enabled: true, triage_notifications_enabled: true, triage_delay_seconds: 60, triage_cooldown_seconds: 900, triage_max_per_hour: 60 };
  const broken = { auto_triage_enabled: false, triage_notifications_enabled: false, triage_delay_seconds: 60, triage_cooldown_seconds: 900, triage_max_per_hour: parseSettingInput('') };
  // Locked (AI draft toggle off): the uneditable draft neither blocks nor leaks.
  assert.deepEqual(triageSettingsForSave(true, broken, server), { payload: server, errors: {} });
  // Unlocked: the draft is validated and sent.
  assert.ok(triageSettingsForSave(false, broken, server).errors.triage_max_per_hour);
  const draft = { ...server, triage_delay_seconds: 120 };
  assert.deepEqual(triageSettingsForSave(false, draft, server), { payload: draft, errors: {} });
  // The follow-up opt-in travels with the draft, and locked keeps the server's.
  const optOut = { ...server, triage_notifications_enabled: false };
  assert.equal(triageSettingsForSave(false, optOut, server).payload.triage_notifications_enabled, false);
  assert.equal(triageSettingsForSave(true, optOut, server).payload.triage_notifications_enabled, true);
  // Extra server fields (the full AISettings object) are not forwarded.
  const wide = { ...server, enabled: true, model: 'm' };
  assert.deepEqual(Object.keys(triageSettingsForSave(true, draft, wide).payload).sort(), Object.keys(server).sort());
});
