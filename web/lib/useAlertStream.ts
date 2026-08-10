'use client';

import { useEffect, useRef, useCallback } from 'react';
import { refreshSession } from './api';
import { getApiKey } from './auth';
import { clearSelectedTenantId, getSelectedTenantId, setSelectedTenantId } from './tenant';
import type { Alert } from './types';

const SSE_API_BASE_URL = '/api';
const TENANTS_PATH = '/api/v1/tenants';

interface StreamRequestConfig {
  apiKeyMode: boolean;
  tenantId: string | null;
  url: string;
  init: RequestInit;
}

function buildStreamUrl(baseUrl: string, tenantId?: string | null): string {
  const base = baseUrl.replace(/\/$/, '');
  const url = new URL(`${base}/v1/alerts/stream`, typeof window !== 'undefined' ? window.location.origin : 'http://localhost');
  if (tenantId) {
    url.searchParams.set('tenant_id', tenantId);
  }
  return url.toString();
}

async function ensureTenantSelected(): Promise<string | null> {
  const storedTenantId = getSelectedTenantId();
  if (storedTenantId) {
    return storedTenantId;
  }

  try {
    const response = await fetch(TENANTS_PATH, {
      method: 'GET',
      credentials: 'include',
    });

    if (!response.ok) {
      return null;
    }

    const data = await response.json() as { items?: Array<{ id: string }> };
    const nextTenantId = data.items?.[0]?.id || null;

    if (nextTenantId) {
      setSelectedTenantId(nextTenantId);
      return nextTenantId;
    }

    clearSelectedTenantId();
    return null;
  } catch {
    return null;
  }
}

async function buildStreamRequestConfig(): Promise<StreamRequestConfig> {
  const apiKey = getApiKey();
  const headers: HeadersInit = {
    Accept: 'text/event-stream',
  };

  const tenantId = apiKey ? null : await ensureTenantSelected();

  if (apiKey) {
    headers.Authorization = `Bearer ${apiKey}`;
  }

  return {
    apiKeyMode: Boolean(apiKey),
    tenantId,
    url: buildStreamUrl(SSE_API_BASE_URL, tenantId),
    init: {
      headers,
      credentials: 'include',
    },
  };
}

interface UseAlertStreamOptions {
  onAlertCreated?: (alert: Alert) => void;
  onAlertAcknowledged?: (alert: Alert) => void;
  onAlertResolved?: (alert: Alert) => void;
  onError?: (error: Error) => void;
  onConnected?: () => void;
  enabled?: boolean;
}

export function useAlertStream(options: UseAlertStreamOptions = {}) {
  const {
    onAlertCreated,
    onAlertAcknowledged,
    onAlertResolved,
    onError,
    onConnected,
    enabled = true,
  } = options;

  const callbacksRef = useRef({
    onAlertCreated,
    onAlertAcknowledged,
    onAlertResolved,
    onError,
    onConnected,
  });

  useEffect(() => {
    callbacksRef.current = {
      onAlertCreated,
      onAlertAcknowledged,
      onAlertResolved,
      onError,
      onConnected,
    };
  }, [onAlertCreated, onAlertAcknowledged, onAlertResolved, onError, onConnected]);

  const abortControllerRef = useRef<AbortController | null>(null);
  const reconnectTimeoutRef = useRef<NodeJS.Timeout | null>(null);
  const reconnectAttempts = useRef(0);
  const isConnectedRef = useRef(false);
  const maxReconnectAttempts = 10;
  const baseReconnectDelay = 1000;
  const connectRef = useRef<(() => Promise<void>) | undefined>(undefined);

  const scheduleReconnect = useCallback(() => {
    if (reconnectAttempts.current >= maxReconnectAttempts) {
      console.error('SSE: Max reconnection attempts reached');
      callbacksRef.current.onError?.(new Error('Max reconnection attempts reached'));
      return;
    }

    const delay = Math.min(baseReconnectDelay * Math.pow(2, reconnectAttempts.current), 30000);
    reconnectAttempts.current++;

    console.log(`SSE: Scheduling reconnect in ${delay}ms (attempt ${reconnectAttempts.current})`);

    reconnectTimeoutRef.current = setTimeout(() => {
      connectRef.current?.();
    }, delay);
  }, []);

  const handleEvent = useCallback((eventType: string, data: string) => {
    try {
      if (eventType === 'connected') {
        console.log('SSE: Connected event received');
        callbacksRef.current.onConnected?.();
        return;
      }

      if (eventType === 'heartbeat') {
        return;
      }

      console.log('SSE: Received event:', eventType);
      const alert: Alert = JSON.parse(data);

      switch (eventType) {
        case 'alert_created':
          callbacksRef.current.onAlertCreated?.(alert);
          break;
        case 'alert_acknowledged':
          callbacksRef.current.onAlertAcknowledged?.(alert);
          break;
        case 'alert_resolved':
          callbacksRef.current.onAlertResolved?.(alert);
          break;
        default:
          console.warn('SSE: Unknown event type:', eventType);
      }
    } catch (error) {
      console.error('SSE: Failed to parse event:', error);
    }
  }, []);

  const connect = useCallback(async () => {
    if (abortControllerRef.current) {
      abortControllerRef.current.abort();
    }

    abortControllerRef.current = new AbortController();
    isConnectedRef.current = false;

    try {
      let requestConfig = await buildStreamRequestConfig();
      console.log('SSE: Connecting to', requestConfig.url);

      let response = await fetch(requestConfig.url, {
        ...requestConfig.init,
        signal: abortControllerRef.current.signal,
      });

      if (response.status === 401 && !requestConfig.apiKeyMode) {
        const refreshed = await refreshSession();
        if (refreshed) {
          requestConfig = await buildStreamRequestConfig();
          console.log('SSE: Reconnecting after refresh to', requestConfig.url);
          response = await fetch(requestConfig.url, {
            ...requestConfig.init,
            signal: abortControllerRef.current.signal,
          });
        }
      }

      if (!response.ok) {
        throw new Error(`SSE connection failed: ${response.status}`);
      }

      if (!response.body) {
        throw new Error('No response body');
      }

      console.log('SSE: Connection established');
      reconnectAttempts.current = 0;
      isConnectedRef.current = true;

      const reader = response.body.getReader();
      const decoder = new TextDecoder();
      let buffer = '';

      while (true) {
        const { done, value } = await reader.read();

        if (done) {
          console.log('SSE: Connection closed by server');
          isConnectedRef.current = false;
          scheduleReconnect();
          break;
        }

        buffer += decoder.decode(value, { stream: true });
        const lines = buffer.split('\n');
        buffer = lines.pop() || '';

        let currentEvent = '';
        let currentData = '';

        for (const line of lines) {
          if (line.startsWith('event:')) {
            currentEvent = line.slice(6).trim();
          } else if (line.startsWith('data:')) {
            currentData = line.slice(5).trim();
          } else if (line === '' && currentEvent && currentData) {
            handleEvent(currentEvent, currentData);
            currentEvent = '';
            currentData = '';
          }
        }
      }
    } catch (error) {
      isConnectedRef.current = false;

      if (error instanceof Error && error.name === 'AbortError') {
        console.log('SSE: Connection aborted');
        return;
      }

      console.error('SSE: Connection error:', error);
      callbacksRef.current.onError?.(error instanceof Error ? error : new Error('SSE connection failed'));
      scheduleReconnect();
    }
  }, [handleEvent, scheduleReconnect]);

  useEffect(() => {
    connectRef.current = connect;
  }, [connect]);

  const disconnect = useCallback(() => {
    console.log('SSE: Disconnecting');
    if (reconnectTimeoutRef.current) {
      clearTimeout(reconnectTimeoutRef.current);
      reconnectTimeoutRef.current = null;
    }
    if (abortControllerRef.current) {
      abortControllerRef.current.abort();
      abortControllerRef.current = null;
    }
    isConnectedRef.current = false;
    reconnectAttempts.current = 0;
  }, []);

  useEffect(() => {
    if (enabled) {
      console.log('SSE: Hook enabled, connecting...');
      void connect();
    } else {
      console.log('SSE: Hook disabled, disconnecting...');
      disconnect();
    }

    return () => {
      disconnect();
    };
  }, [enabled, connect, disconnect]);

  return {
    disconnect,
    reconnect: connect,
    isConnected: () => isConnectedRef.current,
  };
}

export default useAlertStream;
