import assert from 'node:assert/strict';
import test from 'node:test';
import { fetchResultsBatches, type FetchResultsChunk, resultsBatchError } from './monitor-results';
import type { CheckResult } from './types';

function result(id: string): CheckResult {
  return { id, status: 'success', result_source: 'monitor', created_at: '2026-10-21T00:00:00.000Z' };
}

const ids = (n: number) => Array.from({ length: n }, (_, i) => `m-${i}`);

test('fetchResultsBatches chunks at the cap and asks for the limit', async () => {
  const calls: { ids: string[]; limit: number }[] = [];
  const fetchChunk: FetchResultsChunk = async (chunk, { limit }) => {
    calls.push({ ids: chunk, limit });
    return { limit, results: Object.fromEntries(chunk.map((id) => [id, [result(`r-${id}`)]])), members: {} };
  };

  const batch = await fetchResultsBatches([...ids(150), 'm-0'], 20, fetchChunk, 100);

  assert.deepEqual(
    calls.map((call) => call.ids.length),
    [100, 50]
  );
  assert.ok(calls.every((call) => call.limit === 20));
  assert.equal(Object.keys(batch.results).length, 150);
  assert.equal(batch.failed.size, 0);
  assert.equal(resultsBatchError(batch, 150), null);
});

test('fetchResultsBatches gives an id the server left out an empty list', async () => {
  const fetchChunk: FetchResultsChunk = async (_chunk, { limit }) => ({
    limit,
    results: { live: [result('r1')] },
    members: {},
  });

  const batch = await fetchResultsBatches(['live', 'deleted'], 20, fetchChunk);

  assert.deepEqual(batch.results.deleted, []);
  assert.equal(batch.results.live.length, 1);
});

test('fetchResultsBatches marks a failed chunk and keeps the others', async () => {
  const fetchChunk: FetchResultsChunk = async (chunk, { limit }) => {
    if (chunk.includes('m-0')) throw { error: 'internal_error', message: 'boom' };
    return { limit, results: Object.fromEntries(chunk.map((id) => [id, []])), members: { 'm-3': ['m-2'] } };
  };

  const batch = await fetchResultsBatches(ids(4), 20, fetchChunk, 2);

  assert.deepEqual(Array.from(batch.failed).sort(), ['m-0', 'm-1']);
  assert.deepEqual(Object.keys(batch.results).sort(), ['m-2', 'm-3']);
  assert.deepEqual(batch.members, { 'm-3': ['m-2'] });
  assert.equal(resultsBatchError(batch, 4)?.message, 'Recent checks failed to load for 2 of 4 monitors: boom');
});

test('fetchResultsBatches reports a failure of every chunk', async () => {
  const fetchChunk: FetchResultsChunk = async () => {
    throw new Error('HTTP error! status: 404');
  };

  const batch = await fetchResultsBatches(ids(3), 20, fetchChunk, 2);

  assert.equal(batch.failed.size, 3);
  assert.deepEqual(batch.results, {});
  assert.equal(resultsBatchError(batch, 3)?.message, 'Recent checks failed to load: HTTP error! status: 404');
});

test('fetchResultsBatches merges group members across chunks', async () => {
  const fetchChunk: FetchResultsChunk = async (chunk, { limit }) => ({
    limit,
    results: Object.fromEntries(chunk.map((id) => [id, []])),
    members: Object.fromEntries(chunk.filter((id) => id.startsWith('g')).map((id) => [id, [`${id}-member`]])),
  });

  const batch = await fetchResultsBatches(['g1', 'm1', 'g2'], 20, fetchChunk, 2);

  assert.deepEqual(batch.members, { g1: ['g1-member'], g2: ['g2-member'] });
});
