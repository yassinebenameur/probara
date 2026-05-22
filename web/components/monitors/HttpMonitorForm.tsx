'use client';

import { Plus, X } from 'lucide-react';
import CollapsibleSection from '@/components/ui/CollapsibleSection';
import Button from '@/components/ui/Button';
import {
  HTTPBodyAssertion,
  HTTPBodyAssertionOp,
  HTTPHeaderAssertion,
  HTTPHeaderAssertionOp,
  HTTPJSONAssertion,
  HTTPJSONAssertionOp,
} from '@/lib/types';

type HeaderKV = { key: string; value: string; reveal?: boolean };

export interface HttpFormData {
  url: string;
  method: string;
  status_rules: string;
  request_headers: HeaderKV[];
  body: string;
  body_assertions: HTTPBodyAssertion[];
  response_header_assertions: HTTPHeaderAssertion[];
  json_assertions: HTTPJSONAssertion[];
  max_latency_ms: string;
  follow_redirects: boolean;
  max_redirects: number;
  tls_skip_verify: boolean;
  tls_min_days_valid: string;
  tls_server_name: string;
  tls_ca_pem: string;
  collect_timing: boolean;
}

export const SECTION_IDS = {
  request: 'section-request',
  validation: 'section-validation',
  tls: 'section-tls',
} as const;

interface HttpMonitorFormProps {
  data: HttpFormData;
  onChange: (patch: Partial<HttpFormData>) => void;
  errors: Record<string, string>;
  openSections?: Record<string, boolean>;
  onToggleSection?: (id: string, open: boolean) => void;
}

const isSensitiveHeaderName = (name: string) => {
  const n = name.trim().toLowerCase();
  if (!n) return false;
  return (
    n === 'authorization' ||
    n === 'cookie' ||
    n === 'x-api-key' ||
    n === 'api-key' ||
    n === 'x-auth-token' ||
    n.includes('token') ||
    n.includes('secret')
  );
};

function MethodUrlRow({
  method,
  url,
  onMethodChange,
  onUrlChange,
  error,
}: {
  method: string;
  url: string;
  onMethodChange: (v: string) => void;
  onUrlChange: (v: string) => void;
  error?: string;
}) {
  const methodColors: Record<string, string> = {
    GET: 'text-cyan-400',
    HEAD: 'text-cyan-400',
    POST: 'text-emerald-400',
    PUT: 'text-amber-400',
    PATCH: 'text-violet-400',
    DELETE: 'text-rose-400',
    OPTIONS: 'text-slate-400',
  };
  const color = methodColors[method] || 'text-slate-400';

  return (
    <div>
      <label className="block text-xs font-medium text-slate-400 mb-1.5">URL</label>
      <div className={`flex rounded-xl border transition-all ${error ? 'border-rose-500/60' : 'border-white/[0.1] focus-within:border-cyan-500 focus-within:shadow-[0_0_0_3px_rgba(6,182,212,0.15)]'} bg-slate-900/80`}>
        <select
          value={method}
          onChange={(e) => onMethodChange(e.target.value)}
          className={`shrink-0 bg-transparent border-r border-white/[0.08] px-3 py-2.5 text-sm font-semibold focus:outline-none rounded-l-xl ${color}`}
        >
          {['GET', 'HEAD', 'POST', 'PUT', 'PATCH', 'DELETE', 'OPTIONS'].map((m) => (
            <option key={m} value={m} className="bg-slate-900 text-white">
              {m}
            </option>
          ))}
        </select>
        <input
          type="url"
          value={url}
          onChange={(e) => onUrlChange(e.target.value)}
          placeholder="https://api.example.com/health"
          className="flex-1 min-w-0 bg-transparent px-3 py-2.5 text-sm text-white placeholder:text-slate-600 focus:outline-none rounded-r-xl"
        />
      </div>
      {error && <p className="mt-1.5 text-xs text-rose-400">{error}</p>}
    </div>
  );
}

function HeadersEditor({
  headers,
  onChange,
  error,
}: {
  headers: HeaderKV[];
  onChange: (headers: HeaderKV[]) => void;
  error?: string;
}) {
  return (
    <div>
      <label className="block text-xs font-medium text-slate-400 mb-2">Request Headers</label>
      {error && <p className="mb-2 text-xs text-rose-400">{error}</p>}
      <div className="space-y-2">
        {headers.map((h, idx) => {
          const sensitive = isSensitiveHeaderName(h.key);
          const showToggle = sensitive && h.value.trim().length > 0;
          return (
            <div
              key={idx}
              className="grid gap-2 items-center"
              style={{ gridTemplateColumns: showToggle ? '1fr 2fr auto auto' : '1fr 2fr auto' }}
            >
              <input
                className="input text-sm"
                placeholder="Header name"
                value={h.key}
                onChange={(e) => {
                  const next = [...headers];
                  next[idx] = { ...next[idx], key: e.target.value };
                  onChange(next);
                }}
              />
              <input
                className="input text-sm"
                placeholder="Value"
                type={sensitive && !h.reveal ? 'password' : 'text'}
                value={h.value}
                onChange={(e) => {
                  const next = [...headers];
                  next[idx] = { ...next[idx], value: e.target.value };
                  onChange(next);
                }}
              />
              {showToggle && (
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  onClick={() => {
                    const next = [...headers];
                    next[idx] = { ...next[idx], reveal: !next[idx].reveal };
                    onChange(next);
                  }}
                >
                  {h.reveal ? 'Hide' : 'Show'}
                </Button>
              )}
              <Button
                type="button"
                variant="subtle"
                size="sm"
                aria-label="Remove header"
                icon={<X strokeWidth={1.75} />}
                onClick={() => {
                  const next = [...headers];
                  if (next.length === 1) {
                    next[0] = { key: '', value: '', reveal: false };
                  } else {
                    next.splice(idx, 1);
                  }
                  onChange(next);
                }}
              >
                <span className="sr-only">Remove</span>
              </Button>
            </div>
          );
        })}
      </div>
      <div className="mt-2">
        <Button
          type="button"
          variant="ghost"
          size="xs"
          icon={<Plus strokeWidth={1.75} />}
          onClick={() => onChange([...headers, { key: '', value: '', reveal: false }])}
        >
          Add header
        </Button>
      </div>
      <p className="mt-1.5 text-xs text-slate-600">Authorization / API key headers are masked by default.</p>
    </div>
  );
}

export default function HttpMonitorForm({ data, onChange, errors, openSections, onToggleSection }: HttpMonitorFormProps) {
  const activeHeaderCount = data.request_headers.filter((h) => h.key.trim()).length;
  const assertionCount =
    (data.body_assertions?.length || 0) +
    (data.response_header_assertions?.length || 0) +
    (data.json_assertions?.length || 0);

  const requestSummary = [
    activeHeaderCount > 0 ? `${activeHeaderCount} header${activeHeaderCount > 1 ? 's' : ''}` : 'no headers',
    data.body.trim() ? 'with body' : 'no body',
  ].join(', ');

  const validationSummary = [
    data.status_rules.trim() || 'Default (2xx)',
    assertionCount > 0 ? `${assertionCount} assertion${assertionCount > 1 ? 's' : ''}` : '',
    data.max_latency_ms.trim() ? `<${data.max_latency_ms}ms` : '',
  ]
    .filter(Boolean)
    .join(' · ');

  const tlsSummary = [
    data.follow_redirects ? 'Follow redirects' : 'No redirects',
    data.tls_skip_verify ? 'skip TLS verify' : 'verify TLS',
    data.collect_timing ? 'collect timing' : '',
  ]
    .filter(Boolean)
    .join(', ');

  return (
    <div className="space-y-3">
      {/* Request Settings */}
      <CollapsibleSection
        title="Request"
        summary={requestSummary}
        sectionId={SECTION_IDS.request}
        isOpen={openSections?.[SECTION_IDS.request]}
        onToggle={(open) => onToggleSection?.(SECTION_IDS.request, open)}
      >
        <HeadersEditor
          headers={data.request_headers}
          onChange={(h) => onChange({ request_headers: h })}
          error={errors.request_headers}
        />

        <div>
          <label className="block text-xs font-medium text-slate-400 mb-1.5">
            Request Body <span className="text-slate-600">(optional)</span>
          </label>
          <textarea
            rows={4}
            value={data.body}
            onChange={(e) => onChange({ body: e.target.value })}
            placeholder={'{"status":"ok"}'}
            className="input min-h-[80px] text-sm font-mono"
          />
          <p className="mt-1.5 text-xs text-slate-600">
            Sent as-is for POST/PUT/PATCH. Set <code className="text-slate-400">Content-Type</code> in headers if needed.
          </p>
        </div>
      </CollapsibleSection>

      {/* Response Validation */}
      <CollapsibleSection
        title="Response Validation"
        summary={validationSummary}
        sectionId={SECTION_IDS.validation}
        isOpen={openSections?.[SECTION_IDS.validation]}
        onToggle={(open) => onToggleSection?.(SECTION_IDS.validation, open)}
      >
        <div>
          <label className="block text-xs font-medium text-slate-400 mb-1.5">
            Expected Status Rules <span className="text-slate-600">(optional)</span>
          </label>
          <input
            type="text"
            value={data.status_rules}
            onChange={(e) => onChange({ status_rules: e.target.value })}
            placeholder="2xx, 304, 200-299"
            className="input text-sm"
          />
          {errors.status_rules ? (
            <p className="mt-1.5 text-xs text-rose-400">{errors.status_rules}</p>
          ) : (
            <p className="mt-1.5 text-xs text-slate-600">Leave empty to accept any 2xx response. Supports codes, ranges, and classes.</p>
          )}
        </div>

        {/* Body Assertions */}
        <div>
          <label className="block text-xs font-medium text-slate-400 mb-2">Body Assertions</label>
          <div className="space-y-2">
            {(data.body_assertions || []).map((a, idx) => (
              <div
                key={`body-${idx}`}
                className="grid gap-2 items-center"
                style={{ gridTemplateColumns: '140px 1fr auto auto' }}
              >
                <select
                  className="input text-sm"
                  value={a.op}
                  onChange={(e) => {
                    const next = [...data.body_assertions];
                    next[idx] = { ...next[idx], op: e.target.value as HTTPBodyAssertionOp };
                    onChange({ body_assertions: next });
                  }}
                >
                  <option value="contains">contains</option>
                  <option value="not_contains">not contains</option>
                  <option value="regex">regex</option>
                  <option value="not_regex">not regex</option>
                </select>
                <input
                  className="input text-sm"
                  placeholder="Value / pattern"
                  value={a.value}
                  onChange={(e) => {
                    const next = [...data.body_assertions];
                    next[idx] = { ...next[idx], value: e.target.value };
                    onChange({ body_assertions: next });
                  }}
                />
                <label className="flex items-center gap-1 text-xs text-slate-500 whitespace-nowrap">
                  <input
                    type="checkbox"
                    checked={!!a.case_insensitive}
                    onChange={(e) => {
                      const next = [...data.body_assertions];
                      next[idx] = { ...next[idx], case_insensitive: e.target.checked };
                      onChange({ body_assertions: next });
                    }}
                  />
                  CI
                </label>
                <Button
                  type="button"
                  variant="subtle"
                  size="sm"
                  aria-label="Remove body assertion"
                  icon={<X strokeWidth={1.75} />}
                  onClick={() => {
                    const next = [...data.body_assertions];
                    next.splice(idx, 1);
                    onChange({ body_assertions: next });
                  }}
                >
                  <span className="sr-only">Remove</span>
                </Button>
              </div>
            ))}
          </div>
          <div className="mt-2">
            <Button
              type="button"
              variant="ghost"
              size="xs"
              icon={<Plus strokeWidth={1.75} />}
              onClick={() =>
                onChange({
                  body_assertions: [
                    ...(data.body_assertions || []),
                    { op: 'contains', value: '', case_insensitive: false },
                  ],
                })
              }
            >
              Add body assertion
            </Button>
          </div>
        </div>

        {/* Response Header Assertions */}
        <div>
          <label className="block text-xs font-medium text-slate-400 mb-2">Response Header Assertions</label>
          <div className="space-y-2">
            {(data.response_header_assertions || []).map((a, idx) => {
              const needsValue = a.op !== 'exists';
              return (
                <div
                  key={`hdr-${idx}`}
                  className="grid gap-2 items-center"
                  style={{ gridTemplateColumns: '1fr 140px 1fr auto auto' }}
                >
                  <input
                    className="input text-sm"
                    placeholder="Header name"
                    value={a.name}
                    onChange={(e) => {
                      const next = [...data.response_header_assertions];
                      next[idx] = { ...next[idx], name: e.target.value };
                      onChange({ response_header_assertions: next });
                    }}
                  />
                  <select
                    className="input text-sm"
                    value={a.op}
                    onChange={(e) => {
                      const next = [...data.response_header_assertions];
                      next[idx] = { ...next[idx], op: e.target.value as HTTPHeaderAssertionOp };
                      onChange({ response_header_assertions: next });
                    }}
                  >
                    <option value="exists">exists</option>
                    <option value="equals">equals</option>
                    <option value="contains">contains</option>
                    <option value="regex">regex</option>
                    <option value="not_equals">not equals</option>
                    <option value="not_contains">not contains</option>
                    <option value="not_regex">not regex</option>
                  </select>
                  <input
                    className="input text-sm"
                    placeholder={needsValue ? 'Value / pattern' : '—'}
                    disabled={!needsValue}
                    value={a.value || ''}
                    onChange={(e) => {
                      const next = [...data.response_header_assertions];
                      next[idx] = { ...next[idx], value: e.target.value };
                      onChange({ response_header_assertions: next });
                    }}
                  />
                  <label className="flex items-center gap-1 text-xs text-slate-500 whitespace-nowrap">
                    <input
                      type="checkbox"
                      checked={!!a.case_insensitive}
                      onChange={(e) => {
                        const next = [...data.response_header_assertions];
                        next[idx] = { ...next[idx], case_insensitive: e.target.checked };
                        onChange({ response_header_assertions: next });
                      }}
                    />
                    CI
                  </label>
                  <Button
                    type="button"
                    variant="subtle"
                    size="sm"
                    aria-label="Remove header assertion"
                    icon={<X strokeWidth={1.75} />}
                    onClick={() => {
                      const next = [...data.response_header_assertions];
                      next.splice(idx, 1);
                      onChange({ response_header_assertions: next });
                    }}
                  >
                    <span className="sr-only">Remove</span>
                  </Button>
                </div>
              );
            })}
          </div>
          <div className="mt-2">
            <Button
              type="button"
              variant="ghost"
              size="xs"
              icon={<Plus strokeWidth={1.75} />}
              onClick={() =>
                onChange({
                  response_header_assertions: [
                    ...(data.response_header_assertions || []),
                    { name: '', op: 'exists', value: '', case_insensitive: false },
                  ],
                })
              }
            >
              Add header assertion
            </Button>
          </div>
        </div>

        {/* JSON Assertions */}
        <div>
          <label className="block text-xs font-medium text-slate-400 mb-2">JSON Assertions <span className="text-slate-600">(gjson path)</span></label>
          <div className="space-y-2">
            {(data.json_assertions || []).map((a, idx) => {
              const needsValue = a.op !== 'exists';
              return (
                <div
                  key={`json-${idx}`}
                  className="grid gap-2 items-center"
                  style={{ gridTemplateColumns: '1fr 140px 1fr auto auto' }}
                >
                  <input
                    className="input text-sm font-mono"
                    placeholder="Path (e.g. data.status)"
                    value={a.path}
                    onChange={(e) => {
                      const next = [...data.json_assertions];
                      next[idx] = { ...next[idx], path: e.target.value };
                      onChange({ json_assertions: next });
                    }}
                  />
                  <select
                    className="input text-sm"
                    value={a.op}
                    onChange={(e) => {
                      const next = [...data.json_assertions];
                      next[idx] = { ...next[idx], op: e.target.value as HTTPJSONAssertionOp };
                      onChange({ json_assertions: next });
                    }}
                  >
                    <option value="exists">exists</option>
                    <option value="equals">equals</option>
                    <option value="not_equals">not equals</option>
                    <option value="contains">contains</option>
                    <option value="not_contains">not contains</option>
                    <option value="regex">regex</option>
                    <option value="number_gt">number &gt;</option>
                    <option value="number_gte">number ≥</option>
                    <option value="number_lt">number &lt;</option>
                    <option value="number_lte">number ≤</option>
                    <option value="bool_is">bool is</option>
                  </select>
                  <input
                    className="input text-sm"
                    placeholder={needsValue ? 'Value / pattern' : '—'}
                    disabled={!needsValue}
                    value={a.value || ''}
                    onChange={(e) => {
                      const next = [...data.json_assertions];
                      next[idx] = { ...next[idx], value: e.target.value };
                      onChange({ json_assertions: next });
                    }}
                  />
                  <label className="flex items-center gap-1 text-xs text-slate-500 whitespace-nowrap">
                    <input
                      type="checkbox"
                      checked={!!a.case_insensitive}
                      onChange={(e) => {
                        const next = [...data.json_assertions];
                        next[idx] = { ...next[idx], case_insensitive: e.target.checked };
                        onChange({ json_assertions: next });
                      }}
                    />
                    CI
                  </label>
                  <Button
                    type="button"
                    variant="subtle"
                    size="sm"
                    aria-label="Remove JSON assertion"
                    icon={<X strokeWidth={1.75} />}
                    onClick={() => {
                      const next = [...data.json_assertions];
                      next.splice(idx, 1);
                      onChange({ json_assertions: next });
                    }}
                  >
                    <span className="sr-only">Remove</span>
                  </Button>
                </div>
              );
            })}
          </div>
          <div className="mt-2">
            <Button
              type="button"
              variant="ghost"
              size="xs"
              icon={<Plus strokeWidth={1.75} />}
              onClick={() =>
                onChange({
                  json_assertions: [
                    ...(data.json_assertions || []),
                    { path: '', op: 'exists', value: '', case_insensitive: false },
                  ],
                })
              }
            >
              Add JSON assertion
            </Button>
          </div>
          <p className="mt-1.5 text-xs text-slate-600">
            Uses gjson syntax — e.g. <code className="text-slate-400">data.items.#.id</code>
          </p>
        </div>

        <div>
          <label className="block text-xs font-medium text-slate-400 mb-1.5">Max Latency (ms) <span className="text-slate-600">(optional)</span></label>
          <input
            type="number"
            value={data.max_latency_ms}
            onChange={(e) => onChange({ max_latency_ms: e.target.value })}
            placeholder="500"
            min={1}
            className="input text-sm w-40"
          />
          {errors.max_latency_ms ? (
            <p className="mt-1.5 text-xs text-rose-400">{errors.max_latency_ms}</p>
          ) : (
            <p className="mt-1.5 text-xs text-slate-600">Fail the check if total latency exceeds this threshold.</p>
          )}
        </div>
      </CollapsibleSection>

      {/* TLS & Redirects */}
      <CollapsibleSection
        title="TLS & Redirects"
        summary={tlsSummary}
        sectionId={SECTION_IDS.tls}
        isOpen={openSections?.[SECTION_IDS.tls]}
        onToggle={(open) => onToggleSection?.(SECTION_IDS.tls, open)}
      >
        <div className="space-y-3">
          <div className="flex items-center justify-between rounded-lg border border-white/[0.07] bg-slate-800/30 px-4 py-3">
            <div>
              <p className="text-sm font-medium text-white">Follow redirects</p>
              <p className="text-xs text-slate-500">Disable to validate 3xx responses directly</p>
            </div>
            <button
              type="button"
              onClick={() => onChange({ follow_redirects: !data.follow_redirects })}
              className={`relative h-5 w-9 rounded-full transition-colors ${data.follow_redirects ? 'bg-cyan-500' : 'bg-slate-700'}`}
            >
              <span className={`absolute top-0.5 left-0.5 h-4 w-4 rounded-full bg-white transition-transform ${data.follow_redirects ? 'translate-x-4' : ''}`} />
            </button>
          </div>

          {data.follow_redirects && (
            <div>
              <label className="block text-xs font-medium text-slate-400 mb-1.5">Max Redirects</label>
              <input
                type="number"
                value={data.max_redirects}
                onChange={(e) => onChange({ max_redirects: parseInt(e.target.value) || 0 })}
                min={0}
                className="input text-sm w-28"
              />
              {errors.max_redirects && <p className="mt-1.5 text-xs text-rose-400">{errors.max_redirects}</p>}
            </div>
          )}

          <div className="flex items-center justify-between rounded-lg border border-white/[0.07] bg-slate-800/30 px-4 py-3">
            <div>
              <p className="text-sm font-medium text-white">Skip TLS verification</p>
              <p className="text-xs text-slate-500">For self-signed certs — not recommended for public endpoints</p>
            </div>
            <button
              type="button"
              onClick={() => onChange({ tls_skip_verify: !data.tls_skip_verify })}
              className={`relative h-5 w-9 rounded-full transition-colors ${data.tls_skip_verify ? 'bg-amber-500' : 'bg-slate-700'}`}
            >
              <span className={`absolute top-0.5 left-0.5 h-4 w-4 rounded-full bg-white transition-transform ${data.tls_skip_verify ? 'translate-x-4' : ''}`} />
            </button>
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="block text-xs font-medium text-slate-400 mb-1.5">TLS Min Days Valid</label>
              <input
                type="number"
                value={data.tls_min_days_valid}
                onChange={(e) => onChange({ tls_min_days_valid: e.target.value })}
                placeholder="14"
                min={0}
                className="input text-sm"
              />
              {errors.tls_min_days_valid ? (
                <p className="mt-1.5 text-xs text-rose-400">{errors.tls_min_days_valid}</p>
              ) : (
                <p className="mt-1.5 text-xs text-slate-600">Alert before cert expires</p>
              )}
            </div>
            <div>
              <label className="block text-xs font-medium text-slate-400 mb-1.5">TLS Server Name (SNI)</label>
              <input
                type="text"
                value={data.tls_server_name}
                onChange={(e) => onChange({ tls_server_name: e.target.value })}
                placeholder="api.example.com"
                className="input text-sm"
              />
              <p className="mt-1.5 text-xs text-slate-600">Override hostname verification</p>
            </div>
          </div>

          <div>
            <label className="block text-xs font-medium text-slate-400 mb-1.5">Custom CA Bundle (PEM)</label>
            <textarea
              rows={4}
              value={data.tls_ca_pem}
              onChange={(e) => onChange({ tls_ca_pem: e.target.value })}
              placeholder={'-----BEGIN CERTIFICATE-----\n...\n-----END CERTIFICATE-----'}
              className="input text-xs font-mono min-h-[80px]"
            />
            <p className="mt-1.5 text-xs text-slate-600">Add trusted root CAs for this monitor</p>
          </div>

          <div className="flex items-center justify-between rounded-lg border border-white/[0.07] bg-slate-800/30 px-4 py-3">
            <div>
              <p className="text-sm font-medium text-white">Collect timing breakdown</p>
              <p className="text-xs text-slate-500">Store DNS / connect / TLS / TTFB in check results</p>
            </div>
            <button
              type="button"
              onClick={() => onChange({ collect_timing: !data.collect_timing })}
              className={`relative h-5 w-9 rounded-full transition-colors ${data.collect_timing ? 'bg-cyan-500' : 'bg-slate-700'}`}
            >
              <span className={`absolute top-0.5 left-0.5 h-4 w-4 rounded-full bg-white transition-transform ${data.collect_timing ? 'translate-x-4' : ''}`} />
            </button>
          </div>
        </div>
      </CollapsibleSection>
    </div>
  );
}

export { MethodUrlRow };
