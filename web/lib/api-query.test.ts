import assert from 'node:assert/strict';
import test from 'node:test';
import { withQuery } from './api/query';

test('withQuery returns the bare path when nothing is set', () => {
  assert.equal(withQuery('/v1/monitors'), '/v1/monitors');
  assert.equal(withQuery('/v1/monitors', {}), '/v1/monitors');
  assert.equal(withQuery('/v1/monitors', { tag: undefined, q: '', since: null }), '/v1/monitors');
});

test('withQuery sends false and 0 but not unset filters', () => {
  assert.equal(
    withQuery('/v1/monitors', { tag: undefined, enabled: false, page: 0, page_size: 50 }),
    '/v1/monitors?enabled=false&page=0&page_size=50'
  );
});

test('withQuery repeats a key for each array element and drops empty arrays', () => {
  assert.equal(
    withQuery('/v1/dashboard/summary', { range: '24h', tag: ['prod', 'eu west'] }),
    '/v1/dashboard/summary?range=24h&tag=prod&tag=eu+west'
  );
  assert.equal(withQuery('/v1/dashboard/summary', { tag: [] }), '/v1/dashboard/summary');
});

test('withQuery encodes values and keeps insertion order', () => {
  assert.equal(
    withQuery('/v1/monitors/abc/artifacts/screenshot', { tenant_id: 't 1', path: 'runs/1/shot.png' }),
    '/v1/monitors/abc/artifacts/screenshot?tenant_id=t+1&path=runs%2F1%2Fshot.png'
  );
});
