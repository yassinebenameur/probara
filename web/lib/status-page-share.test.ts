import assert from 'node:assert/strict';
import test from 'node:test';
import {
  badgeHtml,
  badgeMarkdown,
  monitorBadgeUrl,
  statusPageBase,
  statusPageShareLinks,
} from './status-page-share';

const PAGE = 'https://status.example.com/public/status/acme';
const MONITOR = '0b6f0a7e-1111-4c2d-8e9f-000000000001';

test('share links hang off the page URL without its query', () => {
  assert.equal(statusPageBase(`${PAGE}/?edit=1#top`), PAGE);
  assert.equal(statusPageBase('/public/status/acme?edit=1'), '/public/status/acme');
  assert.deepEqual(statusPageShareLinks(`${PAGE}?edit=1`), {
    page: PAGE,
    feed: `${PAGE}/feed.atom`,
    summary: `${PAGE}/summary.json`,
    badge: `${PAGE}/badge.svg`,
  });
});

test('monitor badges match the status-page routes', () => {
  assert.equal(monitorBadgeUrl(PAGE, MONITOR, 'status'), `${PAGE}/monitors/${MONITOR}/badge.svg`);
  assert.equal(monitorBadgeUrl(PAGE, MONITOR, 'uptime'), `${PAGE}/monitors/${MONITOR}/uptime.svg?range=30d`);
  assert.equal(monitorBadgeUrl(PAGE, MONITOR, 'uptime', '7d'), `${PAGE}/monitors/${MONITOR}/uptime.svg?range=7d`);
});

test('snippets escape names and URLs', () => {
  const img = `${PAGE}/badge.svg`;
  assert.equal(badgeMarkdown(img, PAGE, 'Acme status'), `[![Acme status](${img})](${PAGE})`);
  assert.equal(
    badgeMarkdown('https://x.test/a(b).svg', PAGE, 'API [eu] \\ status'),
    `[![API \\[eu\\] \\\\ status](https://x.test/a%28b%29.svg)](${PAGE})`
  );
  assert.equal(
    badgeHtml(img, PAGE, 'R&D "core" <api>'),
    `<a href="${PAGE}"><img src="${img}" alt="R&amp;D &quot;core&quot; &lt;api&gt;"></a>`
  );
});
