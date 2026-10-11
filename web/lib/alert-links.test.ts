import assert from 'node:assert/strict';
import test from 'node:test';
import { focusTenantSwitch, parseAlertFocus, withoutAlertFocus } from './alert-links';

const A = '3f2a91cc-11de-4b7a-9a2e-6c5f8d0e1234';
const T1 = '00000000-0000-0000-0000-000000000001';
const T2 = '8f14e45f-ceea-467a-9f0b-2c1a3b4d5e6f';

test('parseAlertFocus reads the notification deep link', () => {
  assert.deepEqual(parseAlertFocus(new URLSearchParams(`alert=${A}&tenant=${T2}`)), { alertId: A, tenantId: T2 });
  assert.deepEqual(parseAlertFocus(new URLSearchParams(`alert=${A}`)), { alertId: A, tenantId: null });
  assert.deepEqual(parseAlertFocus(new URLSearchParams(`alert=${A}&tenant=nope`)), { alertId: A, tenantId: null });
  assert.equal(parseAlertFocus(new URLSearchParams('alert=1')), null);
  assert.equal(parseAlertFocus(new URLSearchParams('status=active')), null);
});

test('focusTenantSwitch only switches to a different, visible workspace', () => {
  const focus = { alertId: A, tenantId: T2 };
  assert.equal(focusTenantSwitch(focus, T1, [T1, T2]), T2);
  assert.equal(focusTenantSwitch(focus, T2, [T1, T2]), null);
  assert.equal(focusTenantSwitch(focus, T1, [T1]), null);
  assert.equal(focusTenantSwitch(focus, null, [T1, T2]), null);
  assert.equal(focusTenantSwitch({ alertId: A, tenantId: null }, T1, [T1, T2]), null);
});

test('withoutAlertFocus keeps the list filters', () => {
  const params = new URLSearchParams(`status=resolved&alert=${A}&tenant=${T2}`);
  assert.equal(withoutAlertFocus(params).toString(), 'status=resolved');
  assert.equal(params.get('alert'), A, 'input untouched');
});
