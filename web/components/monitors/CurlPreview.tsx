'use client';

import { useState, useCallback } from 'react';
import { Check, Copy } from 'lucide-react';

type HeaderKV = { key: string; value: string; reveal?: boolean };

const SENSITIVE_HEADERS = new Set([
  'authorization', 'x-api-key', 'api-key', 'x-auth-token', 'token',
  'cookie', 'set-cookie', 'proxy-authorization',
]);

function maskValue(name: string, value: string) {
  return SENSITIVE_HEADERS.has(name.trim().toLowerCase()) ? '***' : value;
}

function buildCurl(method: string, url: string, headers: HeaderKV[], body: string): string {
  const parts: string[] = [`curl -X ${method || 'GET'}`];
  for (const h of headers) {
    if (!h.key.trim()) continue;
    parts.push(`  -H "${h.key}: ${maskValue(h.key, h.value)}"`);
  }
  if (body.trim()) {
    const truncated = body.length > 80 ? body.slice(0, 80) + '...' : body;
    parts.push(`  -d '${truncated}'`);
  }
  parts.push(`  ${url || 'https://example.com'}`);
  return parts.join(' \\\n');
}

interface CurlPreviewProps {
  method: string;
  url: string;
  headers: HeaderKV[];
  body: string;
}

// Compact, copyable cURL equivalent of the configured request.
export default function CurlPreview({ method, url, headers, body }: CurlPreviewProps) {
  const [expanded, setExpanded] = useState(false);
  const [copied, setCopied] = useState(false);
  const curl = buildCurl(method, url, headers, body);
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
    <div className="flex items-start justify-between gap-2 rounded-lg border border-white/[0.06] bg-slate-950/60 px-3 py-2">
      <pre className="min-w-0 flex-1 whitespace-pre-wrap break-all font-mono text-[10px] leading-relaxed text-slate-400">
        <span className="text-violet-400">curl</span> <span className="text-amber-300">-X</span>{' '}
        <span className="text-cyan-300">{method || 'GET'}</span>
        {shown[0].split(' ').slice(3).length > 0 ? ` ${shown[0].split(' ').slice(3).join(' ')}` : ''}
        {shown.slice(1).map((line, i) => `\n${line}`).join('')}
        {!expanded && hasMore && (
          <button
            type="button"
            onClick={() => setExpanded(true)}
            className="ml-1 text-slate-600 hover:text-cyan-400 transition-colors"
          >
            …+{lines.length - 2} line{lines.length - 2 > 1 ? 's' : ''}
          </button>
        )}
        {expanded && hasMore && (
          <button
            type="button"
            onClick={() => setExpanded(false)}
            className="ml-1 text-slate-600 hover:text-cyan-400 transition-colors"
          >
            collapse
          </button>
        )}
      </pre>
      <button
        type="button"
        onClick={handleCopy}
        className="flex shrink-0 items-center gap-1 rounded-md px-1.5 py-0.5 text-[10px] font-medium text-slate-500 transition-colors hover:bg-slate-800 hover:text-cyan-300"
        title="Copy cURL command"
      >
        {copied ? (
          <>
            <Check className="h-3 w-3 text-emerald-400" strokeWidth={2.5} />
            <span className="text-emerald-400">Copied</span>
          </>
        ) : (
          <>
            <Copy className="h-3 w-3" strokeWidth={2} />
            Copy
          </>
        )}
      </button>
    </div>
  );
}
