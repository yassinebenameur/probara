import assert from 'node:assert/strict';
import test from 'node:test';
import { createEventStreamParser } from './event-stream';

function parse(chunks: string[]) {
  const events: Array<[string, string]> = [];
  const feed = createEventStreamParser((event, data) => events.push([event, data]));
  chunks.forEach(feed);
  return events;
}

for (const newline of ['\n', '\r\n', '\r']) {
  test(`preserves events at every possible chunk boundary with ${JSON.stringify(newline)} lines`, () => {
    const stream = [
      ': keepalive',
      'event: alert_created',
      'data: {"id":"first"}',
      '',
      'event: alert_resolved',
      'data: {"id":"second"}',
      '',
      '',
    ].join(newline);
    const expected = [
      ['alert_created', '{"id":"first"}'],
      ['alert_resolved', '{"id":"second"}'],
    ];
    for (let split = 0; split <= stream.length; split++) {
      assert.deepEqual(parse([stream.slice(0, split), stream.slice(split)]), expected, `split ${split}`);
    }
    assert.deepEqual(parse([...stream]), expected);
  });
}

test('joins data lines while preserving significant whitespace and empty data', () => {
  assert.deepEqual(parse(['event: alert_created\ndata: {\ndata:  "id": "a"\ndata: }\n\ndata:\n\n']), [
    ['alert_created', '{\n "id": "a"\n}'],
    ['message', ''],
  ]);
});

test('ignores comments and unknown fields, resetting event type on each blank line', () => {
  assert.deepEqual(parse(['event: ignored\n\n: ping\nid: 42\nretry: 1000\ndata: next\n\n']), [
    ['message', 'next'],
  ]);
});

test('does not dispatch unfinished events or carry them across connections', () => {
  assert.deepEqual(parse(['event: alert_created\ndata: {"id":"unfinished"}\n']), []);
  assert.deepEqual(parse(['data: fresh\n\n']), [['message', 'fresh']]);
});

test('accepts UTF-8 characters split across network byte chunks', () => {
  const stream = new TextEncoder().encode('event: alert_created\ndata: {"name":"Café ☕"}\n\n');
  const decoder = new TextDecoder();
  assert.deepEqual(parse(Array.from(stream, byte => decoder.decode(Uint8Array.of(byte), { stream: true }))), [
    ['alert_created', '{"name":"Café ☕"}'],
  ]);
});
