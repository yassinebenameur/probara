import assert from 'node:assert/strict';
import test from 'node:test';
import {
  accessCleanupCommand, accessManifestCommands, ADDED_NAMESPACE_TEXT, collectorAccessManifest, configuredNamespaces, installCommands,
  integrationEditErrors, integrationPatch, namespaceDelta, namespacePill, namespaceRows, pendingReasonInfo, removeButtonIndex, removedNamespaceText, withHomeLocked,
  approveFormErrors, COLLECTOR_IMAGE_PREFILL, COLLECTOR_IMAGE_REPOSITORY, collectorHealth, collectorManifest, coverageGaps, coverageQueryString, gapInfo,
  initialCollectorImage, isDuplicateTarget, isNotFound, linkBlocked, manifestInputErrors, MAX_NAMESPACES, namespaceFieldError, pageAfterShrink, parseExpectedStatus,
  parseNamespaces, pendingSuggestionCount, serviceSuggestions, suggestionCover, suggestionTarget, validNamespace,
  type ApproveFormValues, type DiscoveryIntegration, type DiscoveryMonitor, type DiscoveryService,
} from './discovery';
import { errorMessage } from './errors';

const integration: DiscoveryIntegration = {
  id: 'c952c53e-d8c8-4e16-8e94-a9ec57690c11', name: 'Production',
  namespaces: ['payments', 'orders'], home_namespace: 'payments', pending_namespaces: [], pending_status: {},
  location_id: null, last_seen_at: null, revision: 0,
};
// Home is not first in the stored list, and one namespace waits for its first scan.
const edited: DiscoveryIntegration = {
  ...integration, namespaces: ['orders', 'payments'], home_namespace: 'payments',
  pending_namespaces: ['extra'], pending_status: { extra: { reason: 'forbidden', checked_at: '2026-11-02T10:00:00Z' } },
};
const parse = (yaml: string) => yaml.split('\n---\n').map((v) => JSON.parse(v));
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

test('namespace field keeps every entry and reports how many to remove', () => {
  const names = (n: number) => Array.from({ length: n }, (_, i) => `ns-${i}`);
  assert.equal(namespaceFieldError(names(MAX_NAMESPACES)), undefined);
  assert.equal(namespaceFieldError(names(MAX_NAMESPACES + 5)), 'At most 20 namespaces — remove 5');
  assert.equal(namespaceFieldError(['ok', 'Bad_NS']), 'Not a valid namespace: Bad_NS');
  assert.equal(namespaceFieldError([...names(MAX_NAMESPACES), 'bad_ns']), 'Not a valid namespace: bad_ns. At most 20 namespaces — remove 1');
  assert.equal(namespaceFieldError([]), undefined);
  // A draft still being typed counts toward the limit but is not name-checked.
  assert.equal(namespaceFieldError(['ok'], ['kube-']), undefined);
  assert.equal(namespaceFieldError(names(MAX_NAMESPACES), ['extra']), 'At most 20 namespaces — remove 1');
  assert.equal(namespaceFieldError(names(MAX_NAMESPACES), [names(1)[0]]), undefined, 'a draft repeating a chip is not counted twice');
});
test('expected status is parsed from text on submit', () => {
  assert.equal(parseExpectedStatus('200'), 200);
  assert.equal(parseExpectedStatus(' 301 '), 301);
  assert.equal(parseExpectedStatus('100'), 100);
  assert.equal(parseExpectedStatus('599'), 599);
  for (const bad of ['', '  ', '99', '600', '700', '20a', '2e2', '-200', '200.5']) assert.equal(parseExpectedStatus(bad), null, bad);
});
test('approve form reports each blocking field inline', () => {
  const ok: ApproveFormValues = { name: 'payments', status: '200', locationId: '', mode: 'default', channelIds: [] };
  const http = { type: 'http' as const, internal: false };
  assert.deepEqual(approveFormErrors(ok, http), {});
  assert.deepEqual(Object.keys(approveFormErrors({ ...ok, name: '  ', status: '' }, http)).sort(), ['name', 'status']);
  assert.ok(approveFormErrors({ ...ok, status: '700' }, http).status);
  assert.deepEqual(approveFormErrors({ ...ok, status: '' }, { type: 'tcp', internal: false }), {}, 'TCP has no expected status');
  assert.ok(approveFormErrors({ ...ok, mode: 'custom' }, http).channelIds);
  assert.deepEqual(approveFormErrors({ ...ok, mode: 'custom', channelIds: ['c1'] }, http), {});
  assert.ok(approveFormErrors(ok, { type: 'http', internal: true }).locationId);
  assert.deepEqual(approveFormErrors({ ...ok, locationId: 'l1' }, { type: 'http', internal: true }), {});
});
test('an empty page past the end moves to the last page with rows', () => {
  assert.equal(pageAfterShrink(3, 60, 0, 50), 2);
  assert.equal(pageAfterShrink(5, 10, 0, 50), 1);
  assert.equal(pageAfterShrink(3, 60, 0, undefined), 2);
  assert.equal(pageAfterShrink(1, 60, 0, 50), null, 'page 1 never moves');
  assert.equal(pageAfterShrink(3, 0, 0, 50), null, 'nothing matches: the empty state is right');
  assert.equal(pageAfterShrink(2, 60, 10, 50), null, 'rows came back');
  assert.equal(pageAfterShrink(2, 120, 0, 50), null, 'already in range');
});
test('a keyless link that checks the same target covers the suggestion', () => {
  const monitor = (id: string, suggestion_key: string | null): DiscoveryMonitor => ({
    id, name: id, type: 'http', enabled: true, state: 'up', fresh: true, routed: true, suggestion_key,
  });
  const s = (key: string) => ({ key, type: 'http' as const, target: `https://${key}.example.com`, internal: false, reason: '' });
  const service: DiscoveryService = {
    id: 's', name: 'svc', namespace: 'ns', gaps: [],
    resources: [{ uid: '1', kind: 'Ingress', name: 'a', missing_since: null, suggestions: [s('k1'), s('k2'), s('k3')] }],
    monitors: [monitor('keyless', null), monitor('keyed', 'k2'), monitor('other', null)],
    candidates: { k1: [monitor('keyless', null)], k3: [monitor('elsewhere', null)] },
  };
  assert.equal(suggestionCover(service, 'k1')?.id, 'keyless', 'linked without a key, among the candidates');
  assert.equal(suggestionCover(service, 'k2')?.id, 'keyed');
  assert.equal(suggestionCover(service, 'k3'), undefined, 'a candidate not linked to the service does not cover it');
  assert.equal(pendingSuggestionCount(service), 1);

  assert.equal(linkBlocked(service, 'keyless', 'k3'), false, 'a keyless link can take a suggestion');
  assert.equal(linkBlocked(service, 'keyless'), true, 'already linked to the service');
  assert.equal(linkBlocked(service, 'keyed', 'k1'), true, 'already linked for another suggestion');
  assert.equal(linkBlocked(service, 'unlinked', 'k1'), false);
});
test('the prefilled collector image needs its tag', () => {
  assert.ok(COLLECTOR_IMAGE_PREFILL.startsWith('ghcr.io/yassinebenameur/probara-discovery-collector'));
  assert.ok(manifestInputErrors('https://probara.example.com', COLLECTOR_IMAGE_PREFILL).image);
  assert.deepEqual(manifestInputErrors('https://probara.example.com', `${COLLECTOR_IMAGE_PREFILL}2.0.0`), {});
  assert.throws(() => collectorManifest(integration, 'https://probara.example.com', COLLECTOR_IMAGE_PREFILL, 'token'));
});
test('a remembered published image drops its old tag; a custom one is kept', () => {
  assert.equal(initialCollectorImage(''), COLLECTOR_IMAGE_PREFILL);
  assert.equal(initialCollectorImage(`${COLLECTOR_IMAGE_PREFILL}1.9.0`), COLLECTOR_IMAGE_PREFILL);
  assert.equal(initialCollectorImage(`${COLLECTOR_IMAGE_REPOSITORY}@sha256:abc`), COLLECTOR_IMAGE_PREFILL);
  assert.equal(initialCollectorImage(COLLECTOR_IMAGE_REPOSITORY), COLLECTOR_IMAGE_PREFILL);
  assert.equal(initialCollectorImage(' registry.example.com/probara-discovery:discovery-v1 '), 'registry.example.com/probara-discovery:discovery-v1');
  assert.equal(initialCollectorImage(`${COLLECTOR_IMAGE_REPOSITORY}-fork:1.0`), `${COLLECTOR_IMAGE_REPOSITORY}-fork:1.0`);
});

test('the access manifest holds only Roles and RoleBindings bound to the home ServiceAccount', () => {
  const yaml = collectorAccessManifest(edited, configuredNamespaces(edited));
  const docs = parse(yaml);
  assert.deepEqual(docs.map((d) => `${d.kind}/${d.metadata.namespace}`), [
    'Role/orders', 'RoleBinding/orders', 'Role/payments', 'RoleBinding/payments', 'Role/extra', 'RoleBinding/extra',
  ]);
  assert.equal(docs.some((d) => d.kind === 'Secret' || d.kind === 'Deployment' || d.kind === 'ServiceAccount'), false);
  assert.equal(/token/i.test(yaml), false, 'no credential in a re-appliable file');
  for (const binding of docs.filter((d) => d.kind === 'RoleBinding')) {
    assert.deepEqual(binding.subjects, [{ kind: 'ServiceAccount', name: 'probara-discovery-c952c53e', namespace: 'payments' }]);
    assert.equal(binding.roleRef.name, 'probara-discovery-c952c53e');
  }
  assert.deepEqual(accessManifestCommands(edited), {
    file: 'probara-discovery-c952c53e-access.yaml',
    apply: 'kubectl --context your-cluster apply -f probara-discovery-c952c53e-access.yaml',
  });
});
test('a full install runs in the home namespace and grants pending namespaces too', () => {
  const docs = parse(collectorManifest(edited, 'https://api.example.com', 'example/collector:v1', 'test-token'));
  for (const kind of ['ServiceAccount', 'Secret', 'Deployment']) assert.equal(docs.find((d) => d.kind === kind).metadata.namespace, 'payments', kind);
  assert.deepEqual(docs.filter((d) => d.kind === 'Role').map((d) => d.metadata.namespace), ['orders', 'payments', 'extra']);
  assert.equal(installCommands(edited).logs, 'kubectl --context your-cluster -n payments logs deployment/probara-discovery-c952c53e');
});
test('cleanup deletes the collector Role and RoleBinding per removed namespace, idempotently', () => {
  assert.equal(accessCleanupCommand(integration, ['orders']), 'kubectl --context your-cluster -n orders delete role,rolebinding probara-discovery-c952c53e --ignore-not-found');
  assert.equal(accessCleanupCommand(integration, ['a', 'b'], 'prod').split('\n').length, 2);
  assert.equal(accessCleanupCommand(integration, []), '');
});
test('the home namespace is locked first and never removed', () => {
  assert.deepEqual(withHomeLocked('payments', ['orders', 'payments', 'extra']), ['payments', 'orders', 'extra']);
  assert.deepEqual(withHomeLocked('payments', ['orders']), ['payments', 'orders']);
  assert.deepEqual(namespaceDelta(edited, ['orders']), { add: [], remove: ['extra'] }, 'dropping home from the list keeps it');
});
test('the namespace delta covers active and pending namespaces', () => {
  assert.deepEqual(namespaceDelta(edited, ['payments', 'orders', 'extra']), { add: [], remove: [] });
  assert.deepEqual(namespaceDelta(edited, ['payments', 'extra', 'new-one']), { add: ['new-one'], remove: ['orders'] });
  assert.deepEqual(configuredNamespaces({ namespaces: ['a', 'b'], pending_namespaces: ['b', 'c'] }), ['a', 'b', 'c']);
});
test('the edit PATCH sends only what changed', () => {
  const same = { name: 'Production', locationId: '', namespaces: ['payments', 'orders', 'extra'] };
  assert.equal(integrationPatch(edited, same), null);
  assert.deepEqual(integrationPatch(edited, { ...same, name: '  Prod EU ' }), { name: 'Prod EU' });
  assert.deepEqual(integrationPatch(edited, { ...same, locationId: 'loc-1' }), { location_id: 'loc-1' });
  assert.deepEqual(integrationPatch({ ...edited, location_id: 'loc-1' }, same), { location_id: null }, 'None clears the location');
  assert.deepEqual(integrationPatch(edited, { ...same, namespaces: ['payments', 'shop'] }), { add_namespaces: ['shop'], remove_namespaces: ['orders', 'extra'] });
});
test('the edit form reports a blank name and bad namespaces', () => {
  assert.deepEqual(integrationEditErrors({ name: 'x', locationId: '', namespaces: ['payments'] }), {});
  assert.ok(integrationEditErrors({ name: '  ', locationId: '', namespaces: ['payments'] }).name);
  assert.equal(integrationEditErrors({ name: 'x', locationId: '', namespaces: ['payments', 'Bad_NS'] }).namespaces, 'Not a valid namespace: Bad_NS');
  assert.equal(integrationEditErrors({ name: 'x', locationId: '', namespaces: ['payments'] }, ['bad_draft']).namespaces, 'Not a valid namespace: bad_draft', 'submit refuses an invalid draft');
});
test('the review explains what an add and a remove do', () => {
  assert.equal(ADDED_NAMESPACE_TEXT, 'Scanned once the collector can read it. Apply the access manifest. Nothing else changes until then.');
  // linked_services counts linked services, not links (a service can have several).
  assert.equal(removedNamespaceText('shop', { services: 3, linked_services: 2 }),
    '3 services leave coverage now. They and the monitor links of 2 of them are deleted after 7 days unless you add shop back. Monitors and history are not touched.');
  assert.equal(removedNamespaceText('shop', { services: 3, linked_services: 3 }),
    '3 services leave coverage now. They and their monitor links are deleted after 7 days unless you add shop back. Monitors and history are not touched.');
  assert.equal(removedNamespaceText('shop', { services: 1, linked_services: 1 }),
    '1 service leaves coverage now. It and its monitor links are deleted after 7 days unless you add shop back. Monitors and history are not touched.');
  assert.equal(removedNamespaceText('shop', { services: 2, linked_services: 0 }),
    '2 services leave coverage now. They are deleted after 7 days unless you add shop back. Monitors and history are not touched.');
  assert.equal(removedNamespaceText('shop', { services: 0, linked_services: 0 }), 'No discovered services leave coverage. Monitors and history are not touched.');
  assert.match(removedNamespaceText('shop'), /unless you add shop back/);
});
test('namespace rows fall back to the integration in the API order', () => {
  assert.deepEqual(namespaceRows(edited).map((r) => [r.namespace, r.state, r.home, r.reason]), [
    ['payments', 'active', true, undefined], ['orders', 'active', false, undefined], ['extra', 'pending', false, 'forbidden'],
  ]);
  const items = [{ namespace: 'payments', state: 'active' as const, home: true, services: 2, linked_services: 1 }];
  assert.equal(namespaceRows(edited, items), items);
});
test('namespace pills show the count, the wait or why a scan skipped it', () => {
  assert.deepEqual(namespacePill({ state: 'active', services: 4 }, true), { tone: 'neutral', count: 4 });
  assert.deepEqual(namespacePill({ state: 'active', services: 0 }, true), { tone: 'warning', detail: 'no services' });
  assert.deepEqual(namespacePill({ state: 'active', services: 0 }, false), { tone: 'neutral', count: 0 }, 'before the first scan, empty is expected');
  assert.deepEqual(namespacePill({ state: 'active' }, true), { tone: 'neutral' });
  assert.deepEqual(namespacePill({ state: 'pending' }, true), { tone: 'neutral', detail: 'waiting for first scan' });
  assert.deepEqual(namespacePill({ state: 'pending', reason: 'forbidden' }, true), { tone: 'warning', detail: 'can’t read' });
  assert.equal(pendingReasonInfo('forbidden').label, 'Can’t read: the namespace doesn’t exist or the access manifest isn’t applied');
  assert.deepEqual(pendingReasonInfo('new_reason'), { short: 'new_reason', label: 'new_reason', tone: 'neutral' });
});
test('chip removal refocuses by position among the remove buttons, skipping the locked home chip', () => {
  const value = ['home', 'a', 'b', 'c'];
  const removable = (ns: string) => ns !== 'home';
  // Removing a leaves the buttons [b, c]; b took a's place, at button 0.
  assert.equal(removeButtonIndex(value, 1, removable), 0);
  assert.equal(removeButtonIndex(value, 3, removable), 2);
  assert.equal(removeButtonIndex(['a', 'b'], 1, () => true), 1);
});
