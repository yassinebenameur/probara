'use client';

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react';
import { useToast } from '@/components/ui/ToastProvider';
import { getAlerts } from '@/lib/api';
import { getApiKey } from '@/lib/auth';
import { getSelectedTenantId } from '@/lib/tenant';
import { useAlertStream } from '@/lib/useAlertStream';
import type { Alert, AlertStreamEvent, AlertStreamEventType } from '@/lib/types';

type AlertStreamListener = (event: AlertStreamEvent) => void;

interface AlertStreamContextValue {
  isConnected: boolean;
  subscribe: (listener: AlertStreamListener) => () => void;
}

const AlertStreamContext = createContext<AlertStreamContextValue | undefined>(undefined);
const TOAST_STORAGE_KEY = 'probara_seen_alert_toasts';

function getToastScope(): string {
  const tenantId = getSelectedTenantId();
  if (tenantId) {
    return `tenant:${tenantId}`;
  }

  if (getApiKey()) {
    return 'api-key';
  }

  return 'default';
}

function getToastEventKey(type: Extract<AlertStreamEventType, 'created' | 'resolved'>, alertId: string): string {
  return `${type}:${alertId}`;
}

function readSeenToastKeys(scope: string): Set<string> {
  if (typeof window === 'undefined') {
    return new Set();
  }

  try {
    const raw = sessionStorage.getItem(`${TOAST_STORAGE_KEY}:${scope}`);
    if (!raw) {
      return new Set();
    }
    const values = JSON.parse(raw) as string[];
    return new Set(values);
  } catch {
    return new Set();
  }
}

function writeSeenToastKeys(scope: string, values: Set<string>) {
  if (typeof window === 'undefined') {
    return;
  }

  sessionStorage.setItem(`${TOAST_STORAGE_KEY}:${scope}`, JSON.stringify([...values]));
}

function buildAlertEvent(type: AlertStreamEventType, alert: Alert): AlertStreamEvent {
  return {
    type,
    alert,
    received_at: new Date().toISOString(),
  };
}

export function AlertStreamProvider({ children }: { children: ReactNode }) {
  const { showAlertToast } = useToast();
  const [isConnected, setIsConnected] = useState(false);
  const listenersRef = useRef<Set<AlertStreamListener>>(new Set());
  const hydratedScopeRef = useRef<string | null>(null);

  const notifyListeners = useCallback((event: AlertStreamEvent) => {
    listenersRef.current.forEach((listener) => {
      listener(event);
    });
  }, []);

  const showDedupedToast = useCallback((alert: Alert, type: Extract<AlertStreamEventType, 'created' | 'resolved'>) => {
    const scope = getToastScope();
    const eventKey = getToastEventKey(type, alert.id);
    const seenKeys = readSeenToastKeys(scope);

    if (seenKeys.has(eventKey)) {
      return;
    }

    seenKeys.add(eventKey);
    writeSeenToastKeys(scope, seenKeys);
    showAlertToast(alert.id, alert.monitor_name || 'Monitor', type);
  }, [showAlertToast]);

  const hydrateActiveAlertToasts = useCallback(async () => {
    const scope = getToastScope();
    if (hydratedScopeRef.current === scope) {
      return;
    }

    try {
      const response = await getAlerts({
        status: 'active',
        page: 1,
        page_size: 50,
      });

      response.items.forEach((alert) => {
        showDedupedToast(alert, 'created');
      });

      hydratedScopeRef.current = scope;
    } catch (error) {
      console.error('Failed to hydrate active alert toasts', error);
    }
  }, [showDedupedToast]);

  const emitEvent = useCallback((type: AlertStreamEventType, alert: Alert) => {
    const event = buildAlertEvent(type, alert);
    notifyListeners(event);

    if (type === 'created' || type === 'resolved') {
      showDedupedToast(alert, type);
    }
  }, [notifyListeners, showDedupedToast]);

  const { reconnect } = useAlertStream({
    enabled: true,
    onConnected: () => {
      setIsConnected(true);
      void hydrateActiveAlertToasts();
    },
    onAlertCreated: (alert) => emitEvent('created', alert),
    onAlertAcknowledged: (alert) => emitEvent('acknowledged', alert),
    onAlertResolved: (alert) => emitEvent('resolved', alert),
    onError: () => setIsConnected(false),
  });

  useEffect(() => {
    const handleTenantChange = () => {
      setIsConnected(false);
      hydratedScopeRef.current = null;
      void reconnect();
    };

    window.addEventListener('tenant-changed', handleTenantChange);
    return () => {
      window.removeEventListener('tenant-changed', handleTenantChange);
    };
  }, [reconnect]);

  const subscribe = useCallback((listener: AlertStreamListener) => {
    listenersRef.current.add(listener);
    return () => {
      listenersRef.current.delete(listener);
    };
  }, []);

  const value = useMemo(() => ({
    isConnected,
    subscribe,
  }), [isConnected, subscribe]);

  return (
    <AlertStreamContext.Provider value={value}>
      {children}
    </AlertStreamContext.Provider>
  );
}

export function useAlertEvents() {
  const context = useContext(AlertStreamContext);
  if (context === undefined) {
    throw new Error('useAlertEvents must be used within an AlertStreamProvider');
  }
  return context;
}
