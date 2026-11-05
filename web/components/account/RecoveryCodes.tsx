'use client';

import { useState } from 'react';
import { Check, Copy, Download } from 'lucide-react';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';

/**
 * Fresh recovery codes, shown once: the server keeps only their hashes.
 * Each code signs in once in place of an authenticator code.
 */
export default function RecoveryCodes({ codes, username }: { codes: string[]; username?: string }) {
  const [copied, setCopied] = useState(false);
  const text = codes.join('\n') + '\n';

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      setCopied(false);
    }
  };

  const download = () => {
    const blob = new Blob([text], { type: 'text/plain' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `probara-recovery-codes${username ? `-${username}` : ''}.txt`;
    a.click();
    URL.revokeObjectURL(url);
  };

  return (
    <div className="space-y-3">
      <Callout tone="warning">
        Save these codes somewhere safe now. They are shown only once; each one signs you in once
        if you lose your authenticator.
      </Callout>
      <ul className="glass-well grid grid-cols-2 gap-x-6 gap-y-1.5 p-4 font-mono text-sm text-slate-200">
        {codes.map((code) => (
          <li key={code}>{code}</li>
        ))}
      </ul>
      <div className="flex gap-2">
        <Button size="xs" onClick={copy} icon={copied ? <Check /> : <Copy />}>
          {copied ? 'Copied' : 'Copy'}
        </Button>
        <Button size="xs" onClick={download} icon={<Download />}>
          Download
        </Button>
      </div>
    </div>
  );
}
