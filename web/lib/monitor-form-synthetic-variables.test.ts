import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';
import {
  SENSITIVE_VARIABLE_PAIRS,
  SENSITIVE_VARIABLE_WORDS,
  defaultSyntheticAPIStep,
  isSensitiveVariableName,
} from './monitor-form/synthetic';
import { buildMonitorFormRequest, initialMonitorFormState, validateMonitorForm } from './monitor-form/inline';
import type { SyntheticAPIMonitorConfig, SyntheticBrowserMonitorConfig } from './types';

// The variable rule lives twice: isSensitiveVariableName here (the forms show
// such a variable as secret and submit it to secret_variables) and
// shared/secrets IsSensitiveVariableName (every API write moves it). Both
// suites read this one fixture. npm test runs from web/.
const FIXTURE = join(process.cwd(), '..', 'shared', 'secrets', 'testdata', 'sensitive_variable_names.json');

test('isSensitiveVariableName matches the shared fixture the Go twin also reads', () => {
  const fixture = JSON.parse(readFileSync(FIXTURE, 'utf8')) as {
    final_words: string[];
    final_pairs: [string, string][];
    sensitive: string[];
    plain: string[];
  };
  assert.deepEqual([...SENSITIVE_VARIABLE_WORDS], fixture.final_words);
  assert.deepEqual(SENSITIVE_VARIABLE_PAIRS.map((pair) => [...pair]), fixture.final_pairs);
  for (const name of fixture.sensitive) {
    assert.equal(isSensitiveVariableName(name), true, `${JSON.stringify(name)} should be sensitive`);
  }
  for (const name of fixture.plain) {
    assert.equal(isSensitiveVariableName(name), false, `${JSON.stringify(name)} should be plain`);
  }
});

test('api: a credential-named plain variable is submitted as a secret, an ordinary one stays plain', () => {
  const form = {
    ...initialMonitorFormState(),
    name: 'Journey',
    synthetic_api_variables: [
      { key: 'token_url', value: 'https://auth.test/oauth/token' },
      { key: 'api_key', value: 'k-1' },
      { key: 'empty_token', value: '' },
    ],
    synthetic_api_steps: [{ ...defaultSyntheticAPIStep(1), url: '{{token_url}}?k={{api_key}}' }],
  };
  assert.deepEqual(validateMonitorForm('synthetic_api', form, false), {});
  const config = buildMonitorFormRequest('synthetic_api', form, false).config as SyntheticAPIMonitorConfig;
  assert.deepEqual(config.variables, { token_url: 'https://auth.test/oauth/token', empty_token: '' });
  assert.deepEqual(config.secret_variables, { api_key: 'k-1' });
});

test('browser: a credential-named plain variable is submitted as a secret', () => {
  const form = {
    ...initialMonitorFormState(),
    name: 'Login',
    synthetic_browser_start_url: 'https://app.test/login',
    synthetic_browser_variables: [
      { key: 'forgot_password_link', value: 'a.forgot' },
      { key: 'adminPassword', value: 'pw' },
    ],
  };
  const config = buildMonitorFormRequest('synthetic_browser', form, false).config as SyntheticBrowserMonitorConfig;
  assert.deepEqual(config.variables, { forgot_password_link: 'a.forgot' });
  assert.deepEqual(config.secret_variables, { adminPassword: 'pw' });
});

test('secret names that differ only by case are refused before save', () => {
  const form = {
    ...initialMonitorFormState(),
    name: 'Journey',
    synthetic_api_variables: [{ key: 'Password', value: 'a' }],
    synthetic_api_secret_variables: [{ key: 'password', value: 'b', secret: true }],
    synthetic_api_steps: [{ ...defaultSyntheticAPIStep(1), url: 'https://x.test' }],
  };
  assert.equal(
    validateMonitorForm('synthetic_api', form, false).synthetic_api_secret_variables,
    '"password" and "Password" differ only by case; rename one'
  );
});
