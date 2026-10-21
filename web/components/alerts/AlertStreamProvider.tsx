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
import { usePathname } from 'next/navigation';
import { useToast } from '@/components/ui/ToastProvider';
import { alertToast, summarizeAlertBurst } from '@/lib/alert-toast';
import { useAlertStream } from '@/lib/useAlertStream';
import { TENANT_CHANGED_EVENT } from '@/lib/tenant';
import type { Alert, AlertStreamEvent, AlertStreamEventType } from '@/lib/types';

type AlertStreamListener = (event: AlertStreamEvent) => void;
type ResyncListener = () => void;

/**
 * connecting   first connection (or after a tenant switch) not open yet
 * live         the stream is open
 * reconnecting it dropped; a retry is scheduled
 * offline      retries ran out; it retries when the browser is back online
 *              or the tab becomes visible
 */
export type AlertStreamConnection = 'connecting' | 'live' | 'reconnecting' | 'offline';

interface AlertStreamContextValue {
  /** True only while the stream is open (connection === 'live'). */
  isConnected: boolean;
  connection: AlertStreamConnection;
  subscribe: (listener: AlertStreamListener) => () => void;
  /**
   * Called once each time the stream comes back after a drop: events in the
   * gap are lost, so live lists refetch here. Returns an unsubscribe.
   */
  subscribeResync: (listener: ResyncListener) => () => void;
  /** Alias of subscribeResync. */
  onReconnect: (listener: ResyncListener) => () => void;
  /**
   * Call before acknowledging or resolving an alert from the UI: the stream
   * echo of the user's own action then gets no toast (the row already shows
   * it, and a manual resolve is not "back up").
   */
  markLocalAction: (alertId: string) => void;
}

const AlertStreamContext = createContext<AlertStreamContextValue | undefined>(undefined);

const LOCAL_ACTION_TTL_MS = 10_000;
// Stream toasts are ambient: a few seconds, read politely.
const STREAM_TOAST_MS = 8_000;

function buildAlertEvent(type: AlertStreamEventType, alert: Alert): AlertStreamEvent {
  return {
    type,
    alert,
    received_at: new Date().toISOString(),
  };
}

export function AlertStreamProvider({ children }: { children: ReactNode }) {
  const { showToast } = useToast();
  const pathname = usePathname();
  const pathnameRef = useRef(pathname);
  const [connection, setConnection] = useState<AlertStreamConnection>('connecting');
  const listenersRef = useRef<Set<AlertStreamListener>>(new Set());
  const resyncListenersRef = useRef<Set<ResyncListener>>(new Set());
  const localActionsRef = useRef<Map<string, number>>(new Map());
  // Set when the stream drops after having been open: the next open resyncs.
  const droppedRef = useRef(false);

  useEffect(() => {
    pathnameRef.current = pathname;
  }, [pathname]);

  const notifyListeners = useCallback((event: AlertStreamEvent) => {
    listenersRef.current.forEach((listener) => {
      listener(event);
    });
  }, []);

  const isLocalAction = useCallback((alertId: string) => {
    const now = Date.now();
    const local = localActionsRef.current;
    local.forEach((expires, id) => {
      if (expires <= now) local.delete(id);
    });
    return local.has(alertId);
  }, []);

  const emitEvent = useCallback((type: AlertStreamEventType, alert: Alert) => {
    const event = buildAlertEvent(type, alert);
    notifyListeners(event);

    // The alerts page shows the change in its table, live; a toast there
    // would cover the very rows (and their actions) it reports on.
    if (pathnameRef.current?.startsWith('/alerts')) return;
    if (isLocalAction(alert.id)) return;
    const toast = alertToast(type, alert);
    if (!toast) return;
    // Bursts fold into one toast per group ("5 alerts triggered").
    showToast({
      title: toast.title,
      message: toast.message,
      type: toast.type,
      group: toast.group,
      meta: alert,
      duration: STREAM_TOAST_MS,
      politeness: 'polite',
      summarize: (entries) => summarizeAlertBurst(type, entries.map((entry) => entry.meta as Alert)),
      action: type === 'created' ? { label: 'View alerts', href: '/alerts?status=active' } : undefined,
    });
  }, [isLocalAction, notifyListeners, showToast]);

  const onConnected = useCallback(() => {
    setConnection('live');
    if (droppedRef.current) {
      droppedRef.current = false;
      resyncListenersRef.current.forEach((listener) => listener());
    }
  }, []);

  const onDisconnected = useCallback(() => {
    droppedRef.current = true;
    setConnection((current) => (current === 'offline' ? current : 'reconnecting'));
  }, []);

  const onStalled = useCallback(() => {
    droppedRef.current = true;
    setConnection('offline');
  }, []);

  const { reconnect } = useAlertStream({
    enabled: true,
    onConnected,
    onDisconnected,
    onStalled,
    onAlertCreated: (alert) => emitEvent('created', alert),
    onAlertAcknowledged: (alert) => emitEvent('acknowledged', alert),
    onAlertResolved: (alert) => emitEvent('resolved', alert),
  });

  useEffect(() => {
    const handleTenantChange = () => {
      // The pages remount on a tenant switch (TenantScope), so the new
      // stream needs no resync of its own.
      droppedRef.current = false;
      setConnection('connecting');
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

  const subscribeResync = useCallback((listener: ResyncListener) => {
    resyncListenersRef.current.add(listener);
    return () => {
      resyncListenersRef.current.delete(listener);
    };
  }, []);

  const markLocalAction = useCallback((alertId: string) => {
    localActionsRef.current.set(alertId, Date.now() + LOCAL_ACTION_TTL_MS);
  }, []);

  const value = useMemo(() => ({
    isConnected: connection === 'live',
    connection,
    subscribe,
    subscribeResync,
    onReconnect: subscribeResync,
    markLocalAction,
  }), [connection, subscribe, subscribeResync, markLocalAction]);

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

/** useAlertEvents for components that may render outside the provider (null there). */
export function useOptionalAlertEvents(): AlertStreamContextValue | null {
  return useContext(AlertStreamContext) ?? null;
}
