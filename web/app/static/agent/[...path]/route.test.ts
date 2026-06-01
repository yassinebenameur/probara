import assert from 'node:assert/strict';
import test from 'node:test';
import { GET, HEAD } from './route';

const originalFetch = globalThis.fetch;

test.afterEach(() => {
  globalThis.fetch = originalFetch;
});

test('GET proxies agent binaries to the API static route', async () => {
  const upstreamBody = new ReadableStream({
    start(controller) {
      controller.enqueue(new TextEncoder().encode('agent-binary'));
      controller.close();
    },
  });
  const requests: Array<{ url: string; init?: RequestInit }> = [];

  globalThis.fetch = async (input, init) => {
    requests.push({ url: String(input), init });
    return new Response(upstreamBody, {
      status: 200,
      headers: {
        'content-type': 'application/octet-stream',
      },
    });
  };

  const response = await GET(
    new Request('https://example.test/static/agent/probara-agent-linux-amd64?checksum=1'),
    { params: Promise.resolve({ path: ['probara-agent-linux-amd64'] }) },
  );

  assert.equal(requests[0].url, 'http://localhost:8080/static/agent/probara-agent-linux-amd64?checksum=1');
  assert.equal(requests[0].init?.method, 'GET');
  assert.equal(requests[0].init?.cache, 'no-store');
  assert.equal(response.status, 200);
  assert.equal(response.headers.get('content-type'), 'application/octet-stream');
  assert.equal(await response.text(), 'agent-binary');
});

test('HEAD checks agent binary availability without a response body', async () => {
  const requests: Array<{ url: string; init?: RequestInit }> = [];

  globalThis.fetch = async (input, init) => {
    requests.push({ url: String(input), init });
    return new Response(null, { status: 200 });
  };

  const response = await HEAD(
    new Request('https://example.test/static/agent/probara-agent-darwin-arm64', { method: 'HEAD' }),
    { params: Promise.resolve({ path: ['probara-agent-darwin-arm64'] }) },
  );

  assert.equal(requests[0].url, 'http://localhost:8080/static/agent/probara-agent-darwin-arm64');
  assert.equal(requests[0].init?.method, 'HEAD');
  assert.equal(response.status, 200);
  assert.equal(response.body, null);
});
