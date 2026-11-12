'use client';

import { useEffect, useState } from 'react';
import { useToast } from '@/components/ui/ToastProvider';
import { ApiKey, ApiKeyScope } from '@/lib/types';
import { createApiKey, getApiKeys, revokeApiKey } from '@/lib/api';
import { getApiKey } from '@/lib/auth';
import { saveStoredApiKey, removeStoredApiKey } from '@/lib/api-keys';
import { errorMessage } from '@/lib/errors';
import { useClipboard } from '@/lib/useClipboard';

export function isExpired(expiresAt?: string | null): boolean {
  return Boolean(expiresAt) && new Date(expiresAt as string).getTime() <= Date.now();
}

/**
 * The workspace's API keys and the create / revoke flows. Held by the
 * settings page, not the Access tab, so a just-created key (shown once) and
 * the form survive a tab switch.
 */
export function useApiKeys() {
  const { showToast } = useToast();
  const [apiKeys, setApiKeys] = useState<ApiKey[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [creating, setCreating] = useState(false);
  const [newKeyName, setNewKeyName] = useState('');
  const [newKeyScope, setNewKeyScope] = useState<ApiKeyScope>('write');
  const [newKeyExpiry, setNewKeyExpiry] = useState('');
  const [createdKey, setCreatedKey] = useState<ApiKey | null>(null);
  const clipboard = useClipboard();
  const [pendingRevoke, setPendingRevoke] = useState<ApiKey | null>(null);
  const [revoking, setRevoking] = useState(false);
  const [browserKeyPresent, setBrowserKeyPresent] = useState(false);
  const [showInactiveKeys, setShowInactiveKeys] = useState(false);

  useEffect(() => {
    setBrowserKeyPresent(Boolean(getApiKey()));
  }, []);

  const loadKeys = async () => {
    try {
      setLoading(true);
      setError('');
      const response = await getApiKeys({ page_size: 100 });
      setApiKeys(response.items || []);
    } catch (err) {
      setError(errorMessage(err, 'Failed to load API keys'));
      setApiKeys([]);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    loadKeys();
  }, []);

  const handleCreate = async (event: React.FormEvent) => {
    event.preventDefault();
    if (!newKeyName.trim()) {
      showToast('API key name is required', 'error');
      return;
    }

    let expiresAt: string | undefined;
    if (newKeyExpiry) {
      const days = Number(newKeyExpiry);
      expiresAt = new Date(Date.now() + days * 24 * 60 * 60 * 1000).toISOString();
    }

    try {
      setCreating(true);
      const created = await createApiKey({
        name: newKeyName.trim(),
        scope: newKeyScope,
        expires_at: expiresAt,
      });
      setCreatedKey(created);
      setApiKeys((prev) => [created, ...prev]);
      if (created.key) {
        saveStoredApiKey({
          id: created.id,
          name: created.name,
          key: created.key,
          key_prefix: created.key_prefix,
          created_at: created.created_at,
        });
      }
      setNewKeyName('');
      showToast('API key created', 'success');
    } catch (err) {
      showToast(errorMessage(err, 'Failed to create API key'), 'error');
    } finally {
      setCreating(false);
    }
  };

  const handleRevoke = async () => {
    if (!pendingRevoke) return;
    setRevoking(true);
    try {
      await revokeApiKey(pendingRevoke.id);
      setApiKeys((prev) =>
        prev.map((item) =>
          item.id === pendingRevoke.id ? { ...item, revoked_at: new Date().toISOString() } : item
        )
      );
      removeStoredApiKey(pendingRevoke.id);
      showToast('API key revoked', 'success');
      setPendingRevoke(null);
    } catch (err) {
      showToast(errorMessage(err, 'Failed to revoke API key'), 'error');
    } finally {
      setRevoking(false);
    }
  };

  const activeKeys = apiKeys.filter((key) => !key.revoked_at && !isExpired(key.expires_at));
  const inactiveCount = apiKeys.length - activeKeys.length;

  return {
    apiKeys,
    loading,
    error,
    creating,
    newKeyName,
    setNewKeyName,
    newKeyScope,
    setNewKeyScope,
    newKeyExpiry,
    setNewKeyExpiry,
    createdKey,
    clipboard,
    pendingRevoke,
    setPendingRevoke,
    revoking,
    browserKeyPresent,
    showInactiveKeys,
    setShowInactiveKeys,
    loadKeys,
    handleCreate,
    handleRevoke,
    activeKeys,
    inactiveCount,
  };
}

export type ApiKeysState = ReturnType<typeof useApiKeys>;
