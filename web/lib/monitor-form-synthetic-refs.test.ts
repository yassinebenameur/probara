import assert from 'node:assert/strict';
import test from 'node:test';
import {
  defaultSyntheticAPIStep,
  defaultSyntheticBrowserStep,
  templateRefs,
  undefinedRefsWarning,
  undefinedSyntheticAPIRefs,
  undefinedSyntheticBrowserRefs,
} from './monitor-form/synthetic';
import { initialMonitorFormState, validateMonitorForm } from './monitor-form/inline';

// Undefined {{references}} in synthetic steps are warned about next to the
// step, never refused: the save goes through and the worker fails the step.

test('templateRefs lists each referenced name once, in order', () => {
  assert.deepEqual(templateRefs('{{a}}/{{ b }}?x={{a}}&{{c.d-e}}'), ['a', 'b', 'c.d-e']);
  assert.deepEqual(templateRefs('no refs, {{}} or {{bad name}}'), []);
});

test('api steps: variables, secret variables and earlier extractions define names', () => {
  const login = {
    ...defaultSyntheticAPIStep(1),
    url: '/login?u={{user}}',
    body: '{"p":"{{password}}"}',
    extracts: [{ name: 'token', from: 'json' as const, path: 'token', sensitive: true }],
  };
  const me = {
    ...defaultSyntheticAPIStep(2),
    url: '/me/{{tenant}}',
    headers: [{ key: 'Authorization', value: 'Bearer {{token}}' }],
  };
  // An extraction is only available from the next step on.
  const early = { ...defaultSyntheticAPIStep(3), url: '/x?{{later}}', extracts: [{ name: 'later', from: 'json' as const, path: 'x', sensitive: false }] };
  const refs = undefinedSyntheticAPIRefs([{ key: 'user' }], [{ key: 'password' }], [login, me, early]);
  assert.deepEqual(refs, [[], ['tenant'], ['later']]);
  assert.equal(
    undefinedRefsWarning(refs[1]),
    '{{tenant}} is not defined by any variable or earlier extraction; this step will fail when it runs.'
  );
  assert.equal(undefinedRefsWarning([]), undefined);
});

test('browser steps: url, selector and value are checked against both maps', () => {
  const steps = [
    { ...defaultSyntheticBrowserStep(1), action: 'fill' as const, selector: '#p', value: '{{password}}' },
    { ...defaultSyntheticBrowserStep(2), action: 'goto' as const, url: 'https://x.test/{{tenant}}/{{region}}' },
  ];
  assert.deepEqual(undefinedSyntheticBrowserRefs([{ key: 'region' }], [{ key: 'password' }], steps), [[], ['tenant']]);
});

test('an undefined reference does not block the save', () => {
  const form = {
    ...initialMonitorFormState(),
    name: 'Journey',
    synthetic_api_steps: [{ ...defaultSyntheticAPIStep(1), url: 'https://x.test/{{nope}}' }],
  };
  assert.deepEqual(validateMonitorForm('synthetic_api', form, false), {});
});
