'use client';

import { createContext, useContext, useState, useCallback, ReactNode, useEffect } from 'react';

export interface ToastMessage {
  id: string;
  message: string;
  title?: string;
  type: 'success' | 'error' | 'warning' | 'info';
  duration?: number;
}

interface ToastContextType {
  toasts: ToastMessage[];
  showToast: (message: string, type?: ToastMessage['type'], duration?: number) => void;
  showAlertToast: (alertName: string, monitorName: string, eventType: 'created' | 'acknowledged' | 'resolved') => void;
  removeToast: (id: string) => void;
}

const ToastContext = createContext<ToastContextType | undefined>(undefined);

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<ToastMessage[]>([]);

  const removeToast = useCallback((id: string) => {
    setToasts(prev => prev.filter(toast => toast.id !== id));
  }, []);

  const showToast = useCallback((message: string, type: ToastMessage['type'] = 'info', duration = 5000) => {
    const id = `${Date.now()}-${Math.random().toString(36).substr(2, 9)}`;
    setToasts(prev => [...prev, { id, message, type, duration }]);

    setTimeout(() => {
      removeToast(id);
    }, duration);
  }, [removeToast]);

  const showAlertToast = useCallback((alertName: string, monitorName: string, eventType: 'created' | 'acknowledged' | 'resolved') => {
    const id = `${Date.now()}-${Math.random().toString(36).substr(2, 9)}`;
    
    const configs: Record<string, { title: string; message: string; type: ToastMessage['type']; duration: number }> = {
      created: {
        title: 'Alert triggered',
        message: `${monitorName} is down`,
        type: 'error',
        duration: 30000, // 30 seconds for critical alerts
      },
      acknowledged: {
        title: 'Alert acknowledged',
        message: `${monitorName}`,
        type: 'warning',
        duration: 15000, // 15 seconds
      },
      resolved: {
        title: 'Alert resolved',
        message: `${monitorName} is back up`,
        type: 'success',
        duration: 15000, // 15 seconds
      },
    };

    const config = configs[eventType];
    setToasts(prev => [...prev, { id, title: config.title, message: config.message, type: config.type, duration: config.duration }]);

    setTimeout(() => {
      removeToast(id);
    }, config.duration);
  }, [removeToast]);

  return (
    <ToastContext.Provider value={{ toasts, showToast, showAlertToast, removeToast }}>
      {children}
      {/* Toast container */}
      <div className="fixed bottom-4 right-4 z-[60] flex flex-col gap-2 sm:bottom-6 sm:right-6">
        {toasts.map(toast => (
          <ToastItem key={toast.id} toast={toast} onClose={() => removeToast(toast.id)} />
        ))}
      </div>
    </ToastContext.Provider>
  );
}

// Icons for different toast types
function AlertIcon() {
  return (
    <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
      <path strokeLinecap="round" strokeLinejoin="round" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z" />
    </svg>
  );
}

function CheckIcon() {
  return (
    <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
      <path strokeLinecap="round" strokeLinejoin="round" d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z" />
    </svg>
  );
}

function InfoIcon() {
  return (
    <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
      <path strokeLinecap="round" strokeLinejoin="round" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
    </svg>
  );
}

function WarningIcon() {
  return (
    <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
      <path strokeLinecap="round" strokeLinejoin="round" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
    </svg>
  );
}

function CloseIcon() {
  return (
    <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
      <path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" />
    </svg>
  );
}

function ToastItem({ toast, onClose }: { toast: ToastMessage; onClose: () => void }) {
  const [isVisible, setIsVisible] = useState(false);

  useEffect(() => {
    // Trigger animation after mount
    const timer = setTimeout(() => setIsVisible(true), 10);
    return () => clearTimeout(timer);
  }, []);

  const typeConfig: Record<ToastMessage['type'], { icon: React.ReactNode; tint: string; bar: string }> = {
    error: { icon: <AlertIcon />, tint: 'bg-rose-500/[0.14] text-rose-400', bar: 'bg-rose-500' },
    success: { icon: <CheckIcon />, tint: 'bg-emerald-500/[0.14] text-emerald-400', bar: 'bg-emerald-500' },
    warning: { icon: <WarningIcon />, tint: 'bg-amber-500/[0.16] text-amber-400', bar: 'bg-amber-500' },
    info: { icon: <InfoIcon />, tint: 'bg-sky-500/[0.14] text-sky-400', bar: 'bg-sky-500' },
  };

  const config = typeConfig[toast.type];

  return (
    <div
      className={`transform transition-all duration-300 ease-out motion-reduce:transition-none ${
        isVisible ? 'translate-y-0 opacity-100' : 'translate-y-2 opacity-0'
      }`}
      role="alert"
    >
      <div className="glass-overlay relative w-[min(22rem,calc(100vw-2rem))] overflow-hidden !rounded-2xl">
        <div className="flex items-start gap-3 p-3.5">
          <div className={`flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-full ${config.tint} [&_svg]:h-4 [&_svg]:w-4`}>
            {config.icon}
          </div>

          <div className="min-w-0 flex-1 pt-1">
            {toast.title && <p className="text-sm font-semibold text-white">{toast.title}</p>}
            <p className={`text-sm ${toast.title ? 'text-slate-400' : 'text-white'}`}>{toast.message}</p>
          </div>

          <button
            onClick={onClose}
            className="flex-shrink-0 rounded-lg p-1.5 text-slate-500 transition-colors hover:bg-well-hover hover:text-slate-200"
            aria-label="Close"
          >
            <CloseIcon />
          </button>
        </div>

        <div className="absolute inset-x-0 bottom-0 h-0.5 bg-well-hover">
          <div
            className={`h-full ${config.bar} animate-toast-progress opacity-60`}
            style={{ animationDuration: `${toast.duration || 5000}ms` }}
          />
        </div>
      </div>
    </div>
  );
}

export function useToast() {
  const context = useContext(ToastContext);
  if (context === undefined) {
    throw new Error('useToast must be used within a ToastProvider');
  }
  return context;
}
