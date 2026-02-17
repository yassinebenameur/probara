'use client';

import { useEffect, useRef, useCallback } from 'react';
import { getApiKey } from './auth';
import type { Alert } from './types';

// SSE needs to connect directly to the API, not through Next.js proxy
// Next.js rewrites buffer responses which breaks SSE streaming
const SSE_API_URL = process.env.NEXT_PUBLIC_SSE_URL || 'http://localhost:8080/api';

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

  // Use refs to store callbacks to avoid dependency changes causing reconnects
  const callbacksRef = useRef({
    onAlertCreated,
    onAlertAcknowledged,
    onAlertResolved,
    onError,
    onConnected,
  });

  // Update refs when callbacks change (but don't trigger reconnect)
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

  // Use a ref for connect to avoid stale closures
  const connectRef = useRef<() => Promise<void>>();

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
        // Heartbeat - ignore
        return;
      }

      console.log('SSE: Received event:', eventType);
      const alert: Alert = JSON.parse(data);

      switch (eventType) {
        case 'alert_created':
          console.log('SSE: Alert created:', alert.id);
          callbacksRef.current.onAlertCreated?.(alert);
          break;
        case 'alert_acknowledged':
          console.log('SSE: Alert acknowledged:', alert.id);
          callbacksRef.current.onAlertAcknowledged?.(alert);
          break;
        case 'alert_resolved':
          console.log('SSE: Alert resolved:', alert.id);
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
    const apiKey = getApiKey();
    if (!apiKey) {
      console.error('SSE: No API key found');
      callbacksRef.current.onError?.(new Error('No API key found'));
      return;
    }

    // Clean up previous connection
    if (abortControllerRef.current) {
      abortControllerRef.current.abort();
    }

    abortControllerRef.current = new AbortController();
    isConnectedRef.current = false;

    try {
      const url = `${SSE_API_URL}/v1/alerts/stream`;
      console.log('SSE: Connecting to', url);
      
      const response = await fetch(url, {
        headers: {
          Authorization: `Bearer ${apiKey}`,
          Accept: 'text/event-stream',
        },
        signal: abortControllerRef.current.signal,
      });

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
          // Connection closed, attempt reconnect
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
            // End of event - handle it
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
  }, [scheduleReconnect, handleEvent]);

  // Update connectRef whenever connect changes
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

  // Connect when enabled, disconnect on cleanup
  useEffect(() => {
    if (enabled) {
      console.log('SSE: Hook enabled, connecting...');
      connect();
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
