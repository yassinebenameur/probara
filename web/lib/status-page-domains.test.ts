import assert from 'node:assert/strict';
import test from 'node:test';
import {
  cleanHostnameInput,
  STATUS_PAGE_DOMAIN_LABEL,
  statusPageDomainHint,
  statusPageDomainState,
} from './status-page-domains';
import { statusPageDomainTone } from './status-tones';

test('a verified domain with a failing streak shows as failing', () => {
  assert.equal(statusPageDomainState({ status: 'verified' }), 'verified');
  assert.equal(statusPageDomainState({ status: 'verified', failing_since: '2026-11-17T10:00:00Z' }), 'failing');
  assert.equal(statusPageDomainState({ status: 'failed', failing_since: '2026-11-16T10:00:00Z' }), 'failed');
  assert.equal(statusPageDomainState({ status: 'pending' }), 'pending');
});

test('every domain state has a label, a hint and a tone', () => {
  for (const state of ['pending', 'verified', 'failing', 'failed'] as const) {
    assert.ok(STATUS_PAGE_DOMAIN_LABEL[state]);
    assert.ok(statusPageDomainHint(state));
  }
  assert.equal(statusPageDomainTone('verified'), 'success');
  assert.equal(statusPageDomainTone('failing'), 'warning');
  assert.equal(statusPageDomainTone('failed'), 'danger');
  assert.equal(statusPageDomainTone('pending'), 'info');
});

test('a pasted URL keeps only its host; the rest is left for the API', () => {
  assert.equal(cleanHostnameInput('  https://Status.Example.com/page?x=1 '), 'status.example.com');
  assert.equal(cleanHostnameInput('status.example.com.'), 'status.example.com');
  assert.equal(cleanHostnameInput('Status.Example.COM'), 'status.example.com');
  assert.equal(cleanHostnameInput('status.example.com:8443'), 'status.example.com:8443');
  assert.equal(cleanHostnameInput('*.example.com'), '*.example.com');
});
