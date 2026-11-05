/*
 * Probara status widget.
 *
 *   <script src="https://status.example.com/public/status/acme/embed.js" async></script>
 *
 * Renders the page's current status where the script tag sits, inside a
 * shadow root so the host page's CSS cannot reach it and its CSS cannot leak
 * out. It reads summary.json next to its own URL, so it follows the page
 * wherever the script is served from. Options are data- attributes on the
 * script tag:
 *
 *   data-target="#id"        render into that element instead
 *   data-components="true"   list every component with its status
 *   data-theme="light|dark"  force a theme (default: the visitor's)
 *   data-refresh="60"        seconds between refreshes, 30 minimum, 0 = off
 *
 * Everything from summary.json is written with textContent, never HTML.
 */
(function () {
  'use strict';

  var script = document.currentScript;
  if (!script || !script.src) return;

  var base = script.src.split('#')[0].split('?')[0].replace(/\/embed\.js$/, '');
  var summaryURL = base + '/summary.json';
  var opts = script.dataset || {};

  var host = document.createElement('div');
  host.className = 'probara-status-widget';
  if (opts.theme === 'light' || opts.theme === 'dark') host.setAttribute('data-theme', opts.theme);
  var target = opts.target ? document.querySelector(opts.target) : null;
  if (target) {
    target.appendChild(host);
  } else if (script.parentNode) {
    script.parentNode.insertBefore(host, script.nextSibling);
  } else {
    return;
  }

  var root = host.attachShadow ? host.attachShadow({ mode: 'open' }) : host;

  var css = [
    ':host{all:initial;display:block;--bg:#fff;--fg:#18181b;--muted:#71717a;--line:rgba(0,0,0,.1);',
    '--ok:#16a34a;--warn:#d97706;--down:#dc2626;--maint:#2563eb;--unknown:#9ca3af;}',
    '@media (prefers-color-scheme:dark){:host(:not([data-theme=light])){--bg:#111113;--fg:#f4f4f5;--muted:#a1a1aa;--line:rgba(255,255,255,.12);}}',
    ':host([data-theme=dark]){--bg:#111113;--fg:#f4f4f5;--muted:#a1a1aa;--line:rgba(255,255,255,.12);}',
    '.w{font:13px/1.4 system-ui,-apple-system,"Segoe UI",sans-serif;color:var(--fg);background:var(--bg);',
    'border:1px solid var(--line);border-radius:10px;padding:10px 12px;max-width:420px;box-sizing:border-box;}',
    '.head{display:flex;align-items:center;gap:8px;color:inherit;text-decoration:none;}',
    '.head:hover .label{text-decoration:underline;}',
    '.dot{flex:none;width:9px;height:9px;border-radius:50%;background:var(--unknown);}',
    '.t-ok{background:var(--ok)}.t-warn{background:var(--warn)}.t-down{background:var(--down)}.t-maint{background:var(--maint)}',
    '.label{font-weight:600;}.title{color:var(--muted);margin-left:auto;font-size:12px;}',
    'ul{list-style:none;margin:8px 0 0;padding:8px 0 0;border-top:1px solid var(--line);}',
    'li{display:flex;align-items:center;gap:8px;padding:2px 0;}',
    'li .st{margin-left:auto;color:var(--muted);font-size:12px;}',
    '.inc{margin-top:8px;padding-top:8px;border-top:1px solid var(--line);}',
    '.inc b{display:block;font-weight:600;}.inc span{color:var(--muted);font-size:12px;}'
  ].join('');

  if (root !== host && 'adoptedStyleSheets' in root && typeof CSSStyleSheet === 'function') {
    try {
      var sheet = new CSSStyleSheet();
      sheet.replaceSync(css);
      root.adoptedStyleSheets = [sheet];
    } catch (err) {
      appendStyle();
    }
  } else {
    appendStyle();
  }

  function appendStyle() {
    var style = document.createElement('style');
    style.textContent = css;
    root.appendChild(style);
  }

  var container = document.createElement('div');
  container.className = 'w';
  root.appendChild(container);

  var TONE = { operational: 't-ok', degraded: 't-warn', down: 't-down', maintenance: 't-maint', issues: 't-warn' };

  function el(tag, cls, text) {
    var node = document.createElement(tag);
    if (cls) node.className = cls;
    if (text != null) node.textContent = String(text);
    return node;
  }

  function safeLink(url) {
    return /^https?:\/\//i.test(url || '') ? url : base;
  }

  function overallTone(summary) {
    var c = (summary.status && summary.status.counts) || {};
    if (c.down > 0) return 't-down';
    if (c.degraded > 0) return 't-warn';
    if (c.unknown > 0) return '';
    if (c.maintenance > 0) return 't-maint';
    return 't-ok';
  }

  function render(summary) {
    container.textContent = '';
    var page = summary.page || {};
    var head = el('a', 'head');
    head.href = safeLink(page.url);
    head.target = '_blank';
    head.rel = 'noopener';
    head.appendChild(el('span', 'dot ' + overallTone(summary)));
    head.appendChild(el('span', 'label', (summary.status && summary.status.description) || 'Status'));
    head.appendChild(el('span', 'title', page.title || ''));
    container.appendChild(head);

    if (opts.components === 'true' && Array.isArray(summary.components) && summary.components.length) {
      var list = el('ul');
      summary.components.forEach(function (c) {
        var item = el('li');
        item.appendChild(el('span', 'dot ' + (TONE[c.status] || '')));
        item.appendChild(el('span', '', c.name));
        item.appendChild(el('span', 'st', c.status));
        list.appendChild(item);
      });
      container.appendChild(list);
    }

    (summary.incidents || []).slice(0, 3).forEach(function (incident) {
      var box = el('div', 'inc');
      box.appendChild(el('b', '', incident.title));
      box.appendChild(el('span', '', incident.state + (incident.latest_update ? ' · ' + incident.latest_update.message : '')));
      container.appendChild(box);
    });
  }

  function renderError() {
    container.textContent = '';
    var head = el('a', 'head');
    head.href = base;
    head.target = '_blank';
    head.rel = 'noopener';
    head.appendChild(el('span', 'dot'));
    head.appendChild(el('span', 'label', 'Status unavailable'));
    container.appendChild(head);
  }

  function load() {
    if (typeof fetch !== 'function') return;
    fetch(summaryURL, { credentials: 'omit', headers: { Accept: 'application/json' } })
      .then(function (res) { return res.ok ? res.json() : Promise.reject(res.status); })
      .then(render)
      .catch(renderError);
  }

  load();
  var refresh = opts.refresh == null || opts.refresh === '' ? 60 : parseInt(opts.refresh, 10);
  if (refresh > 0) {
    window.setInterval(load, Math.max(30, refresh) * 1000);
  }
})();
