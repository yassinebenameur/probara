import assert from 'node:assert/strict';
import test from 'node:test';
import { parseSlaLink, slaIssuedReportsHref } from './sla-links';
import { focusTenantSwitch } from './alert-links';

const SLA = '0e9d8c7b-6a5f-4e3d-8c2b-1a0f9e8d7c6b';
const T1 = '00000000-0000-0000-0000-000000000001';
const T2 = '8f14e45f-ceea-467a-9f0b-2c1a3b4d5e6f';

test('an emailed report link names the SLA and its workspace', () => {
  assert.deepEqual(parseSlaLink(new URLSearchParams(`sla=${SLA}&tenant=${T2}`)), { slaId: SLA, tenantId: T2 });
  assert.deepEqual(parseSlaLink(new URLSearchParams(`sla=${SLA}&tenant=nope`)), { slaId: SLA, tenantId: null });
  assert.equal(parseSlaLink(new URLSearchParams('sla=nope')), null);
  assert.equal(parseSlaLink(new URLSearchParams('')), null);
  assert.equal(slaIssuedReportsHref(SLA), `/slas/${SLA}?tab=issued`);
});

test('it switches workspace only to one the viewer can see', () => {
  const link = { tenantId: T2 };
  assert.equal(focusTenantSwitch(link, T1, [T1, T2]), T2);
  assert.equal(focusTenantSwitch(link, T1, [T1]), null);
  assert.equal(focusTenantSwitch(link, T2, [T1, T2]), null, 'already there');
  assert.equal(focusTenantSwitch({ tenantId: null }, T1, [T1, T2]), null);
});
