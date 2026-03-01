'use client';

import { useState, useCallback } from 'react';

type HeaderKV = { key: string; value: string; reveal?: boolean };

export interface HttpPreviewPanelProps {
  monitorName: string;
  method: string;
  url: string;
  headers: HeaderKV[];
  body: string;
  statusRules: string;
  bodyAssertionCount: number;
  headerAssertionCount: number;
  jsonAssertionCount: number;
  maxLatencyMs: string;
  tlsSkipVerify: boolean;
  tlsMinDaysValid: string;
  tlsServerName: string;
  followRedirects: boolean;
  maxRedirects: number;
  collectTiming: boolean;
  intervalSeconds: number;
  timeoutSeconds: number;
  onScrollToSection: (sectionId: string) => void;
}

const SENSITIVE_HEADERS = new Set([
  'authorization', 'x-api-key', 'api-key', 'x-auth-token', 'token',
  'cookie', 'set-cookie', 'proxy-authorization',
]);

function isSensitive(name: string) {
  return SENSITIVE_HEADERS.has(name.trim().toLowerCase());
}

function maskValue(name: string, value: string) {
  return isSensitive(name) ? '***' : value;
}

function parseUrl(url: string) {
  try {
    const u = new URL(url.startsWith('http') ? url : `https://${url}`);
    return {
      protocol: u.protocol,
      hostname: u.hostname,
      port: u.port,
      path: u.pathname + u.search,
      isHttps: u.protocol === 'https:',
    };
  } catch {
    return { protocol: 'https:', hostname: url || '—', port: '', path: '', isHttps: true };
  }
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

const METHOD_STYLE: Record<string, { bg: string; text: string; border: string }> = {
  GET: { bg: 'bg-cyan-500/15', text: 'text-cyan-400', border: 'border-cyan-500/30' },
  POST: { bg: 'bg-emerald-500/15', text: 'text-emerald-400', border: 'border-emerald-500/30' },
  PUT: { bg: 'bg-amber-500/15', text: 'text-amber-400', border: 'border-amber-500/30' },
  DELETE: { bg: 'bg-rose-500/15', text: 'text-rose-400', border: 'border-rose-500/30' },
  PATCH: { bg: 'bg-violet-500/15', text: 'text-violet-400', border: 'border-violet-500/30' },
  HEAD: { bg: 'bg-sky-500/15', text: 'text-sky-400', border: 'border-sky-500/30' },
};

function getMethodStyle(method: string) {
  return METHOD_STYLE[method?.toUpperCase()] ?? METHOD_STYLE.GET;
}

interface CurlPreviewProps {
  method: string;
  url: string;
  headers: HeaderKV[];
  body: string;
}

function CurlPreview({ method, url, headers, body }: CurlPreviewProps) {
  const [expanded, setExpanded] = useState(false);
  const [copied, setCopied] = useState(false);
  const curl = buildCurl(method, url, headers, body);
  const lines = curl.split('\n');
  const preview = expanded ? lines : lines.slice(0, 2);
  const hasMore = lines.length > 2;

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
    <div className="rounded-xl border border-white/[0.07] bg-slate-950/60 overflow-hidden mb-4">
      <div className="flex items-center justify-between px-3 py-2 border-b border-white/[0.05]">
        <span className="text-[10px] font-semibold uppercase tracking-widest text-slate-500">cURL preview</span>
        <button
          type="button"
          onClick={handleCopy}
          className="flex items-center gap-1 rounded-md px-2 py-0.5 text-[10px] font-medium text-slate-400 hover:text-cyan-300 hover:bg-slate-800 transition-colors"
          title="Copy cURL command"
        >
          {copied ? (
            <>
              <svg className="h-3 w-3 text-emerald-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2.5}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" />
              </svg>
              <span className="text-emerald-400">Copied</span>
            </>
          ) : (
            <>
              <svg className="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                <rect x="9" y="9" width="13" height="13" rx="2" ry="2" />
                <path d="M5 15H4a2 2 0 01-2-2V4a2 2 0 012-2h9a2 2 0 012 2v1" />
              </svg>
              Copy
            </>
          )}
        </button>
      </div>
      <div className="px-3 py-2.5">
        <pre className="font-mono text-[10px] leading-relaxed text-slate-300 whitespace-pre-wrap break-all">
          {preview.map((line, i) => {
            if (i === 0) {
              const [cmd, ...rest] = line.split(' ');
              return (
                <span key={i}>
                  <span className="text-violet-400">{cmd}</span>
                  {' '}
                  <span className="text-amber-300">-X</span>
                  {' '}
                  <span className={getMethodStyle(method).text}>{method || 'GET'}</span>
                  {rest.slice(1).join(' ')}
                  {!expanded && hasMore && i === preview.length - 1 ? '' : '\n'}
                </span>
              );
            }
            return (
              <span key={i} className="text-slate-400">
                {line}{i < preview.length - 1 || (!expanded && hasMore) ? '\n' : ''}
              </span>
            );
          })}
          {!expanded && hasMore && (
            <span className="text-slate-600 italic">  …{lines.length - 2} more line{lines.length - 2 > 1 ? 's' : ''}</span>
          )}
        </pre>
        {hasMore && (
          <button
            type="button"
            onClick={() => setExpanded((v) => !v)}
            className="mt-1 text-[10px] text-slate-500 hover:text-cyan-400 transition-colors"
          >
            {expanded ? '▲ collapse' : '▼ expand'}
          </button>
        )}
      </div>
    </div>
  );
}

interface FlowNodeProps {
  label: string;
  lines: React.ReactNode[];
  configured?: boolean;
  onClick?: () => void;
  accentColor?: string;
  icon?: React.ReactNode;
  dimmed?: boolean;
}

function FlowNode({ label, lines, configured, onClick, accentColor = 'border-white/[0.08]', icon, dimmed }: FlowNodeProps) {
  const isClickable = !!onClick;
  return (
    <div
      role={isClickable ? 'button' : undefined}
      tabIndex={isClickable ? 0 : undefined}
      onClick={onClick}
      onKeyDown={isClickable ? (e) => { if (e.key === 'Enter' || e.key === ' ') onClick?.(); } : undefined}
      className={[
        'group relative w-full rounded-xl border px-3.5 py-3 transition-all duration-150',
        accentColor,
        configured ? 'border-l-2 border-l-cyan-500/40' : '',
        isClickable
          ? 'cursor-pointer hover:border-cyan-500/40 hover:bg-slate-800/60 hover:shadow-[0_0_16px_rgba(6,182,212,0.06)] active:scale-[0.99]'
          : '',
        dimmed ? 'opacity-60' : '',
        'bg-slate-900/50',
      ].filter(Boolean).join(' ')}
    >
      <div className="flex items-center justify-between mb-1.5">
        <div className="flex items-center gap-1.5">
          {icon && <span className="text-slate-500">{icon}</span>}
          <span className="text-[9px] font-bold uppercase tracking-[0.12em] text-slate-500">{label}</span>
        </div>
        {isClickable && (
          <svg
            className="h-3 w-3 text-slate-600 opacity-0 group-hover:opacity-100 transition-opacity"
            fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}
          >
            <path strokeLinecap="round" strokeLinejoin="round" d="M9 5l7 7-7 7" />
          </svg>
        )}
      </div>
      <div className="space-y-0.5">
        {lines.map((line, i) => (
          <div key={i} className="text-xs leading-snug">{line}</div>
        ))}
      </div>
    </div>
  );
}

interface FlowArrowProps {
  label?: React.ReactNode;
  methodColor?: string;
  direction?: 'down' | 'up';
}

function FlowArrow({ label, methodColor = 'text-cyan-500', direction = 'down' }: FlowArrowProps) {
  const dotStyle = {
    animation: 'flow-dot 1.6s linear infinite',
  };
  return (
    <div className="relative flex flex-col items-center my-0.5" style={{ minHeight: 48 }}>
      <svg
        viewBox="0 0 20 48"
        className={`w-5 h-12 ${methodColor}`}
        xmlns="http://www.w3.org/2000/svg"
      >
        <line
          x1="10" y1="0" x2="10" y2="48"
          stroke="currentColor"
          strokeWidth="1.5"
          strokeDasharray="3 3"
          opacity="0.4"
        />
        {direction === 'down' ? (
          <polygon points="5,38 10,48 15,38" fill="currentColor" opacity="0.5" />
        ) : (
          <polygon points="5,10 10,0 15,10" fill="currentColor" opacity="0.5" />
        )}
        <circle r="2.5" fill="currentColor" opacity="0.8">
          <animateMotion
            dur="1.6s"
            repeatCount="indefinite"
            path={direction === 'down' ? 'M 10 0 L 10 44' : 'M 10 44 L 10 0'}
          />
        </circle>
      </svg>
      {label && (
        <div className="absolute left-1/2 top-1/2 -translate-y-1/2 translate-x-3 flex flex-col gap-0.5 pointer-events-none">
          {label}
        </div>
      )}
    </div>
  );
}

export default function HttpRequestFlowPreview({
  monitorName,
  method,
  url,
  headers,
  body,
  statusRules,
  bodyAssertionCount,
  headerAssertionCount,
  jsonAssertionCount,
  maxLatencyMs,
  tlsSkipVerify,
  tlsMinDaysValid,
  tlsServerName,
  followRedirects,
  maxRedirects,
  collectTiming,
  intervalSeconds,
  timeoutSeconds,
  onScrollToSection,
}: HttpPreviewPanelProps) {
  const parsed = parseUrl(url);
  const methodStyle = getMethodStyle(method);
  const activeHeaders = headers.filter((h) => h.key.trim());
  const hasBody = !!body.trim();
  const assertionTotal = bodyAssertionCount + headerAssertionCount + jsonAssertionCount;

  // MONITOR node content
  const monitorLines: React.ReactNode[] = [
    <span key="name" className={monitorName ? 'text-white font-medium' : 'text-slate-500 italic'}>
      {monitorName || 'New Monitor'}
    </span>,
    <span key="schedule" className="text-slate-500 text-[11px]">
      every {intervalSeconds}s · timeout {timeoutSeconds}s
    </span>,
  ];

  // REQUEST node content
  const requestLines: React.ReactNode[] = [
    <span key="url" className="text-white/90 font-medium break-all">
      <span className={`${methodStyle.text} font-bold mr-1`}>{method || 'GET'}</span>
      {parsed.hostname
        ? <>{parsed.hostname}{parsed.port ? `:${parsed.port}` : ''}<span className="text-slate-500">{parsed.path || '/'}</span></>
        : <span className="text-slate-500 italic">no URL configured</span>
      }
    </span>,
  ];
  if (activeHeaders.length > 0) {
    const shown = activeHeaders.slice(0, 2);
    requestLines.push(
      <span key="headers" className="text-slate-400 text-[11px]">
        {shown.map((h) => (
          <span key={h.key} className="block">
            <span className="text-slate-300">{h.key}:</span>{' '}
            <span className="text-slate-500">{maskValue(h.key, h.value)}</span>
          </span>
        ))}
        {activeHeaders.length > 2 && (
          <span className="text-slate-600 italic">+{activeHeaders.length - 2} more</span>
        )}
      </span>
    );
  } else {
    requestLines.push(
      <span key="no-headers" className="text-slate-600 italic text-[11px]">no headers</span>
    );
  }
  if (hasBody) {
    requestLines.push(
      <span key="body" className="text-slate-400 text-[11px]">
        body: {body.length} byte{body.length !== 1 ? 's' : ''}
      </span>
    );
  }

  // SERVER node content
  const serverLines: React.ReactNode[] = [
    <span key="host" className={parsed.hostname && parsed.hostname !== '—' ? 'text-white/90' : 'text-slate-500 italic'}>
      {parsed.hostname || 'no host'}
      {parsed.port ? <span className="text-slate-500">:{parsed.port}</span> : null}
    </span>,
  ];
  if (tlsSkipVerify) {
    serverLines.push(
      <span key="tls" className="flex items-center gap-1 text-amber-400 text-[11px]">
        <svg className="h-3 w-3 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
          <path strokeLinecap="round" strokeLinejoin="round" d="M12 9v2m0 4h.01M10.29 3.86L1.82 18a2 2 0 001.71 3h16.94a2 2 0 001.71-3L13.71 3.86a2 2 0 00-3.42 0z" />
        </svg>
        TLS verification skipped
      </span>
    );
  } else {
    serverLines.push(
      <span key="tls" className="flex items-center gap-1 text-emerald-400 text-[11px]">
        <svg className="h-3 w-3 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
          <path strokeLinecap="round" strokeLinejoin="round" d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z" />
        </svg>
        TLS verified
        {tlsMinDaysValid ? ` · cert ≥${tlsMinDaysValid}d` : ''}
      </span>
    );
  }
  if (tlsServerName) {
    serverLines.push(
      <span key="sni" className="text-slate-400 text-[11px]">SNI: {tlsServerName}</span>
    );
  }
  serverLines.push(
    <span key="redirects" className="text-slate-500 text-[11px]">
      {followRedirects ? `follow redirects (max ${maxRedirects})` : 'no redirects'}
    </span>
  );

  // RESPONSE node content
  const responseLines: React.ReactNode[] = [];
  if (statusRules.trim()) {
    responseLines.push(
      <span key="status" className="text-white/90">
        expect <span className="text-cyan-300 font-mono text-[11px]">{statusRules}</span>
      </span>
    );
  } else {
    responseLines.push(
      <span key="status" className="text-slate-400">
        expect <span className="text-cyan-400/60 italic">2xx</span> (default)
      </span>
    );
  }
  if (assertionTotal > 0) {
    const parts: string[] = [];
    if (bodyAssertionCount > 0) parts.push(`${bodyAssertionCount} body`);
    if (headerAssertionCount > 0) parts.push(`${headerAssertionCount} header`);
    if (jsonAssertionCount > 0) parts.push(`${jsonAssertionCount} JSON`);
    responseLines.push(
      <span key="assertions" className="text-slate-400 text-[11px]">
        {parts.join(' · ')} assertion{assertionTotal > 1 ? 's' : ''}
      </span>
    );
  } else {
    responseLines.push(
      <span key="no-assertions" className="text-slate-600 italic text-[11px]">no assertions</span>
    );
  }
  if (maxLatencyMs.trim()) {
    responseLines.push(
      <span key="latency" className="text-slate-400 text-[11px]">
        max latency: <span className="text-white/70">{maxLatencyMs}ms</span>
      </span>
    );
  }

  // RESULT node content
  const resultLines: React.ReactNode[] = [
    <span key="check" className="flex items-center gap-1.5 text-emerald-400 font-medium">
      <svg className="h-3.5 w-3.5 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2.5}>
        <path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" />
      </svg>
      Check: PASS
    </span>,
  ];
  if (collectTiming) {
    resultLines.push(
      <span key="timing" className="text-slate-500 text-[11px]">timing breakdown: on</span>
    );
  }

  const requestConfigured = activeHeaders.length > 0 || hasBody;
  const validationConfigured = statusRules.trim() !== '' || assertionTotal > 0 || maxLatencyMs.trim() !== '';
  const tlsConfigured = tlsSkipVerify || !!tlsMinDaysValid || !!tlsServerName || !followRedirects;

  return (
    <div className="flex flex-col select-none">
      <p className="mb-3 text-[10px] font-semibold uppercase tracking-widest text-slate-600">Live Preview</p>

      {/* cURL preview */}
      <CurlPreview method={method} url={url} headers={headers} body={body} />

      {/* Flow diagram */}
      <div className="flex flex-col items-stretch gap-0">

        {/* MONITOR node */}
        <FlowNode
          label="Monitor"
          lines={monitorLines}
          onClick={() => onScrollToSection('monitor-name')}
          accentColor="border-cyan-500/20 shadow-[0_0_20px_rgba(6,182,212,0.08)]"
          icon={
            <svg className="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
              <circle cx="12" cy="12" r="3" /><path d="M12 1v4M12 19v4M4.22 4.22l2.83 2.83M16.95 16.95l2.83 2.83M1 12h4M19 12h4M4.22 19.78l2.83-2.83M16.95 7.05l2.83-2.83" />
            </svg>
          }
        />

        {/* Arrow: MONITOR → REQUEST */}
        <FlowArrow
          methodColor={methodStyle.text}
          label={
            <span className={`rounded px-1.5 py-0.5 text-[9px] font-bold ${methodStyle.bg} ${methodStyle.text} ${methodStyle.border} border`}>
              {method || 'GET'}
            </span>
          }
        />

        {/* REQUEST node */}
        <FlowNode
          label="Request"
          lines={requestLines}
          configured={requestConfigured}
          onClick={() => onScrollToSection('section-request')}
          accentColor="border-white/[0.08]"
          icon={
            <svg className="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M7 11l5-5m0 0l5 5m-5-5v12" />
            </svg>
          }
        />

        {/* Arrow: REQUEST → SERVER */}
        <FlowArrow methodColor={methodStyle.text} />

        {/* SERVER node */}
        <FlowNode
          label="Server"
          lines={serverLines}
          configured={tlsConfigured}
          onClick={() => onScrollToSection('section-tls')}
          accentColor="border-white/[0.08]"
          icon={
            <svg className="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
              <rect x="2" y="2" width="20" height="8" rx="2" /><rect x="2" y="14" width="20" height="8" rx="2" />
              <line x1="6" y1="6" x2="6.01" y2="6" strokeWidth={3} strokeLinecap="round" />
              <line x1="6" y1="18" x2="6.01" y2="18" strokeWidth={3} strokeLinecap="round" />
            </svg>
          }
        />

        {/* Arrow: SERVER → RESPONSE */}
        <FlowArrow methodColor="text-emerald-500" direction="down" />

        {/* RESPONSE node */}
        <FlowNode
          label="Response"
          lines={responseLines}
          configured={validationConfigured}
          onClick={() => onScrollToSection('section-validation')}
          accentColor="border-white/[0.08]"
          icon={
            <svg className="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M17 13l-5 5m0 0l-5-5m5 5V6" />
            </svg>
          }
        />

        {/* Arrow: RESPONSE → RESULT */}
        <FlowArrow methodColor="text-emerald-500" direction="down" />

        {/* RESULT node */}
        <FlowNode
          label="Result"
          lines={resultLines}
          accentColor="border-emerald-500/20 bg-emerald-500/[0.03]"
          icon={
            <svg className="h-3 w-3 text-emerald-500" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>
          }
        />
      </div>

      <p className="mt-3 text-center text-[10px] text-slate-600">
        Click any node to jump to that section
      </p>
    </div>
  );
}
