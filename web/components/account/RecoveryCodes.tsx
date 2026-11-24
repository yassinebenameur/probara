'use client';

import { Copy, Download } from 'lucide-react';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import { recoveryCodesFile } from '@/lib/account-links';
import { useClipboard } from '@/lib/useClipboard';

/**
 * Freshly issued recovery codes, shown once: the API stores only their hashes.
 * Each signs in once in place of an authenticator code.
 */
export default function RecoveryCodes({ username, codes, onDone }: { username: string; codes: string[]; onDone: () => void }) {
  const { copy, copied } = useClipboard();
  const text = recoveryCodesFile(username, codes);

  const download = () => {
    const url = URL.createObjectURL(new Blob([text], { type: 'text/plain' }));
    const a = document.createElement('a');
    a.href = url;
    a.download = 'probara-recovery-codes.txt';
    a.click();
    URL.revokeObjectURL(url);
  };

  return (
    <div className="space-y-4">
      <Callout tone="warning" title="Save your recovery codes now">
        They are shown only this once. Each code signs you in once if you lose your authenticator; keep them somewhere
        safe, away from your phone.
      </Callout>
      <ol className="glass-well grid grid-cols-2 gap-x-6 gap-y-1.5 rounded-xl p-4 font-mono text-sm text-slate-200">
        {codes.map((code) => (
          <li key={code}>{code}</li>
        ))}
      </ol>
      <div className="flex flex-wrap gap-2">
        <Button size="xs" icon={<Copy />} onClick={() => void copy(text)}>
          {copied ? 'Copied' : 'Copy'}
        </Button>
        <Button size="xs" icon={<Download />} onClick={download}>
          Download
        </Button>
        <Button size="xs" variant="accent" onClick={onDone}>
          I have saved them
        </Button>
      </div>
    </div>
  );
}
