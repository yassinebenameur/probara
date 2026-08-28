// Probara status page service worker.
//
// Served from /public/status/sw.js rather than from under a page slug: a
// worker's default max scope is its own directory, so at
// /public/status/{slug}/sw.js the scope would be /public/status/{slug}/ --
// which is not a prefix of the page URL /public/status/{slug}. Registration
// would succeed and control nothing. From the parent directory the max scope
// covers every page, and the client narrows registration to its own slug so
// one page's registration cannot clobber another's.

self.addEventListener('push', function (event) {
  var payload = {};
  try {
    payload = event.data ? event.data.json() : {};
  } catch (e) {
    // Fall through to the generic notification below.
  }

  // The subscription was created with userVisibleOnly: true, so a push MUST
  // result in a visible notification -- even a malformed one. Staying silent
  // makes the browser show its own "this site was updated in the background"
  // notice and, after repeats, revoke the subscription outright.
  var title = payload.title || 'Status update';
  var options = {
    body: payload.body || '',
    // One slot per monitor, so a component going down and recovering replaces
    // rather than stacks, and a flapping monitor cannot bury the screen.
    tag: payload.tag || 'probara-status',
    renotify: true,
    timestamp: payload.ts || Date.now(),
    data: { url: payload.url || self.registration.scope }
  };

  event.waitUntil(self.registration.showNotification(title, options));
});

self.addEventListener('notificationclick', function (event) {
  event.notification.close();

  var target = (event.notification.data && event.notification.data.url) || self.registration.scope;

  event.waitUntil(
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then(function (windows) {
      // Prefer focusing a tab already on this status page over opening a
      // duplicate.
      for (var i = 0; i < windows.length; i++) {
        if (windows[i].url.indexOf(target) === 0 && 'focus' in windows[i]) {
          return windows[i].focus();
        }
      }
      if (self.clients.openWindow) {
        return self.clients.openWindow(target);
      }
      return undefined;
    })
  );
});

// Browsers may rotate a subscription without any user action. Without this
// the old endpoint keeps receiving pushes the browser will drop, and the
// visitor silently stops being notified.
self.addEventListener('pushsubscriptionchange', function (event) {
  var scope = self.registration.scope;
  var slug = scope.replace(/\/+$/, '').split('/').pop();
  var base = scope.replace(/\/+$/, '').replace(/\/[^/]*$/, '');

  event.waitUntil(
    self.registration.pushManager
      .subscribe(event.oldSubscription ? event.oldSubscription.options : { userVisibleOnly: true })
      .then(function (sub) {
        return fetch(base + '/' + slug + '/push/subscribe', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(sub.toJSON())
        });
      })
      .catch(function () {
        // Nothing useful to do here; the page re-subscribes on next visit.
      })
  );
});
