import assert from 'node:assert/strict';
import test from 'node:test';
import {
  resolveStatusPageDraftPreviewUrl,
  resolveStatusPageOrigin,
  resolveStatusPagePublicUrl,
} from './statusPageUrl';

// No window and no API key here: links carry ?edit=1 (cookie session), and
// without any configured origin they stay relative.

test('the runtime origin from the API builds absolute links', () => {
  assert.equal(
    resolveStatusPagePublicUrl({ slug: 'acme' }, 'https://status.example.com/'),
    'https://status.example.com/public/status/acme?edit=1'
  );
  assert.equal(
    resolveStatusPageDraftPreviewUrl({ slug: 'acme' }, 'tok en', 'http://status.example.com:8082'),
    'http://status.example.com:8082/public/status/acme/preview/draft?token=tok%20en'
  );
  // A relative public_url resolves against it too.
  assert.equal(
    resolveStatusPagePublicUrl({ slug: 'acme', public_url: '/public/status/acme' }, 'https://status.example.com'),
    'https://status.example.com/public/status/acme?edit=1'
  );
});

test('without a usable origin links stay relative', () => {
  assert.equal(resolveStatusPageOrigin(undefined), '');
  assert.equal(resolveStatusPageOrigin(null), '');
  assert.equal(resolveStatusPageOrigin('/not-absolute'), '');
  assert.equal(resolveStatusPageOrigin('javascript:alert(1)'), '');
  assert.equal(resolveStatusPagePublicUrl({ slug: 'acme' }, ''), '/public/status/acme?edit=1');
  assert.equal(resolveStatusPageDraftPreviewUrl({ slug: 'acme' }, undefined, null), '/public/status/acme/preview/draft');
});
