'use client';

import { ReactNode } from 'react';
import { Plus, X } from 'lucide-react';
import CollapsibleSection from '@/components/ui/CollapsibleSection';
import CurlPreview from './CurlPreview';
import { FormPanel, ToggleRow } from './form-kit';
import Button from '@/components/ui/Button';
import Checkbox from '@/components/ui/Checkbox';
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
import { HeaderKV, isSensitiveHeaderName } from '@/lib/monitor-form/http';

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

type SectionId = (typeof SECTION_IDS)[keyof typeof SECTION_IDS];

// Which collapsible row each validated field lives in, so a failed submit can
// open the rows holding errors and mark them while collapsed.
const FIELD_SECTION: Record<string, SectionId> = {
  request_headers: SECTION_IDS.request,
  status_rules: SECTION_IDS.validation,
  max_latency_ms: SECTION_IDS.validation,
  max_redirects: SECTION_IDS.tls,
  tls_min_days_valid: SECTION_IDS.tls,
};

/** The section ids that hold at least one of `errors`, as an open-state patch. */
export function sectionsWithErrors(errors: Record<string, string>): Record<string, boolean> {
  const open: Record<string, boolean> = {};
  for (const [field, message] of Object.entries(errors)) {
    const section = FIELD_SECTION[field];
    if (message && section) open[section] = true;
  }
  return open;
}

interface HttpMonitorFormProps {
  data: HttpFormData;
  onChange: (patch: Partial<HttpFormData>) => void;
  errors: Record<string, string>;
  openSections?: Record<string, boolean>;
  onToggleSection?: (id: string, open: boolean) => void;
}

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
    <FormField label="URL" required error={error}>
      {(control) => (
        <div
          className={`flex rounded-[10px] border bg-control transition-colors ${
            error
              ? 'border-rose-500/60'
              : 'border-[var(--input-edge)] hover:border-slate-400 focus-within:border-[var(--focus)] focus-within:shadow-[0_0_0_1px_var(--focus)]'
          }`}
        >
          <select
            aria-label="HTTP method"
            value={method}
            onChange={(e) => onMethodChange(e.target.value)}
            className="shrink-0 rounded-l-[10px] border-r border-edge-control bg-transparent px-3 py-[9px] text-sm font-semibold text-white focus:outline-none focus-visible:bg-control-hover"
          >
            {['GET', 'HEAD', 'POST', 'PUT', 'PATCH', 'DELETE', 'OPTIONS'].map((m) => (
              <option key={m} value={m}>
                {m}
              </option>
            ))}
          </select>
          <input
            {...control}
            type="url"
            value={url}
            onChange={(e) => onUrlChange(e.target.value)}
            placeholder="https://api.example.com/health"
            className="min-w-0 flex-1 rounded-r-[10px] bg-transparent px-3 py-[9px] font-mono text-sm text-white placeholder:font-sans placeholder:text-slate-500 focus:outline-none"
          />
        </div>
      )}
    </FormField>
  );
}

function Hint({ children }: { children: ReactNode }) {
  return <p className="mt-1 text-xs text-slate-500">{children}</p>;
}

function CaseToggle({ checked, onChange }: { checked: boolean; onChange: (checked: boolean) => void }) {
  return (
    <Checkbox
      size="sm"
      checked={checked}
      onChange={onChange}
      title="Case-insensitive match"
      aria-label="Case-insensitive"
      className="!items-center !gap-1.5 whitespace-nowrap"
      label={<span className="text-xs text-slate-400" aria-hidden="true">CI</span>}
    />
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
                aria-label={`Header ${idx + 1} name`}
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
                aria-label={`Header ${idx + 1} value`}
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
  const errorSections = sectionsWithErrors(errors);

  const requestSummary = [
    activeHeaderCount > 0 ? `${activeHeaderCount} header${activeHeaderCount > 1 ? 's' : ''}` : 'No headers',
    data.body.trim() ? 'with body' : 'no body',
  ].join(', ');

  const validationSummary = [
    data.status_rules.trim() ? `Expects ${data.status_rules.trim()}` : 'Expects 2xx',
    assertionCount > 0 ? `${assertionCount} assertion${assertionCount > 1 ? 's' : ''}` : '',
    data.max_latency_ms.trim() ? `under ${data.max_latency_ms} ms` : '',
  ]
    .filter(Boolean)
    .join(', ');

  // Skipping verification weakens the check, so it gets the warning tone and
  // stays readable in the collapsed header instead of hiding in grey text.
  const tlsSummary = data.tls_skip_verify
    ? 'TLS not verified'
    : [
        data.follow_redirects ? 'Follows redirects' : 'No redirects',
        'verifies TLS',
        data.collect_timing ? 'collects timing' : '',
      ]
        .filter(Boolean)
        .join(', ');

  const rowProps = (id: SectionId, summary: string, tone: 'default' | 'warning' = 'default') => {
    const hasError = Boolean(errorSections[id]);
    return {
      variant: 'row' as const,
      sectionId: id,
      summary: hasError ? 'Fix errors' : summary,
      tone: hasError ? ('danger' as const) : tone,
      isOpen: openSections?.[id] ?? false,
      onToggle: (open: boolean) => onToggleSection?.(id, open),
    };
  };

  return (
    <FormPanel
      title="Check"
      rows={
        <>
          <CollapsibleSection title="Request" {...rowProps(SECTION_IDS.request, requestSummary)}>
            <HeadersEditor
              headers={data.request_headers}
              onChange={(h) => onChange({ request_headers: h })}
              error={errors.request_headers}
            />

            <FormField label="Request body">
              {(control) => (
                <>
                  <textarea
                    {...control}
                    rows={4}
                    value={data.body}
                    onChange={(e) => onChange({ body: e.target.value })}
                    placeholder={'{"status":"ok"}'}
                    className="input min-h-[80px] font-mono"
                  />
                  <Hint>
                    Optional. Sent as-is for POST/PUT/PATCH; set <code className="text-slate-300">Content-Type</code> in the headers if needed.
                  </Hint>
                </>
              )}
            </FormField>

            {/* Only with a real URL: a placeholder host would read as configuration. */}
            {data.url.trim() && (
              <div>
                <p className="mb-1.5 text-ui font-medium text-slate-300">As cURL</p>
                <CurlPreview method={data.method} url={data.url} headers={data.request_headers} body={data.body} />
              </div>
            )}
          </CollapsibleSection>

          <CollapsibleSection title="Response validation" {...rowProps(SECTION_IDS.validation, validationSummary)}>
            <FormField label="Expected status" error={errors.status_rules}>
              {(control) => (
                <>
                  <input
                    {...control}
                    type="text"
                    value={data.status_rules}
                    onChange={(e) => onChange({ status_rules: e.target.value })}
                    placeholder="2xx, 304, 200-299"
                    className="input font-mono"
                  />
                  {!errors.status_rules && (
                    <Hint>Codes, ranges or classes. Leave empty to accept any 2xx response.</Hint>
                  )}
                </>
              )}
            </FormField>

            <FormField label="Body assertions">
              <div className="space-y-2">
                {(data.body_assertions || []).map((a, idx) => (
                  <div
                    key={`body-${idx}`}
                    className="grid grid-cols-[7.5rem_minmax(0,1fr)_auto_auto] items-center gap-2 sm:grid-cols-[140px_minmax(0,1fr)_auto_auto]"
                  >
                    <select
                      className="input"
                      aria-label={`Body assertion ${idx + 1} operator`}
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
                      aria-label={`Body assertion ${idx + 1} value`}
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
                      className="grid grid-cols-[7.5rem_minmax(0,1fr)_auto_auto] items-center gap-2 sm:grid-cols-[minmax(0,1fr)_140px_minmax(0,1fr)_auto_auto]"
                    >
                      <input
                        className="input col-span-full sm:col-span-1"
                        placeholder="Header name"
                        aria-label={`Header assertion ${idx + 1} name`}
                        value={a.name}
                        onChange={(e) => {
                          const next = [...data.response_header_assertions];
                          next[idx] = { ...next[idx], name: e.target.value };
                          onChange({ response_header_assertions: next });
                        }}
                      />
                      <select
                        className="input"
                        aria-label={`Header assertion ${idx + 1} operator`}
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
                        aria-label={`Header assertion ${idx + 1} value`}
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

            <FormField label="JSON assertions">
              <div className="space-y-2">
                {(data.json_assertions || []).map((a, idx) => {
                  const needsValue = a.op !== 'exists';
                  return (
                    <div
                      key={`json-${idx}`}
                      className="grid grid-cols-[7.5rem_minmax(0,1fr)_auto_auto] items-center gap-2 sm:grid-cols-[minmax(0,1fr)_140px_minmax(0,1fr)_auto_auto]"
                    >
                      <input
                        className="input font-mono col-span-full sm:col-span-1"
                        placeholder="Path (e.g. data.status)"
                        aria-label={`JSON assertion ${idx + 1} path`}
                        value={a.path}
                        onChange={(e) => {
                          const next = [...data.json_assertions];
                          next[idx] = { ...next[idx], path: e.target.value };
                          onChange({ json_assertions: next });
                        }}
                      />
                      <select
                        className="input"
                        aria-label={`JSON assertion ${idx + 1} operator`}
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
                        aria-label={`JSON assertion ${idx + 1} value`}
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

            <FormField label="Max latency" error={errors.max_latency_ms}>
              {(control) => (
                <>
                  <span className="flex items-center gap-2">
                    <input
                      {...control}
                      type="number"
                      value={data.max_latency_ms}
                      onChange={(e) => onChange({ max_latency_ms: e.target.value })}
                      placeholder="500"
                      min={1}
                      className="input !w-32 tabular-nums"
                    />
                    <span className="text-xs text-slate-500">ms</span>
                  </span>
                  {!errors.max_latency_ms && <Hint>Optional. Fail the check when the total response time exceeds this.</Hint>}
                </>
              )}
            </FormField>
          </CollapsibleSection>

          <CollapsibleSection
            title="TLS & redirects"
            {...rowProps(SECTION_IDS.tls, tlsSummary, data.tls_skip_verify ? 'warning' : 'default')}
          >
            <ToggleRow
              title="Follow redirects"
              description="Turn off to validate 3xx responses directly"
              checked={data.follow_redirects}
              onChange={(next) => onChange({ follow_redirects: next })}
            />

            {data.follow_redirects && (
              <FormField label="Max redirects" error={errors.max_redirects}>
                <input
                  type="number"
                  value={data.max_redirects}
                  onChange={(e) => onChange({ max_redirects: parseInt(e.target.value) || 0 })}
                  min={0}
                  className="input !w-28 tabular-nums"
                />
              </FormField>
            )}

            <ToggleRow
              title="Skip TLS verification"
              description="For self-signed certificates. Not recommended for public endpoints."
              checked={data.tls_skip_verify}
              onChange={(next) => onChange({ tls_skip_verify: next })}
              tone="warning"
            />

            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <FormField label="Certificate valid for at least" error={errors.tls_min_days_valid}>
                {(control) => (
                  <>
                    <span className="flex items-center gap-2">
                      <input
                        {...control}
                        type="number"
                        value={data.tls_min_days_valid}
                        onChange={(e) => onChange({ tls_min_days_valid: e.target.value })}
                        placeholder="14"
                        min={0}
                        className="input !w-28 tabular-nums"
                      />
                      <span className="text-xs text-slate-500">days</span>
                    </span>
                    {!errors.tls_min_days_valid && <Hint>Open a certificate-expiry alert when fewer days remain; the check itself still passes</Hint>}
                  </>
                )}
              </FormField>
              <FormField label="TLS server name (SNI)">
                {(control) => (
                  <>
                    <input
                      {...control}
                      type="text"
                      value={data.tls_server_name}
                      onChange={(e) => onChange({ tls_server_name: e.target.value })}
                      placeholder="api.example.com"
                      className="input font-mono"
                    />
                    <Hint>Overrides the hostname used for verification</Hint>
                  </>
                )}
              </FormField>
            </div>

            <FormField label="Custom CA bundle (PEM)">
              {(control) => (
                <>
                  <textarea
                    {...control}
                    rows={4}
                    value={data.tls_ca_pem}
                    onChange={(e) => onChange({ tls_ca_pem: e.target.value })}
                    placeholder={'-----BEGIN CERTIFICATE-----\n...\n-----END CERTIFICATE-----'}
                    className="input min-h-[80px] font-mono"
                  />
                  <Hint>Trusted root CAs for this monitor</Hint>
                </>
              )}
            </FormField>

            <ToggleRow
              title="Collect timing breakdown"
              description="Store DNS, connect, TLS and time-to-first-byte in check results"
              checked={data.collect_timing}
              onChange={(next) => onChange({ collect_timing: next })}
            />
          </CollapsibleSection>
        </>
      }
    />
  );
}

export { MethodUrlRow };
