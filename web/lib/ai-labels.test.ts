import assert from 'node:assert/strict';
import test from 'node:test';
import {
  buildIncidentLabelRequest, choiceTitle, dependencyLabelRequest, labelControlHasContent, labelErrorMessage,
  labelExportOutcome, labelSummary, parseExportCount,
} from './ai-labels';
import type { AILabel, AILabelChoice } from './types';

const choices: AILabelChoice[] = [
  { key: 'dns_failure', title: 'DNS resolution failure' },
  { key: 'tls_problem', title: 'TLS or certificate problem' },
  { key: 'insufficient_evidence', title: 'Insufficient evidence' },
];

function label(over: Partial<AILabel>): AILabel {
  return {
    subject_type: 'incident_analysis',
    subject_id: 'a1',
    verdict: 'confirmed',
    answers: {},
    catalogue_version: 'probara-evidence-v1',
    labeled_at: '2026-10-11T09:30:00Z',
    ...over,
  };
}

test('confirmed request carries only the analysis and verdict', () => {
  assert.deepEqual(buildIncidentLabelRequest('a1', 'confirmed'), { analysis_id: 'a1', verdict: 'confirmed' });
  // A stray incident label is not sent with confirmed: the server derives it.
  assert.deepEqual(buildIncidentLabelRequest('a1', 'confirmed', 'dns_failure'), { analysis_id: 'a1', verdict: 'confirmed' });
  assert.deepEqual(
    buildIncidentLabelRequest('a1', 'confirmed', undefined, { d1: 'tls_problem' }),
    { analysis_id: 'a1', verdict: 'confirmed', answers: { d1: 'tls_problem' } },
  );
});

test('corrected request includes the incident label and non-empty overrides', () => {
  assert.deepEqual(
    buildIncidentLabelRequest('a1', 'corrected', 'dns_failure', { d1: 'insufficient_evidence', d2: '' }),
    { analysis_id: 'a1', verdict: 'corrected', incident_label: 'dns_failure', answers: { d1: 'insufficient_evidence' } },
  );
  // Only "follow the actual cause" overrides: answers is omitted entirely.
  assert.deepEqual(
    buildIncidentLabelRequest('a1', 'corrected', 'dns_failure', { d1: '', d2: '' }),
    { analysis_id: 'a1', verdict: 'corrected', incident_label: 'dns_failure' },
  );
});

test('choiceTitle falls back to the key', () => {
  assert.equal(choiceTitle(choices, 'tls_problem'), 'TLS or certificate problem');
  assert.equal(choiceTitle(choices, 'upstream_failure'), 'upstream_failure');
  assert.equal(choiceTitle([], 'dns_failure'), 'dns_failure');
});

test('labelSummary names the verdict and who gave it', () => {
  assert.equal(labelSummary(label({ labeled_by: 'u1' }), choices, 'u1'), 'Confirmed by you');
  assert.equal(labelSummary(label({ labeled_by: 'u2' }), choices, 'u1'), 'Confirmed by a teammate');
  assert.equal(labelSummary(label({ labeled_by: 'u2' }), choices), 'Confirmed by a teammate');
  assert.equal(labelSummary(label({ labeled_by: null }), choices, 'u1'), 'Confirmed');
  assert.equal(labelSummary(label({}), choices, 'u1'), 'Confirmed');
  assert.equal(
    labelSummary(label({ verdict: 'corrected', incident_label: 'dns_failure', labeled_by: 'u1' }), choices, 'u1'),
    'Corrected to DNS resolution failure by you',
  );
  assert.equal(
    labelSummary(label({ verdict: 'corrected', incident_label: 'upstream_failure', labeled_by: 'u3' }), choices, 'u1'),
    'Corrected to upstream_failure by a teammate',
  );
  assert.equal(labelSummary(label({ verdict: 'corrected' }), choices, 'u1'), 'Labelled');
  assert.equal(labelSummary(label({ verdict: 'rejected' }), choices, 'u1'), 'Labelled');
});

test('dependencyLabelRequest keeps the suggestion orientation', () => {
  const s = { monitor_id: 'm1', depends_on_id: 'm2', monitor_name: 'api', depends_on_name: 'db', reason: '', confidence: 'high' };
  assert.deepEqual(dependencyLabelRequest(s, 'confirmed'), { monitor_id: 'm1', depends_on_id: 'm2', verdict: 'confirmed' });
  // corrected is meaningless with confirmed; the server would reject it.
  assert.deepEqual(dependencyLabelRequest(s, 'confirmed', 'reversed'), { monitor_id: 'm1', depends_on_id: 'm2', verdict: 'confirmed' });
  assert.deepEqual(dependencyLabelRequest(s, 'rejected'), { monitor_id: 'm1', depends_on_id: 'm2', verdict: 'rejected' });
  assert.deepEqual(
    dependencyLabelRequest(s, 'rejected', 'shared_upstream'),
    { monitor_id: 'm1', depends_on_id: 'm2', verdict: 'rejected', corrected: 'shared_upstream' },
  );
});

test('labelErrorMessage reads API error objects and Errors', () => {
  assert.equal(labelErrorMessage({ error: 'analysis_not_labelable', message: 'Not labelable' }, 'x'), 'Not labelable');
  assert.equal(labelErrorMessage(new Error('boom'), 'x'), 'boom');
  assert.equal(labelErrorMessage({ message: '' }, 'fallback'), 'fallback');
  assert.equal(labelErrorMessage(null, 'fallback'), 'fallback');
});

test('export counts parse only non-negative integers', () => {
  assert.equal(parseExportCount('0'), 0);
  assert.equal(parseExportCount(' 12 '), 12);
  assert.equal(parseExportCount(null), null);
  assert.equal(parseExportCount(''), null);
  assert.equal(parseExportCount('-1'), null);
  assert.equal(parseExportCount('1.5'), null);
  assert.equal(parseExportCount('abc'), null);
});

test('an empty export skips the 0-byte download', () => {
  assert.deepEqual(labelExportOutcome(0, 0), { download: false, message: 'No labels to export yet.', tone: 'info' });
  assert.deepEqual(labelExportOutcome(0, null), { download: false, message: 'No labels to export yet.', tone: 'info' });
  const allSkipped = labelExportOutcome(0, 3);
  assert.equal(allSkipped.download, false);
  assert.equal(allSkipped.tone, 'warning');
  assert.match(allSkipped.message, /3 labels were skipped/);
});

test('a non-empty export downloads and reports skipped labels', () => {
  assert.deepEqual(labelExportOutcome(2, 0), { download: true, message: 'Downloaded 2 training rows', tone: 'success' });
  assert.deepEqual(labelExportOutcome(1, null), { download: true, message: 'Downloaded 1 training row', tone: 'success' });
  const partial = labelExportOutcome(5, 1);
  assert.equal(partial.download, true);
  assert.equal(partial.tone, 'warning');
  assert.match(partial.message, /^Downloaded 5 training rows; 1 label was skipped/);
  // Missing headers (counts unknown) keep the plain download.
  assert.deepEqual(labelExportOutcome(null, null), { download: true, message: 'Training labels downloaded', tone: 'success' });
});

test('label control renders nothing while identity loads on an unlabelled analysis', () => {
  assert.equal(labelControlHasContent(false, false, true), false);
  assert.equal(labelControlHasContent(true, false, true), true); // stored label summary
  assert.equal(labelControlHasContent(false, true, false), true); // prompt
  assert.equal(labelControlHasContent(false, false, false), true); // viewer note
});
