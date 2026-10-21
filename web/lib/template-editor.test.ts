import assert from 'node:assert/strict';
import test from 'node:test';
import { followLiveSource, templateEditorState } from './template-editor';
import { modalOpen, shortcutLabel } from './shortcuts';

test('a freshly loaded editor with no draft matches the live template', () => {
  assert.deepEqual(templateEditorState({ source: 'live', baseline: 'live', hasDraft: false }), {
    dirty: false,
    needsSave: true, // publishing promotes a draft, so one must be saved first
    status: 'live',
  });
});

test('a loaded draft is clean and publishable without another save', () => {
  assert.deepEqual(templateEditorState({ source: 'd', baseline: 'd', hasDraft: true }), {
    dirty: false,
    needsSave: false,
    status: 'draft',
  });
});

test('any edit is unsaved and needs a save, draft or not', () => {
  for (const hasDraft of [true, false]) {
    assert.deepEqual(templateEditorState({ source: 'd2', baseline: 'd', hasDraft }), {
      dirty: true,
      needsSave: true,
      status: 'unsaved',
    });
  }
});

test('reset/republish without edits reseeds the editor with the new live source', () => {
  assert.deepEqual(followLiveSource({ source: 'old', baseline: 'old' }, 'built-in'), {
    source: 'built-in',
    baseline: 'built-in',
  });
});

test('reset/republish keeps edits, which then read as unsaved against the new live source', () => {
  const next = followLiveSource({ source: 'edited', baseline: 'old' }, 'built-in');
  assert.deepEqual(next, { source: 'edited', baseline: 'built-in' });
  assert.equal(templateEditorState({ ...next, hasDraft: false }).status, 'unsaved');
  // Edits that happen to equal the new live source are simply clean.
  const same = followLiveSource({ source: 'built-in', baseline: 'old' }, 'built-in');
  assert.equal(templateEditorState({ ...same, hasDraft: false }).status, 'live');
});

test('shortcut labels follow the platform', () => {
  assert.equal(shortcutLabel('S', true), '⌘S');
  assert.equal(shortcutLabel('S', false), 'Ctrl+S');
  assert.equal(shortcutLabel('↵', false), 'Ctrl+↵');
});

test('modalOpen looks for an aria-modal element', () => {
  assert.equal(modalOpen(undefined), false);
  assert.equal(modalOpen({ querySelector: () => null }), false);
  assert.equal(
    modalOpen({ querySelector: (sel: string) => (sel === '[aria-modal="true"]' ? ({} as Element) : null) } as Pick<Document, 'querySelector'>),
    true,
  );
});
