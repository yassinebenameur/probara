import assert from 'node:assert/strict';
import test from 'node:test';
import { notifyModeChange } from './monitor-form/common';
import type { ChannelAssignment } from './types';

const DEFAULTS: ChannelAssignment[] = [
  { channel_id: 'slack', channel_name: 'Slack', delay_seconds: 0 },
  { channel_id: 'pager', channel_name: 'Pager', delay_seconds: 300 },
];

test('re-choosing Custom keeps a deliberately empty channel list (muted monitor)', () => {
  assert.equal(notifyModeChange('custom', 'custom', [], DEFAULTS), null);
});

test('re-choosing Workspace default is a no-op', () => {
  assert.equal(notifyModeChange('default', 'default', [], DEFAULTS), null);
});

test('switching to Custom with nothing picked pre-checks the workspace defaults', () => {
  assert.deepEqual(notifyModeChange('custom', 'default', [], DEFAULTS), {
    mode: 'custom',
    channels: [
      { channel_id: 'slack', delay_seconds: 0 },
      { channel_id: 'pager', delay_seconds: 300 },
    ],
  });
});

test('switching to Custom keeps channels already picked', () => {
  const picked: ChannelAssignment[] = [{ channel_id: 'email', delay_seconds: 0 }];
  assert.deepEqual(notifyModeChange('custom', 'default', picked, DEFAULTS), { mode: 'custom' });
});

test('switching to Custom without workspace defaults leaves the list empty', () => {
  assert.deepEqual(notifyModeChange('custom', 'default', [], []), { mode: 'custom' });
});

test('switching back to Workspace default only changes the mode', () => {
  assert.deepEqual(notifyModeChange('default', 'custom', [], DEFAULTS), { mode: 'default' });
});
