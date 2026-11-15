import assert from 'node:assert/strict';
import test from 'node:test';
import { bootstrapRequestBody, bootstrapView } from './bootstrap';

test('the setup code field appears only when the API requires it', () => {
  assert.deepEqual(bootstrapView({ has_admin_users: false, setup_code_required: true }), {
    mode: 'bootstrap',
    setupCodeRequired: true,
  });
  assert.deepEqual(bootstrapView({ has_admin_users: false, setup_code_required: false }), {
    mode: 'bootstrap',
    setupCodeRequired: false,
  });
  // An API from before the setup code existed omits the field.
  assert.deepEqual(bootstrapView({ has_admin_users: false }), {
    mode: 'bootstrap',
    setupCodeRequired: false,
  });
  assert.deepEqual(bootstrapView({ has_admin_users: true, setup_code_required: true }), {
    mode: 'login',
    setupCodeRequired: false,
  });
});

test('the request carries the setup code only when required', () => {
  assert.deepEqual(bootstrapRequestBody(' admin ', 'pw', ' ABCD-EFGH ', true), {
    username: 'admin',
    password: 'pw',
    setup_code: 'ABCD-EFGH',
  });
  assert.deepEqual(bootstrapRequestBody('admin', 'pw', 'stale', false), {
    username: 'admin',
    password: 'pw',
  });
});
