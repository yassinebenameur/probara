'use client';

import { Monitor } from '@/lib/types';
import { useState } from 'react';

interface MonitorDetailJsonProps {
  monitor: Monitor;
}

export default function MonitorDetailJson({ monitor }: MonitorDetailJsonProps) {
  const [copied, setCopied] = useState(false);

  const handleCopy = async () => {
    const jsonString = JSON.stringify(monitor, null, 2);
    try {
      await navigator.clipboard.writeText(jsonString);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch (err) {
      console.error('Failed to copy JSON:', err);
    }
  };

  const jsonString = JSON.stringify(monitor, null, 2);

  return (
    <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 overflow-hidden">
      {/* Header */}
      <div className="flex items-center justify-between px-5 py-4 border-b border-white/[0.06]">
        <div>
          <h3 className="text-sm font-medium text-white">Raw JSON</h3>
          <p className="text-xs text-slate-500 mt-0.5">Monitor configuration payload</p>
        </div>
        <button
          onClick={handleCopy}
          className={`inline-flex items-center gap-2 rounded-lg px-3 py-1.5 text-xs transition-colors ${
            copied 
              ? 'bg-emerald-500/10 text-emerald-400 border border-emerald-500/20'
              : 'bg-slate-800/50 text-slate-400 border border-white/[0.08] hover:text-white'
          }`}
        >
          {copied ? (
            <>
              <svg className="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 13l4 4L19 7" />
              </svg>
              Copied!
            </>
          ) : (
            <>
              <svg className="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z" />
              </svg>
              Copy
            </>
          )}
        </button>
      </div>

      {/* Code Block */}
      <div className="p-4 bg-slate-950/50 overflow-x-auto">
        <pre className="text-xs font-mono text-slate-300 whitespace-pre">{jsonString}</pre>
      </div>
    </div>
  );
}
