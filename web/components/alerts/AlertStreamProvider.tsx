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
import { useAlertStream } from '@/lib/useAlertStream';
import { TENANT_CHANGED_EVENT } from '@/lib/tenant';
import type { Alert, AlertStreamEvent, AlertStreamEventType } from '@/lib/types';

type AlertStreamListener = (event: AlertStreamEvent) => void;

interface AlertStreamContextValue {
  isConnected: boolean;
  subscribe: (listener: AlertStreamListener) => () => void;
}

const AlertStreamContext = createContext<AlertStreamContextValue | undefined>(undefined);

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

  const notifyListeners = useCallback((event: AlertStreamEvent) => {
    listenersRef.current.forEach((listener) => {
      listener(event);
    });
  }, []);

  const emitEvent = useCallback((type: AlertStreamEventType, alert: Alert) => {
    const event = buildAlertEvent(type, alert);
    notifyListeners(event);

    if (type === 'created' || type === 'resolved') {
      showAlertToast(alert.id, alert.monitor_name || 'Monitor', type);
    }
  }, [notifyListeners, showAlertToast]);

  const { reconnect } = useAlertStream({
    enabled: true,
    onConnected: () => setIsConnected(true),
    onAlertCreated: (alert) => emitEvent('created', alert),
    onAlertAcknowledged: (alert) => emitEvent('acknowledged', alert),
    onAlertResolved: (alert) => emitEvent('resolved', alert),
    onError: () => setIsConnected(false),
  });

  useEffect(() => {
    const handleTenantChange = () => {
      setIsConnected(false);
      void reconnect();
    };

    window.addEventListener(TENANT_CHANGED_EVENT, handleTenantChange);
    return () => {
      window.removeEventListener(TENANT_CHANGED_EVENT, handleTenantChange);
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
