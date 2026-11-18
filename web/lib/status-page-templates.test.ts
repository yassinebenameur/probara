import assert from 'node:assert/strict';
import test from 'node:test';
import { PAGE_TEMPLATE_OPTIONS, pageTemplate, pageTemplateLabel } from './status-page-templates';

test('lists the built-ins the API accepts, default first', () => {
  assert.deepEqual(
    PAGE_TEMPLATE_OPTIONS.map((o) => o.value),
    ['default', 'classic'],
  );
});

test('an unset or unknown template reads as the default', () => {
  assert.equal(pageTemplate(undefined), 'default');
  assert.equal(pageTemplate(''), 'default');
  assert.equal(pageTemplate('modern'), 'default');
  assert.equal(pageTemplate('classic'), 'classic');
  assert.equal(pageTemplateLabel('classic'), 'Classic');
  assert.equal(pageTemplateLabel(undefined), 'Default');
});
