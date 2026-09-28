'use client';

import { ReactNode } from 'react';
import { Plus, X } from 'lucide-react';
import CollapsibleSection from '@/components/ui/CollapsibleSection';
import Button from '@/components/ui/Button';
import FormField from '@/components/ui/FormField';
import IconButton from '@/components/ui/IconButton';
import {
  HTTPBodyAssertion,
  HTTPBodyAssertionOp,
  HTTPHeaderAssertion,
  HTTPHeaderAssertionOp,
  HTTPJSONAssertion,
  HTTPJSONAssertionOp,
} from '@/lib/types';
import Switch from '@/components/ui/Switch';

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
  return (
    <FormField label="URL" error={error}>
      <div
        className={`flex rounded-[10px] border bg-control transition-colors ${
          error
            ? 'border-rose-500/60'
            : 'border-edge-control focus-within:border-cyan-500 focus-within:shadow-[0_0_0_3px_rgba(var(--accent-rgb),0.16)]'
        }`}
      >
        <select
          value={method}
          onChange={(e) => onMethodChange(e.target.value)}
          className="shrink-0 rounded-l-[10px] border-r border-edge-control bg-transparent px-3 py-[9px] text-sm font-semibold text-white focus:outline-none"
        >
          {['GET', 'HEAD', 'POST', 'PUT', 'PATCH', 'DELETE', 'OPTIONS'].map((m) => (
            <option key={m} value={m}>
              {m}
            </option>
          ))}
        </select>
        <input
          type="url"
          value={url}
          onChange={(e) => onUrlChange(e.target.value)}
          placeholder="https://api.example.com/health"
          className="min-w-0 flex-1 rounded-r-[10px] bg-transparent px-3 py-[9px] text-sm text-white placeholder:text-slate-500 focus:outline-none"
        />
      </div>
    </FormField>
  );
}

function SwitchRow({
  title,
  description,
  checked,
  onToggle,
  onClass = 'bg-cyan-500',
}: {
  title: string;
  description: string;
  checked: boolean;
  onToggle: () => void;
  onClass?: string;
}) {
  return (
    <div className="flex items-center justify-between gap-4">
      <div className="min-w-0">
        <p className="text-sm font-medium text-slate-200">{title}</p>
        <p className="text-xs text-slate-500">{description}</p>
      </div>
      <Switch checked={checked} onChange={onToggle} tone={onClass === 'bg-amber-500' ? 'warning' : 'accent'} />
    </div>
  );
}

function Hint({ children }: { children: ReactNode }) {
  return <p className="mt-1 text-xs text-slate-500">{children}</p>;
}

function CaseToggle({ checked, onChange }: { checked: boolean; onChange: (checked: boolean) => void }) {
  return (
    <label className="flex items-center gap-1 whitespace-nowrap text-xs text-slate-400">
      <input
        type="checkbox"
        className="accent-cyan-500"
        checked={checked}
        onChange={(e) => onChange(e.target.checked)}
      />
      CI
    </label>
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
    <FormField label="Request headers" error={error}>
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
                className="input"
                placeholder="Header name"
                value={h.key}
                onChange={(e) => {
                  const next = [...headers];
                  next[idx] = { ...next[idx], key: e.target.value };
                  onChange(next);
                }}
              />
              <input
                className="input"
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
                  variant="subtle"
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
              <IconButton
                label="Remove header"
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
              />
            </div>
          );
        })}
      </div>
      <div className="mt-2 flex flex-wrap items-center gap-3">
        <Button
          type="button"
          variant="ghost"
          size="xs"
          icon={<Plus className="h-3.5 w-3.5" strokeWidth={1.75} />}
          onClick={() => onChange([...headers, { key: '', value: '', reveal: false }])}
        >
          Add header
        </Button>
        <p className="text-xs text-slate-500">Authorization / API key headers are masked by default.</p>
      </div>
    </FormField>
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
    <div className="space-y-5">
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

        <FormField label="Request body (optional)">
          <textarea
            rows={4}
            value={data.body}
            onChange={(e) => onChange({ body: e.target.value })}
            placeholder={'{"status":"ok"}'}
            className="input min-h-[80px] font-mono"
          />
          <Hint>
            Sent as-is for POST/PUT/PATCH. Set <code className="text-slate-300">Content-Type</code> in headers if needed.
          </Hint>
        </FormField>
      </CollapsibleSection>

      <CollapsibleSection
        title="Response validation"
        summary={validationSummary}
        sectionId={SECTION_IDS.validation}
        isOpen={openSections?.[SECTION_IDS.validation]}
        onToggle={(open) => onToggleSection?.(SECTION_IDS.validation, open)}
      >
        <FormField label="Expected status rules (optional)" error={errors.status_rules}>
          <input
            type="text"
            value={data.status_rules}
            onChange={(e) => onChange({ status_rules: e.target.value })}
            placeholder="2xx, 304, 200-299"
            className="input"
          />
          {!errors.status_rules && (
            <Hint>Leave empty to accept any 2xx response. Supports codes, ranges, and classes.</Hint>
          )}
        </FormField>

        <FormField label="Body assertions">
          <div className="space-y-2">
            {(data.body_assertions || []).map((a, idx) => (
              <div
                key={`body-${idx}`}
                className="grid gap-2 items-center"
                style={{ gridTemplateColumns: '140px 1fr auto auto' }}
              >
                <select
                  className="input"
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
                  className="input"
                  placeholder="Value / pattern"
                  value={a.value}
                  onChange={(e) => {
                    const next = [...data.body_assertions];
                    next[idx] = { ...next[idx], value: e.target.value };
                    onChange({ body_assertions: next });
                  }}
                />
                <CaseToggle
                  checked={!!a.case_insensitive}
                  onChange={(checked) => {
                    const next = [...data.body_assertions];
                    next[idx] = { ...next[idx], case_insensitive: checked };
                    onChange({ body_assertions: next });
                  }}
                />
                <IconButton
                  label="Remove body assertion"
                  icon={<X strokeWidth={1.75} />}
                  onClick={() => {
                    const next = [...data.body_assertions];
                    next.splice(idx, 1);
                    onChange({ body_assertions: next });
                  }}
                />
              </div>
            ))}
          </div>
          <div className="mt-2">
            <Button
              type="button"
              variant="ghost"
              size="xs"
              icon={<Plus className="h-3.5 w-3.5" strokeWidth={1.75} />}
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
        </FormField>

        <FormField label="Response header assertions">
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
                    className="input"
                    placeholder="Header name"
                    value={a.name}
                    onChange={(e) => {
                      const next = [...data.response_header_assertions];
                      next[idx] = { ...next[idx], name: e.target.value };
                      onChange({ response_header_assertions: next });
                    }}
                  />
                  <select
                    className="input"
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
                    className="input"
                    placeholder={needsValue ? 'Value / pattern' : '—'}
                    disabled={!needsValue}
                    value={a.value || ''}
                    onChange={(e) => {
                      const next = [...data.response_header_assertions];
                      next[idx] = { ...next[idx], value: e.target.value };
                      onChange({ response_header_assertions: next });
                    }}
                  />
                  <CaseToggle
                    checked={!!a.case_insensitive}
                    onChange={(checked) => {
                      const next = [...data.response_header_assertions];
                      next[idx] = { ...next[idx], case_insensitive: checked };
                      onChange({ response_header_assertions: next });
                    }}
                  />
                  <IconButton
                    label="Remove header assertion"
                    icon={<X strokeWidth={1.75} />}
                    onClick={() => {
                      const next = [...data.response_header_assertions];
                      next.splice(idx, 1);
                      onChange({ response_header_assertions: next });
                    }}
                  />
                </div>
              );
            })}
          </div>
          <div className="mt-2">
            <Button
              type="button"
              variant="ghost"
              size="xs"
              icon={<Plus className="h-3.5 w-3.5" strokeWidth={1.75} />}
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
        </FormField>

        <FormField label="JSON assertions (gjson path)">
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
                    className="input font-mono"
                    placeholder="Path (e.g. data.status)"
                    value={a.path}
                    onChange={(e) => {
                      const next = [...data.json_assertions];
                      next[idx] = { ...next[idx], path: e.target.value };
                      onChange({ json_assertions: next });
                    }}
                  />
                  <select
                    className="input"
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
                    className="input"
                    placeholder={needsValue ? 'Value / pattern' : '—'}
                    disabled={!needsValue}
                    value={a.value || ''}
                    onChange={(e) => {
                      const next = [...data.json_assertions];
                      next[idx] = { ...next[idx], value: e.target.value };
                      onChange({ json_assertions: next });
                    }}
                  />
                  <CaseToggle
                    checked={!!a.case_insensitive}
                    onChange={(checked) => {
                      const next = [...data.json_assertions];
                      next[idx] = { ...next[idx], case_insensitive: checked };
                      onChange({ json_assertions: next });
                    }}
                  />
                  <IconButton
                    label="Remove JSON assertion"
                    icon={<X strokeWidth={1.75} />}
                    onClick={() => {
                      const next = [...data.json_assertions];
                      next.splice(idx, 1);
                      onChange({ json_assertions: next });
                    }}
                  />
                </div>
              );
            })}
          </div>
          <div className="mt-2 flex flex-wrap items-center gap-3">
            <Button
              type="button"
              variant="ghost"
              size="xs"
              icon={<Plus className="h-3.5 w-3.5" strokeWidth={1.75} />}
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
            <p className="text-xs text-slate-500">
              Uses gjson syntax — e.g. <code className="text-slate-300">data.items.#.id</code>
            </p>
          </div>
        </FormField>

        <FormField label="Max latency (ms, optional)" error={errors.max_latency_ms}>
          <input
            type="number"
            value={data.max_latency_ms}
            onChange={(e) => onChange({ max_latency_ms: e.target.value })}
            placeholder="500"
            min={1}
            className="input !w-40"
          />
          {!errors.max_latency_ms && <Hint>Fail the check if total latency exceeds this threshold.</Hint>}
        </FormField>
      </CollapsibleSection>

      <CollapsibleSection
        title="TLS & redirects"
        summary={tlsSummary}
        sectionId={SECTION_IDS.tls}
        isOpen={openSections?.[SECTION_IDS.tls]}
        onToggle={(open) => onToggleSection?.(SECTION_IDS.tls, open)}
      >
        <SwitchRow
          title="Follow redirects"
          description="Disable to validate 3xx responses directly"
          checked={data.follow_redirects}
          onToggle={() => onChange({ follow_redirects: !data.follow_redirects })}
        />

        {data.follow_redirects && (
          <FormField label="Max redirects" error={errors.max_redirects}>
            <input
              type="number"
              value={data.max_redirects}
              onChange={(e) => onChange({ max_redirects: parseInt(e.target.value) || 0 })}
              min={0}
              className="input !w-28"
            />
          </FormField>
        )}

        <SwitchRow
          title="Skip TLS verification"
          description="For self-signed certs — not recommended for public endpoints"
          checked={data.tls_skip_verify}
          onToggle={() => onChange({ tls_skip_verify: !data.tls_skip_verify })}
          onClass="bg-amber-500"
        />

        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <FormField label="TLS min days valid" error={errors.tls_min_days_valid}>
            <input
              type="number"
              value={data.tls_min_days_valid}
              onChange={(e) => onChange({ tls_min_days_valid: e.target.value })}
              placeholder="14"
              min={0}
              className="input"
            />
            {!errors.tls_min_days_valid && <Hint>Alert before cert expires</Hint>}
          </FormField>
          <FormField label="TLS server name (SNI)">
            <input
              type="text"
              value={data.tls_server_name}
              onChange={(e) => onChange({ tls_server_name: e.target.value })}
              placeholder="api.example.com"
              className="input"
            />
            <Hint>Override hostname verification</Hint>
          </FormField>
        </div>

        <FormField label="Custom CA bundle (PEM)">
          <textarea
            rows={4}
            value={data.tls_ca_pem}
            onChange={(e) => onChange({ tls_ca_pem: e.target.value })}
            placeholder={'-----BEGIN CERTIFICATE-----\n...\n-----END CERTIFICATE-----'}
            className="input min-h-[80px] font-mono"
          />
          <Hint>Add trusted root CAs for this monitor</Hint>
        </FormField>

        <SwitchRow
          title="Collect timing breakdown"
          description="Store DNS / connect / TLS / TTFB in check results"
          checked={data.collect_timing}
          onToggle={() => onChange({ collect_timing: !data.collect_timing })}
        />
      </CollapsibleSection>
    </div>
  );
}

export { MethodUrlRow };
