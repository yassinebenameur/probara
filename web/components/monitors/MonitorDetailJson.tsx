'use client';

import { Monitor } from '@/lib/types';
import { useState } from 'react';
import { Check, Copy } from 'lucide-react';
import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';

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
    <Panel
      title="Raw JSON"
      subtitle="Monitor configuration payload"
      actions={
        <Button
          variant="ghost"
          size="xs"
          onClick={handleCopy}
          icon={
            copied ? (
              <Check className="h-3.5 w-3.5 text-emerald-400" strokeWidth={1.75} />
            ) : (
              <Copy className="h-3.5 w-3.5" strokeWidth={1.75} />
            )
          }
        >
          {copied ? 'Copied!' : 'Copy'}
        </Button>
      }
    >
      <pre className="glass-well overflow-x-auto whitespace-pre p-4 font-mono text-xs text-slate-300">{jsonString}</pre>
    </Panel>
  );
}
