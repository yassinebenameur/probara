'use client';

import type { SyntheticAPIExtractConfig } from '@/lib/types';
import { type HeaderKV, emptyHeaderRow, isSensitiveHeaderName } from '@/lib/monitor-form/common';
import {
  type SyntheticAPIAssertionDraft,
  type SyntheticAPIExtractDraft,
  type SyntheticAPIStepDraft,
  assertionPathRequired,
  assertionValuePlaceholder,
  assertionValueRequired,
  defaultSyntheticAPIAssertion,
  defaultSyntheticAPIExtract,
  defaultSyntheticAssertionForTarget,
  normalizeAssertionTarget,
  syntheticAssertionOpsByTarget,
} from '@/lib/monitor-form/synthetic-api';
import {
  BTN_GHOST_SM,
  FormInput,
  FormSelect,
  FormTextarea,
  FormToggle,
  ReorderRemoveButtons,
  removeKeepingOne,
  updateItem,
} from './MonitorFormControls';

const HTTP_METHODS = ['GET', 'HEAD', 'POST', 'PUT', 'DELETE', 'PATCH', 'OPTIONS'].map((m) => ({ value: m, label: m }));

interface SyntheticApiStepEditorProps {
  step: SyntheticAPIStepDraft;
  index: number;
  count: number;
  advanced: boolean;
  onChange: (step: SyntheticAPIStepDraft) => void;
  onMove: (from: number, to: number) => void;
  onRemove: () => void;
}

/** One request in a synthetic API journey. Headers, tuning, assertions and extracts show in Advanced mode. */
export default function SyntheticApiStepEditor({ step, index, count, advanced, onChange, onMove, onRemove }: SyntheticApiStepEditorProps) {
  const patch = (next: Partial<SyntheticAPIStepDraft>) => onChange({ ...step, ...next });

  return (
    <div className="rounded-lg border border-white/[0.08] bg-slate-800/20 p-4 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <p className="text-xs font-medium text-slate-300">Step {index + 1}</p>
          <p className="text-[11px] text-slate-500">{step.method || 'GET'} {step.url || '(missing URL)'}</p>
        </div>
        <ReorderRemoveButtons index={index} count={count} onMove={onMove} onRemove={onRemove} />
      </div>

      <div className="grid grid-cols-3 gap-3">
        <FormInput label="Step ID" value={step.id} onChange={(id) => patch({ id })} placeholder="health" />
        <FormInput label="Name (optional)" value={step.name} onChange={(name) => patch({ name })} placeholder="Health check" />
        <FormSelect label="Method" value={step.method} onChange={(method) => patch({ method })} options={HTTP_METHODS} />
      </div>

      <FormInput
        label="URL"
        value={step.url}
        onChange={(url) => patch({ url })}
        placeholder="/health or https://service.example.com/health"
      />

      {advanced && <StepHeadersEditor headers={step.headers} onChange={(headers) => patch({ headers })} />}

      <FormTextarea
        label="Request Body (optional)"
        value={step.body}
        onChange={(body) => patch({ body })}
        placeholder='{"email":"{{email}}","password":"{{password}}"}'
        rows={4}
      />

      {advanced && (
        <div className="grid grid-cols-3 gap-3">
          <FormInput
            label="Step Timeout (seconds, optional)"
            type="number"
            value={step.timeout_seconds}
            onChange={(timeout_seconds) => patch({ timeout_seconds })}
            placeholder="20"
            min={1}
          />
          <FormInput
            label="Max Redirects (optional)"
            type="number"
            value={step.max_redirects}
            onChange={(max_redirects) => patch({ max_redirects })}
            placeholder="5"
            min={0}
          />
          <FormToggle
            label="Follow Redirects"
            checked={step.follow_redirects}
            onChange={(follow_redirects) => patch({ follow_redirects })}
          />
        </div>
      )}

      {advanced && <StepAssertionsEditor assertions={step.assertions} onChange={(assertions) => patch({ assertions })} />}
      {advanced && <StepExtractsEditor extracts={step.extracts} onChange={(extracts) => patch({ extracts })} />}
    </div>
  );
}

function StepHeadersEditor({ headers, onChange }: { headers: HeaderKV[]; onChange: (headers: HeaderKV[]) => void }) {
  const setHeader = (idx: number, next: Partial<HeaderKV>) => onChange(updateItem(headers, idx, (h) => ({ ...h, ...next })));

  return (
    <div>
      <label className="block text-xs font-medium text-slate-400 mb-1.5">Request Headers (optional)</label>
      <div className="space-y-2">
        {headers.map((h, idx) => {
          const sensitive = isSensitiveHeaderName(h.key);
          const showToggle = sensitive && h.value.trim().length > 0;
          return (
            <div key={idx} className="grid grid-cols-12 gap-2">
              <input
                className="input col-span-4"
                placeholder="Header name"
                value={h.key}
                onChange={(e) => setHeader(idx, { key: e.target.value })}
              />
              <input
                className="input col-span-6"
                placeholder="Header value"
                type={sensitive && !h.reveal ? 'password' : 'text'}
                value={h.value}
                onChange={(e) => setHeader(idx, { value: e.target.value })}
              />
              <button
                type="button"
                disabled={!showToggle}
                onClick={() => setHeader(idx, { reveal: !h.reveal })}
                className={`${BTN_GHOST_SM} col-span-1`}
                title={showToggle ? (h.reveal ? 'Hide value' : 'Show value') : 'Add a value to show/hide'}
              >
                {h.reveal ? 'Hide' : 'Show'}
              </button>
              <button
                type="button"
                className={`${BTN_GHOST_SM} col-span-1`}
                onClick={() => onChange(removeKeepingOne(headers, idx, emptyHeaderRow))}
              >
                ×
              </button>
            </div>
          );
        })}
      </div>
      <div className="mt-2">
        <button type="button" className={BTN_GHOST_SM} onClick={() => onChange([...headers, emptyHeaderRow()])}>
          + Add header
        </button>
      </div>
    </div>
  );
}

function StepAssertionsEditor({
  assertions,
  onChange,
}: {
  assertions: SyntheticAPIAssertionDraft[];
  onChange: (assertions: SyntheticAPIAssertionDraft[]) => void;
}) {
  const setAssertion = (idx: number, update: (a: SyntheticAPIAssertionDraft) => SyntheticAPIAssertionDraft) =>
    onChange(updateItem(assertions, idx, update));

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between">
        <label className="block text-xs font-medium text-slate-400">Assertions (optional)</label>
        <button type="button" className={BTN_GHOST_SM} onClick={() => onChange([...assertions, defaultSyntheticAPIAssertion()])}>
          + Add assertion
        </button>
      </div>

      <div className="space-y-2">
        {assertions.map((assertion, idx) => {
          const target = normalizeAssertionTarget(assertion.target);
          const allowedOps = syntheticAssertionOpsByTarget[target];
          const op = allowedOps.includes(assertion.op) ? assertion.op : allowedOps[0];

          return (
            <div key={idx} className="grid grid-cols-12 gap-2">
              <select
                className="input col-span-2"
                value={target}
                onChange={(e) =>
                  // A new target resets op and path to its defaults but keeps the typed value.
                  setAssertion(idx, (prev) => ({
                    ...defaultSyntheticAssertionForTarget(normalizeAssertionTarget(e.target.value)),
                    value_input: prev.value_input,
                  }))
                }
              >
                <option value="status">status</option>
                <option value="header">header</option>
                <option value="body">body</option>
                <option value="json">json</option>
              </select>

              <select
                className="input col-span-2"
                value={op}
                onChange={(e) => setAssertion(idx, (prev) => ({ ...prev, op: e.target.value }))}
              >
                {allowedOps.map((option) => (
                  <option key={option} value={option}>{option}</option>
                ))}
              </select>

              <input
                className="input col-span-3"
                placeholder={target === 'header' ? 'Header name' : target === 'json' ? 'JSON path' : 'Path (unused)'}
                value={assertion.path}
                disabled={!assertionPathRequired(target)}
                onChange={(e) => setAssertion(idx, (prev) => ({ ...prev, path: e.target.value }))}
              />

              <input
                className="input col-span-4"
                placeholder={assertionValuePlaceholder(target, op)}
                value={assertion.value_input}
                disabled={!assertionValueRequired(target, op)}
                onChange={(e) => setAssertion(idx, (prev) => ({ ...prev, value_input: e.target.value }))}
              />

              <button
                type="button"
                className={`${BTN_GHOST_SM} col-span-1`}
                onClick={() => onChange(assertions.filter((_, i) => i !== idx))}
              >
                ×
              </button>
            </div>
          );
        })}
      </div>
      <p className="text-xs text-slate-500">Value accepts plain text or JSON literals (e.g. <code>200</code>, <code>true</code>, <code>[200,201]</code>).</p>
    </div>
  );
}

function StepExtractsEditor({
  extracts,
  onChange,
}: {
  extracts: SyntheticAPIExtractDraft[];
  onChange: (extracts: SyntheticAPIExtractDraft[]) => void;
}) {
  const setExtract = (idx: number, next: Partial<SyntheticAPIExtractDraft>) =>
    onChange(updateItem(extracts, idx, (x) => ({ ...x, ...next })));

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between">
        <label className="block text-xs font-medium text-slate-400">Extracts (optional)</label>
        <button type="button" className={BTN_GHOST_SM} onClick={() => onChange([...extracts, defaultSyntheticAPIExtract()])}>
          + Add extract
        </button>
      </div>

      <div className="space-y-2">
        {extracts.map((extract, idx) => (
          <div key={idx} className="grid grid-cols-12 gap-2">
            <input
              className="input col-span-3"
              placeholder="Variable name"
              value={extract.name}
              onChange={(e) => setExtract(idx, { name: e.target.value })}
            />
            <select
              className="input col-span-2"
              value={extract.from}
              onChange={(e) => setExtract(idx, { from: e.target.value as SyntheticAPIExtractConfig['from'] })}
            >
              <option value="json">json</option>
              <option value="header">header</option>
            </select>
            <input
              className="input col-span-5"
              placeholder={extract.from === 'header' ? 'Header name' : 'JSON path'}
              value={extract.path}
              onChange={(e) => setExtract(idx, { path: e.target.value })}
            />
            <label className="col-span-1 flex items-center justify-center gap-1 text-xs text-slate-400">
              <input
                type="checkbox"
                checked={extract.sensitive}
                onChange={(e) => setExtract(idx, { sensitive: e.target.checked })}
              />
              Secret
            </label>
            <button
              type="button"
              className={`${BTN_GHOST_SM} col-span-1`}
              onClick={() => onChange(extracts.filter((_, i) => i !== idx))}
            >
              ×
            </button>
          </div>
        ))}
      </div>
      <p className="text-xs text-slate-500">Extracted values become variables for subsequent steps.</p>
    </div>
  );
}
