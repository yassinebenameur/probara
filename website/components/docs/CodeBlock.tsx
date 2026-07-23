'use client';

import { useState } from 'react';
import { Check, Copy } from 'lucide-react';

export function CodeBlock({
  code,
  language = 'text',
  title,
}: {
  code: string;
  language?: string;
  title?: string;
}) {
  const [copied, setCopied] = useState(false);

  const copy = async () => {
    await navigator.clipboard.writeText(code);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1600);
  };

  return (
    <div className="doc-code">
      <div className="doc-code__head">
        <span>{title || language}</span>
        <button type="button" onClick={copy}>
          {copied ? <Check size={13} /> : <Copy size={13} />}
          {copied ? 'Copied' : 'Copy'}
        </button>
      </div>
      <pre>
        <code>{code}</code>
      </pre>
    </div>
  );
}
