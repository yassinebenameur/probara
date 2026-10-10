import assert from 'node:assert/strict';
import test from 'node:test';
import {
  collectorHealth, collectorManifest, coverageGaps, coverageQueryString, errorMessage, gapInfo, isDuplicateTarget, isNotFound, manifestInputErrors,
  parseNamespaces, serviceSuggestions, suggestionTarget, validNamespace, type DiscoveryIntegration, type DiscoveryService,
} from './discovery';

const integration: DiscoveryIntegration = {
  id: 'c952c53e-d8c8-4e16-8e94-a9ec57690c11', name: 'Production',
  namespaces: ['payments', 'orders'], location_id: null, last_seen_at: null, revision: 0,
};
test('collector deployment grants only namespaced inventory reads', () => {
  const docs = collectorManifest(integration, 'https://api.example.com', 'example/collector:v1', 'test-token').split('\n---\n').map((v) => JSON.parse(v));
  const roles = docs.filter((d) => d.kind === 'Role');
  assert.deepEqual(roles.map((d) => d.metadata.namespace), ['payments', 'orders']);
  for (const role of roles) {
    assert.deepEqual(role.rules, [
      { apiGroups: [''], resources: ['services'], verbs: ['list'] },
      { apiGroups: ['networking.k8s.io'], resources: ['ingresses'], verbs: ['list'] },
    ]);
  }
  assert.equal(docs.some((d) => d.kind === 'ClusterRole' || d.kind === 'ClusterRoleBinding'), false);
  const pod = docs.find((d) => d.kind === 'Deployment').spec.template.spec;
  assert.equal(pod.securityContext.runAsNonRoot, true);
  assert.equal(pod.volumes[0].secret.secretName, docs.find((d) => d.kind === 'Secret').metadata.name);
  assert.equal(pod.containers[0].securityContext.readOnlyRootFilesystem, true);
  assert.equal(pod.containers[0].env.some((e: { name: string }) => e.name === 'PROBARA_API_KEY'), false);
});
test('manifest keeps values literal and rejects unsafe API origins', () => {
  const docs = collectorManifest(integration, 'https://api.example.com', 'example/collector:v1', 'a"\nb').split('\n---\n').map((v) => JSON.parse(v));
  assert.equal(docs.find((d) => d.kind === 'Secret').stringData.token, 'a"\nb');
  for (const origin of ['http://api.example.com', 'https://user:pass@api.example.com', 'https://api.example.com/path', 'https://api.example.com/?token=secret']) {
    assert.throws(() => collectorManifest(integration, origin, 'example/collector:v1', 'token'));
  }
});

test('coverage query only sends set filters', () => {
  assert.equal(coverageQueryString({ page: 1 }), 'page=1');
  assert.equal(coverageQueryString({ page: 2, q: '  pay ', gaps: true, sort: 'gaps' }), 'page=2&q=pay&gaps=true&sort=gaps');
  assert.equal(coverageQueryString({ page: 1, q: '   ', gaps: false, sort: 'name' }), 'page=1');
});
test('namespace input is split, lowercased and de-duplicated', () => {
  assert.deepEqual(parseNamespaces('payments, Orders  payments\norders'), ['payments', 'orders']);
  assert.equal(validNamespace('payments'), true);
  assert.equal(validNamespace('bad_ns'), false);
  assert.equal(validNamespace('-lead'), false);
  assert.equal(validNamespace('a'.repeat(64)), false);
});
test('install form validation matches the manifest guard', () => {
  assert.deepEqual(manifestInputErrors('https://probara.example.com', 'example/collector:v1'), {});
  assert.ok(manifestInputErrors('http://probara.example.com', 'img').origin);
  assert.ok(manifestInputErrors('https://probara.example.com/api', 'img').origin);
  assert.ok(manifestInputErrors('not a url', 'img').origin);
  assert.ok(manifestInputErrors('https://probara.example.com', 'bad image').image);
  assert.ok(manifestInputErrors('https://probara.example.com', '  ').image);
});
test('collector health follows the 15-minute staleness bound', () => {
  const now = Date.parse('2026-09-29T10:00:00Z');
  assert.equal(collectorHealth(null, now), 'waiting');
  assert.equal(collectorHealth('2026-09-29T09:50:00Z', now), 'syncing');
  assert.equal(collectorHealth('2026-09-29T09:40:00Z', now), 'stale');
});
test('suggestions skip missing resources and repeated keys', () => {
  const s = (key: string) => ({ key, type: 'tcp' as const, target: 'a.b.svc', port: 1, internal: true, reason: '' });
  const service: DiscoveryService = {
    id: 's', name: 'svc', namespace: 'ns', monitors: [], gaps: [], candidates: {},
    resources: [
      { uid: '1', kind: 'Service', name: 'a', missing_since: null, suggestions: [s('k1'), s('k2')] },
      { uid: '2', kind: 'Ingress', name: 'b', missing_since: null, suggestions: [s('k2'), s('k3')] },
      { uid: '3', kind: 'Service', name: 'c', missing_since: '2026-09-29T09:00:00Z', suggestions: [s('k4')] },
    ],
  };
  assert.deepEqual(serviceSuggestions(service).map((x) => [x.suggestion.key, x.resource.uid]), [['k1', '1'], ['k2', '1'], ['k3', '2']]);
  assert.equal(suggestionTarget(s('x')), 'a.b.svc:1');
});
test('API error objects keep their message and code', () => {
  assert.equal(errorMessage({ error: 'validation_error', message: 'an existing monitor checks this target from this location; link it instead' }), 'an existing monitor checks this target from this location; link it instead');
  assert.equal(isDuplicateTarget({ error: 'validation_error', message: 'an existing monitor checks this target from this location; link it instead' }), true);
  assert.equal(isNotFound({ error: 'not_found', message: 'discovery resource not found' }), true);
  assert.equal(isNotFound(new Error('boom')), false);
  assert.equal(errorMessage(new Error('boom')), 'boom');
  assert.equal(errorMessage(42), 'The request failed. Please retry.');
});
test('gap tones stay on the shared status-tone vocabulary', () => {
  const statusTones = ['success', 'danger', 'warning', 'info', 'neutral'];
  for (const [code, gap] of Object.entries(coverageGaps)) assert.ok(statusTones.includes(gap.tone), code);
  assert.equal(gapInfo('resource_missing').tone, 'danger');
  assert.deepEqual(gapInfo('something_new'), { short: 'something_new', label: 'something_new', tone: 'neutral' });
});
