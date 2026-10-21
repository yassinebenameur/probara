import assert from 'node:assert/strict';
import test from 'node:test';
import {
  isApplePlatform,
  matchSidebarShortcut,
  sidebarShortcutAria,
  sidebarShortcutLabel,
  type ShortcutKeyEvent,
} from './sidebar-shortcut';

const body = { isContentEditable: false, closest: () => null };

function key(overrides: Partial<ShortcutKeyEvent> = {}): ShortcutKeyEvent {
  return {
    key: 'b',
    code: 'KeyB',
    metaKey: false,
    ctrlKey: false,
    altKey: false,
    shiftKey: false,
    repeat: false,
    defaultPrevented: false,
    isComposing: false,
    target: body,
    ...overrides,
  };
}

const noPopup = () => false;

/** An element whose closest() matches when the selector list names `match`. */
function inside(match: string) {
  return {
    isContentEditable: false,
    closest: (selector: string) => (selector.split(',').some((part) => part.trim() === match) ? {} : null),
  };
}

test('Meta+B toggles on Apple platforms, Ctrl+B elsewhere', () => {
  assert.equal(matchSidebarShortcut(key({ metaKey: true }), true, noPopup), 'toggle');
  assert.equal(matchSidebarShortcut(key({ ctrlKey: true }), false, noPopup), 'toggle');
  assert.equal(matchSidebarShortcut(key({ key: 'B', ctrlKey: true }), false, noPopup), 'toggle');
});

test('the other platform\'s chord and a bare B do nothing', () => {
  assert.equal(matchSidebarShortcut(key({ ctrlKey: true }), true, noPopup), null);
  assert.equal(matchSidebarShortcut(key({ metaKey: true }), false, noPopup), null);
  assert.equal(matchSidebarShortcut(key({ metaKey: true, ctrlKey: true }), true, noPopup), null);
  assert.equal(matchSidebarShortcut(key(), false, noPopup), null);
  assert.equal(matchSidebarShortcut(key({ key: '[', code: 'BracketLeft' }), false, noPopup), null);
});

test('Alt (incl. AltGr as Ctrl+Alt) and Shift variants are not the chord', () => {
  assert.equal(matchSidebarShortcut(key({ ctrlKey: true, altKey: true }), false, noPopup), null);
  assert.equal(matchSidebarShortcut(key({ metaKey: true, altKey: true }), true, noPopup), null);
  assert.equal(matchSidebarShortcut(key({ key: 'B', ctrlKey: true, shiftKey: true }), false, noPopup), null);
});

test('auto-repeat is reported as repeat, not a toggle', () => {
  assert.equal(matchSidebarShortcut(key({ ctrlKey: true, repeat: true }), false, noPopup), 'repeat');
});

test('non-Latin layouts fall back to the physical B key; Latin layouts use the letter', () => {
  assert.equal(matchSidebarShortcut(key({ key: 'и', code: 'KeyB', ctrlKey: true }), false, noPopup), 'toggle');
  assert.equal(matchSidebarShortcut(key({ key: 'β', code: 'KeyB', metaKey: true }), true, noPopup), 'toggle');
  // Dvorak: the physical B key types x — that is not B.
  assert.equal(matchSidebarShortcut(key({ key: 'x', code: 'KeyB', ctrlKey: true }), false, noPopup), null);
  // …and the key that types b on Dvorak (physical N) is.
  assert.equal(matchSidebarShortcut(key({ key: 'b', code: 'KeyN', ctrlKey: true }), false, noPopup), 'toggle');
});

test('composition and already-handled presses are left alone', () => {
  assert.equal(matchSidebarShortcut(key({ ctrlKey: true, isComposing: true }), false, noPopup), null);
  assert.equal(matchSidebarShortcut(key({ ctrlKey: true, defaultPrevented: true }), false, noPopup), null);
});

test('fields, rich text, menus, listboxes, comboboxes and dialogs keep the chord', () => {
  for (const match of ['input', 'textarea', 'select', '[role="menu"]', '[role="listbox"]', '[role="dialog"]', '[role="combobox"]']) {
    assert.equal(matchSidebarShortcut(key({ ctrlKey: true, target: inside(match) }), false, noPopup), null, match);
  }
  assert.equal(
    matchSidebarShortcut(key({ ctrlKey: true, target: { isContentEditable: true, closest: () => null } }), false, noPopup),
    null
  );
  // A target without closest (the window, a text node) is not a field.
  assert.equal(matchSidebarShortcut(key({ ctrlKey: true, target: {} }), false, noPopup), 'toggle');
  assert.equal(matchSidebarShortcut(key({ ctrlKey: true, target: null }), false, noPopup), 'toggle');
});

test('an open modal or menu suspends the shortcut, and is only checked for the chord', () => {
  assert.equal(matchSidebarShortcut(key({ ctrlKey: true }), false, () => true), null);
  let checked = false;
  matchSidebarShortcut(key({ key: 'a', ctrlKey: true }), false, () => {
    checked = true;
    return false;
  });
  assert.equal(checked, false);
});

test('platform detection prefers userAgentData, then platform, then the UA', () => {
  assert.equal(isApplePlatform({ userAgentData: { platform: 'macOS' }, platform: 'Win32' }), true);
  assert.equal(isApplePlatform({ userAgentData: { platform: 'Windows' }, platform: 'MacIntel' }), false);
  assert.equal(isApplePlatform({ platform: 'MacIntel' }), true);
  assert.equal(isApplePlatform({ platform: 'Linux x86_64' }), false);
  assert.equal(isApplePlatform({ userAgent: 'Mozilla/5.0 (iPad; CPU OS 17_0 like Mac OS X)' }), true);
  assert.equal(isApplePlatform(null), false);
});

test('key cap and aria-keyshortcuts name the same chord', () => {
  assert.equal(sidebarShortcutLabel(true), '⌘B');
  assert.equal(sidebarShortcutAria(true), 'Meta+B');
  assert.equal(sidebarShortcutLabel(false), 'Ctrl+B');
  assert.equal(sidebarShortcutAria(false), 'Control+B');
});
