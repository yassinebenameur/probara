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
        title: 'Alert Triggered',
        message: `${monitorName} is down`,
        type: 'error',
        duration: 30000, // 30 seconds for critical alerts
      },
      acknowledged: {
        title: 'Alert Acknowledged',
        message: `${monitorName}`,
        type: 'warning',
        duration: 15000, // 15 seconds
      },
      resolved: {
        title: 'Alert Resolved',
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
      <div className="fixed bottom-6 right-6 z-50 flex flex-col gap-3">
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

  const typeConfig: Record<ToastMessage['type'], { 
    icon: React.ReactNode; 
    iconBg: string; 
    iconColor: string;
    bgColor: string;
    borderColor: string;
    glowColor: string;
  }> = {
    error: {
      icon: <AlertIcon />,
      iconBg: 'bg-rose-500/30',
      iconColor: 'text-rose-300',
      bgColor: 'bg-rose-950/80',
      borderColor: 'border-rose-500/50',
      glowColor: 'shadow-[0_0_30px_rgba(244,63,94,0.3)]',
    },
    success: {
      icon: <CheckIcon />,
      iconBg: 'bg-emerald-500/30',
      iconColor: 'text-emerald-300',
      bgColor: 'bg-emerald-950/80',
      borderColor: 'border-emerald-500/50',
      glowColor: 'shadow-[0_0_30px_rgba(16,185,129,0.3)]',
    },
    warning: {
      icon: <WarningIcon />,
      iconBg: 'bg-amber-500/30',
      iconColor: 'text-amber-300',
      bgColor: 'bg-amber-950/80',
      borderColor: 'border-amber-500/50',
      glowColor: 'shadow-[0_0_30px_rgba(245,158,11,0.3)]',
    },
    info: {
      icon: <InfoIcon />,
      iconBg: 'bg-cyan-500/30',
      iconColor: 'text-cyan-300',
      bgColor: 'bg-cyan-950/80',
      borderColor: 'border-cyan-500/50',
      glowColor: 'shadow-[0_0_30px_rgba(6,182,212,0.3)]',
    },
  };

  const config = typeConfig[toast.type];

  return (
    <div
      className={`
        transform transition-all duration-300 ease-out
        ${isVisible ? 'translate-x-0 opacity-100' : 'translate-x-full opacity-0'}
      `}
      role="alert"
    >
      <div
        className={`
          relative overflow-hidden
          ${config.bgColor} backdrop-blur-xl
          border ${config.borderColor}
          rounded-xl ${config.glowColor}
          min-w-[340px] max-w-md
        `}
      >
        {/* Progress bar */}
        <div className="absolute bottom-0 left-0 right-0 h-0.5 bg-slate-800">
          <div 
            className={`h-full ${config.iconColor.replace('text-', 'bg-')} animate-toast-progress`}
            style={{ animationDuration: `${toast.duration || 5000}ms` }}
          />
        </div>
        
        <div className="flex items-start gap-3 p-4">
          {/* Icon */}
          <div className={`flex-shrink-0 p-2 rounded-lg ${config.iconBg} ${config.iconColor}`}>
            {config.icon}
          </div>
          
          {/* Content */}
          <div className="flex-1 min-w-0 pt-0.5">
            {toast.title && (
              <p className="text-sm font-semibold text-white">{toast.title}</p>
            )}
            <p className={`text-sm ${toast.title ? 'text-slate-400' : 'text-white'}`}>
              {toast.message}
            </p>
          </div>
          
          {/* Close button */}
          <button
            onClick={onClose}
            className="flex-shrink-0 p-1.5 rounded-lg text-slate-500 hover:text-slate-300 hover:bg-slate-800/50 transition-colors"
            aria-label="Close"
          >
            <CloseIcon />
          </button>
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
