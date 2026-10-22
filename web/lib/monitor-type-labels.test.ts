import assert from 'node:assert/strict';
import test from 'node:test';
import { monitorTypeDisplay } from './monitor-type-labels';

test('monitorTypeDisplay returns CALL for a SIP test call', () => {
  assert.deepEqual(monitorTypeDisplay({ type: 'sip', config: { method: 'invite', call: { number: '1000' } } }), {
    label: 'SIP call',
    badge: 'CALL',
  });
  assert.deepEqual(monitorTypeDisplay({ type: 'sip', config: { method: 'options' } }), { label: 'SIP', badge: 'SIP' });
  assert.deepEqual(monitorTypeDisplay({ type: 'sip' }), { label: 'SIP', badge: 'SIP' });
  assert.deepEqual(monitorTypeDisplay({ type: 'postgres', config: { method: 'invite' } }), {
    label: 'PostgreSQL',
    badge: 'PG',
  });
});
