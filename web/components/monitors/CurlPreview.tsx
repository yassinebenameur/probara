'use client';

import { useState, useCallback } from 'react';
import { Check, Copy } from 'lucide-react';
import Button from '@/components/ui/Button';

import { HeaderKV, isSensitiveHeaderName } from '@/lib/monitor-form/http';
import { isSecretRow } from '@/lib/monitor-form/secret-map';

// Secret rows (and every credential-named header) print masked: the preview
// is meant to be copied around, and a stored secret has no value here anyway.
function buildCurl(method: string, url: string, headers: HeaderKV[], basicAuthUser: string, body: string): string {
  const parts: string[] = [`curl -X ${method || 'GET'}`];
  for (const h of headers) {
    if (!h.key.trim()) continue;
    const value = isSecretRow(h, isSensitiveHeaderName) ? '***' : h.value;
    parts.push(`  -H "${h.key}: ${value}"`);
  }
  if (basicAuthUser.trim()) {
    parts.push(`  -u '${basicAuthUser.trim()}:***'`);
  }
  if (body.trim()) {
    const truncated = body.length > 80 ? body.slice(0, 80) + '...' : body;
    parts.push(`  -d '${truncated}'`);
  }
  // Never a made-up host: a placeholder would read as real configuration.
  parts.push(`  ${url.trim() || '<url>'}`);
  return parts.join(' \\\n');
}

interface CurlPreviewProps {
  method: string;
  url: string;
  headers: HeaderKV[];
  /** Basic auth username; the password always prints masked. */
  basicAuthUser?: string;
  body: string;
}

// Compact, copyable cURL equivalent of the configured request.
export default function CurlPreview({ method, url, headers, basicAuthUser = '', body }: CurlPreviewProps) {
  const [expanded, setExpanded] = useState(false);
  const [copied, setCopied] = useState(false);
  const curl = buildCurl(method, url, headers, basicAuthUser, body);
  const lines = curl.split('\n');
  const hasMore = lines.length > 2;
  const shown = expanded ? lines : lines.slice(0, 2);

  const handleCopy = useCallback(async () => {
    try {
      await navigator.clipboard.writeText(curl);
      setCopied(true);
      setTimeout(() => setCopied(false), 1800);
    } catch {
      /* ignore */
    }
  }, [curl]);

  return (
    <div className="glass-well flex items-start justify-between gap-2 py-2 pl-3 pr-2">
      <pre className="min-w-0 flex-1 whitespace-pre-wrap break-all py-0.5 font-mono text-xs leading-relaxed text-slate-400">
        <span className="text-slate-500">curl -X</span>{' '}
        <span className="font-medium text-slate-200">{method || 'GET'}</span>
        {shown[0].split(' ').slice(3).length > 0 ? ` ${shown[0].split(' ').slice(3).join(' ')}` : ''}
        {shown.slice(1).map((line, i) => `\n${line}`).join('')}
        {!expanded && hasMore && (
          <button
            type="button"
            onClick={() => setExpanded(true)}
            className="ml-1 text-slate-500 transition-colors hover:text-cyan-300"
          >
            …+{lines.length - 2} line{lines.length - 2 > 1 ? 's' : ''}
          </button>
        )}
        {expanded && hasMore && (
          <button
            type="button"
            onClick={() => setExpanded(false)}
            className="ml-1 text-slate-500 transition-colors hover:text-cyan-300"
          >
            collapse
          </button>
        )}
      </pre>
      <Button
        type="button"
        variant="subtle"
        size="xs"
        onClick={handleCopy}
        title="Copy cURL command"
        icon={copied ? <Check className="h-3.5 w-3.5 text-emerald-400" strokeWidth={1.75} /> : <Copy className="h-3.5 w-3.5" strokeWidth={1.75} />}
        className={copied ? '!text-emerald-300' : ''}
      >
        {copied ? 'Copied' : 'Copy'}
      </Button>
    </div>
  );
}
