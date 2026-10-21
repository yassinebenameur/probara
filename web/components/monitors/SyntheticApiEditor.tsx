'use client';

import { Dispatch, ReactNode, SetStateAction } from 'react';
import { ChevronDown, ChevronUp, Eye, EyeOff, Plus, Trash2, X } from 'lucide-react';
import Button from '@/components/ui/Button';
import Checkbox from '@/components/ui/Checkbox';
import IconButton from '@/components/ui/IconButton';
import { SyntheticAPIExtractConfig } from '@/lib/types';
import { isSensitiveHeaderName } from '@/lib/monitor-form/http';
import { MonitorFormState, syntheticAPITemplateFields } from '@/lib/monitor-form/inline';
import {
  SyntheticAPIAssertionDraft,
  SyntheticAPIExtractDraft,
  SyntheticAPIStepDraft,
  SyntheticEditorMode,
  assertionPathRequired,
  assertionValuePlaceholder,
  assertionValueRequired,
  defaultSyntheticAPIAssertion,
  defaultSyntheticAPIExtract,
  defaultSyntheticAPIStep,
  defaultSyntheticAssertionForTarget,
  normalizeAssertionTarget,
  syntheticAssertionOpsByTarget,
} from '@/lib/monitor-form/synthetic';
import { FIELD_LABEL, FormInput, FormSelect, FormTextarea, ToggleRow, FormPanel } from './form-kit';
import { SyntheticModeHeader, SyntheticTemplatesWell, SyntheticVariablesField, swapped, withItem, withoutItem } from './synthetic-kit';

const STEP_METHODS = ['GET', 'HEAD', 'POST', 'PUT', 'DELETE', 'PATCH', 'OPTIONS'].map((m) => ({ value: m, label: m }));

/**
 * The synthetic API journey's Check section: Basic/Advanced mode, templates,
 * the live test, base URL, failure mode, variables and the step list. Its
 * fields live in MonitorForm's state; `mode` lives there too, so it survives
 * a look at another type.
 */
export default function SyntheticApiEditor({
  form,
  setForm,
  errors,
  setErrors,
  mode,
  onModeChange,
  liveTest,
}: {
  form: MonitorFormState;
  setForm: Dispatch<SetStateAction<MonitorFormState>>;
  errors: Record<string, string>;
  setErrors: Dispatch<SetStateAction<Record<string, string>>>;
  mode: SyntheticEditorMode;
  onModeChange: (mode: SyntheticEditorMode) => void;
  liveTest: ReactNode;
}) {
  const patch = (p: Partial<MonitorFormState>) => setForm({ ...form, ...p });
  const steps = form.synthetic_api_steps;
  const setSteps = (next: SyntheticAPIStepDraft[]) => patch({ synthetic_api_steps: next });

  const applyTemplate = (template: 'health_auth' | 'single_health') => {
    setForm((prev) => ({ ...prev, ...syntheticAPITemplateFields(template) }));
    setErrors((prev) => ({
      ...prev,
      synthetic_api_steps: '',
      synthetic_api_variables: '',
      synthetic_api_base_url: '',
    }));
  };

  return (
    <FormPanel title="Check">
      <SyntheticModeHeader
        mode={mode}
        onModeChange={onModeChange}
        facts={[
          `${steps.length} steps`,
          `${(form.synthetic_api_variables || []).filter((v) => v.key.trim()).length} variables`,
          `${steps.reduce((sum, step) => sum + step.extracts.length, 0)} extracts`,
        ]}
        description="Basic keeps the journey builder focused. Advanced unlocks headers, extracts, and assertion tuning."
      />

      <SyntheticTemplatesWell
        description="Load a starter workflow, then customize fields for your environment."
        templates={[
          { label: 'Health check', onApply: () => applyTemplate('single_health') },
          { label: 'Health + auth flow', onApply: () => applyTemplate('health_auth') },
        ]}
      />

      {liveTest}

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <FormInput
          label="Base URL (optional)"
          value={form.synthetic_api_base_url}
          onChange={(v) => patch({ synthetic_api_base_url: v })}
          placeholder="https://api.example.com"
          error={errors.synthetic_api_base_url}
          hint="Relative step URLs resolve against this base URL."
        />
        <FormSelect
          label="Failure mode"
          value={form.synthetic_api_failure_mode}
          onChange={(v) => patch({ synthetic_api_failure_mode: v as 'fail_fast' | 'continue' })}
          options={[
            { value: 'fail_fast', label: 'Fail fast' },
            { value: 'continue', label: 'Continue after failures' },
          ]}
          hint="Fail fast is recommended for linear user journeys."
        />
      </div>

      {mode === 'advanced' && (
        <SyntheticVariablesField
          rows={form.synthetic_api_variables}
          onChange={(rows) => patch({ synthetic_api_variables: rows })}
          error={errors.synthetic_api_variables}
          hint={<>Use <code>{'{{variable_name}}'}</code> in URLs, headers, and request bodies.</>}
        />
      )}

      <div className="space-y-3">
        <div className="flex items-center justify-between">
          <label className={FIELD_LABEL}>Steps</label>
          <Button
            type="button"
            variant="ghost"
            size="xs"
            icon={<Plus className="h-3.5 w-3.5" strokeWidth={1.75} />}
            onClick={() => setSteps([...steps, defaultSyntheticAPIStep(steps.length + 1)])}
          >
            Add step
          </Button>
        </div>
        {errors.synthetic_api_steps && <p className="text-xs text-rose-300">{errors.synthetic_api_steps}</p>}
        {steps.map((step, stepIdx) => (
          <SyntheticApiStepCard
            key={`synthetic-api-step-${stepIdx}`}
            step={step}
            index={stepIdx}
            count={steps.length}
            advanced={mode === 'advanced'}
            onChange={(next) => setSteps(withItem(steps, stepIdx, next))}
            onMove={(to) => setSteps(swapped(steps, stepIdx, to))}
            onRemove={() =>
              setSteps(steps.length === 1 ? [defaultSyntheticAPIStep(1)] : withoutItem(steps, stepIdx))
            }
          />
        ))}
      </div>
    </FormPanel>
  );
}

function SyntheticApiStepCard({
  step,
  index,
  count,
  advanced,
  onChange,
  onMove,
  onRemove,
}: {
  step: SyntheticAPIStepDraft;
  index: number;
  count: number;
  advanced: boolean;
  onChange: (step: SyntheticAPIStepDraft) => void;
  onMove: (to: number) => void;
  onRemove: () => void;
}) {
  const patchStep = (p: Partial<SyntheticAPIStepDraft>) => onChange({ ...step, ...p });
  const patchHeader = (idx: number, p: Partial<SyntheticAPIStepDraft['headers'][number]>) =>
    patchStep({ headers: withItem(step.headers, idx, { ...step.headers[idx], ...p }) });
  const setAssertion = (idx: number, assertion: SyntheticAPIAssertionDraft) =>
    patchStep({ assertions: withItem(step.assertions, idx, assertion) });
  const patchExtract = (idx: number, p: Partial<SyntheticAPIExtractDraft>) =>
    patchStep({ extracts: withItem(step.extracts, idx, { ...step.extracts[idx], ...p }) });

  return (
    <div className="glass-well space-y-4 p-4">
      <div className="flex items-center justify-between">
        <div>
          <p className="text-sm font-medium text-white">Step {index + 1}</p>
          <p className="font-mono text-xs text-slate-500">{step.method || 'GET'} {step.url || '(missing URL)'}</p>
        </div>
        <div className="flex gap-1">
          <IconButton
            icon={<ChevronUp strokeWidth={1.75} />}
            label="Move step up"
            disabled={index === 0}
            onClick={() => {
              if (index === 0) return;
              onMove(index - 1);
            }}
          />
          <IconButton
            icon={<ChevronDown strokeWidth={1.75} />}
            label="Move step down"
            disabled={index === count - 1}
            onClick={() => {
              if (index === count - 1) return;
              onMove(index + 1);
            }}
          />
          <IconButton icon={<Trash2 strokeWidth={1.75} />} label="Remove step" danger onClick={onRemove} />
        </div>
      </div>

      <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
        <FormInput label="Step ID" value={step.id} onChange={(v) => patchStep({ id: v })} placeholder="health" />
        <FormInput
          label="Name (optional)"
          value={step.name}
          onChange={(v) => patchStep({ name: v })}
          placeholder="Health check"
        />
        <FormSelect label="Method" value={step.method} onChange={(v) => patchStep({ method: v })} options={STEP_METHODS} />
      </div>

      <FormInput
        label="URL"
        value={step.url}
        onChange={(v) => patchStep({ url: v })}
        placeholder="/health or https://service.example.com/health"
      />

      {advanced && (
        <div>
          <label className={`${FIELD_LABEL} mb-1.5`}>Request headers (optional)</label>
          <div className="space-y-2">
            {step.headers.map((h, headerIdx) => {
              const sensitive = isSensitiveHeaderName(h.key);
              const showToggle = sensitive && h.value.trim().length > 0;
              return (
                <div key={`synthetic-api-step-${index}-header-${headerIdx}`} className="grid grid-cols-12 gap-2">
                  <input
                    className="input col-span-4"
                    placeholder="Header name"
                    aria-label={`Header ${headerIdx + 1} name`}
                    value={h.key}
                    onChange={(e) => patchHeader(headerIdx, { key: e.target.value })}
                  />
                  <input
                    className="input col-span-6"
                    placeholder="Header value"
                    aria-label={`Header ${headerIdx + 1} value`}
                    type={sensitive && !h.reveal ? 'password' : 'text'}
                    value={h.value}
                    onChange={(e) => patchHeader(headerIdx, { value: e.target.value })}
                  />
                  <div className="col-span-1 flex items-center justify-center">
                    <IconButton
                      icon={h.reveal ? <EyeOff strokeWidth={1.75} /> : <Eye strokeWidth={1.75} />}
                      label={showToggle ? (h.reveal ? 'Hide value' : 'Show value') : 'Add a value to show/hide'}
                      disabled={!showToggle}
                      onClick={() => patchHeader(headerIdx, { reveal: !h.reveal })}
                    />
                  </div>
                  <div className="col-span-1 flex items-center justify-center">
                    <IconButton
                      icon={<X strokeWidth={1.75} />}
                      label="Remove header"
                      onClick={() =>
                        patchStep({
                          headers:
                            step.headers.length === 1
                              ? [{ key: '', value: '', reveal: false }]
                              : withoutItem(step.headers, headerIdx),
                        })
                      }
                    />
                  </div>
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
              onClick={() => patchStep({ headers: [...step.headers, { key: '', value: '', reveal: false }] })}
            >
              Add header
            </Button>
          </div>
        </div>
      )}

      <FormTextarea
        label="Request body (optional)"
        value={step.body}
        onChange={(v) => patchStep({ body: v })}
        placeholder='{"email":"{{email}}","password":"{{password}}"}'
        rows={4}
      />

      {advanced && (
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
          <FormInput
            label="Step timeout (seconds, optional)"
            type="number"
            value={step.timeout_seconds}
            onChange={(v) => patchStep({ timeout_seconds: v })}
            placeholder="20"
            min={1}
          />
          <FormInput
            label="Max redirects (optional)"
            type="number"
            value={step.max_redirects}
            onChange={(v) => patchStep({ max_redirects: v })}
            placeholder="5"
            min={0}
          />
          <div className="sm:flex sm:flex-col sm:justify-end">
            <div className="sm:flex sm:h-10 sm:flex-col sm:justify-center">
              <ToggleRow
                title="Follow redirects"
                checked={step.follow_redirects}
                onChange={(v) => patchStep({ follow_redirects: v })}
              />
            </div>
          </div>
        </div>
      )}

      {advanced && (
        <div className="space-y-2">
          <div className="flex items-center justify-between">
            <label className={FIELD_LABEL}>Assertions (optional)</label>
            <Button
              type="button"
              variant="ghost"
              size="xs"
              icon={<Plus className="h-3.5 w-3.5" strokeWidth={1.75} />}
              onClick={() => patchStep({ assertions: [...step.assertions, defaultSyntheticAPIAssertion()] })}
            >
              Add assertion
            </Button>
          </div>

          <div className="space-y-2">
            {step.assertions.map((assertion, assertionIdx) => {
              const target = normalizeAssertionTarget(assertion.target);
              const allowedOps = syntheticAssertionOpsByTarget[target];
              const op = allowedOps.includes(assertion.op) ? assertion.op : allowedOps[0];
              const needsPath = assertionPathRequired(target);
              const needsValue = assertionValueRequired(target, op);

              return (
                <div key={`synthetic-api-step-${index}-assertion-${assertionIdx}`} className="grid grid-cols-12 gap-2">
                  <select
                    className="input col-span-2"
                    aria-label={`Assertion ${assertionIdx + 1} target`}
                    value={target}
                    onChange={(e) =>
                      // A new target resets the assertion but keeps the typed value.
                      setAssertion(assertionIdx, {
                        ...defaultSyntheticAssertionForTarget(normalizeAssertionTarget(e.target.value)),
                        value_input: assertion.value_input,
                      })
                    }
                  >
                    <option value="status">status</option>
                    <option value="header">header</option>
                    <option value="body">body</option>
                    <option value="json">json</option>
                  </select>

                  <select
                    className="input col-span-2"
                    aria-label={`Assertion ${assertionIdx + 1} operator`}
                    value={op}
                    onChange={(e) => setAssertion(assertionIdx, { ...assertion, op: e.target.value })}
                  >
                    {allowedOps.map((option) => (
                      <option key={option} value={option}>{option}</option>
                    ))}
                  </select>

                  <input
                    className="input col-span-3"
                    placeholder={target === 'header' ? 'Header name' : target === 'json' ? 'JSON path' : 'Path (unused)'}
                    aria-label={`Assertion ${assertionIdx + 1} ${target === 'header' ? 'header name' : 'path'}`}
                    value={assertion.path}
                    disabled={!needsPath}
                    onChange={(e) => setAssertion(assertionIdx, { ...assertion, path: e.target.value })}
                  />

                  <input
                    className="input col-span-4"
                    placeholder={assertionValuePlaceholder(target, op)}
                    aria-label={`Assertion ${assertionIdx + 1} value`}
                    value={assertion.value_input}
                    disabled={!needsValue}
                    onChange={(e) => setAssertion(assertionIdx, { ...assertion, value_input: e.target.value })}
                  />

                  <div className="col-span-1 flex items-center justify-center">
                    <IconButton
                      icon={<X strokeWidth={1.75} />}
                      label="Remove assertion"
                      onClick={() => patchStep({ assertions: withoutItem(step.assertions, assertionIdx) })}
                    />
                  </div>
                </div>
              );
            })}
          </div>
          <p className="text-xs text-slate-500">Value accepts plain text or JSON literals (e.g. <code>200</code>, <code>true</code>, <code>[200,201]</code>).</p>
        </div>
      )}

      {advanced && (
        <div className="space-y-2">
          <div className="flex items-center justify-between">
            <label className={FIELD_LABEL}>Extracts (optional)</label>
            <Button
              type="button"
              variant="ghost"
              size="xs"
              icon={<Plus className="h-3.5 w-3.5" strokeWidth={1.75} />}
              onClick={() => patchStep({ extracts: [...step.extracts, defaultSyntheticAPIExtract()] })}
            >
              Add extract
            </Button>
          </div>

          <div className="space-y-2">
            {step.extracts.map((extract, extractIdx) => (
              <div key={`synthetic-api-step-${index}-extract-${extractIdx}`} className="grid grid-cols-12 gap-2">
                <input
                  className="input col-span-3"
                  placeholder="Variable name"
                  aria-label="Variable name"
                  value={extract.name}
                  onChange={(e) => patchExtract(extractIdx, { name: e.target.value })}
                />
                <select
                  className="input col-span-2"
                  aria-label="Extract from"
                  value={extract.from}
                  onChange={(e) => patchExtract(extractIdx, { from: e.target.value as SyntheticAPIExtractConfig['from'] })}
                >
                  <option value="json">json</option>
                  <option value="header">header</option>
                </select>
                <input
                  className="input col-span-5"
                  placeholder={extract.from === 'header' ? 'Header name' : 'JSON path'}
                  aria-label={extract.from === 'header' ? 'Header name' : 'JSON path'}
                  value={extract.path}
                  onChange={(e) => patchExtract(extractIdx, { path: e.target.value })}
                />
                <Checkbox
                  size="sm"
                  checked={extract.sensitive}
                  onChange={(checked) => patchExtract(extractIdx, { sensitive: checked })}
                  className="col-span-1 !items-center justify-center !gap-1.5"
                  label={<span className="text-xs text-slate-400">Secret</span>}
                />
                <div className="col-span-1 flex items-center justify-center">
                  <IconButton
                    icon={<X strokeWidth={1.75} />}
                    label="Remove extract"
                    onClick={() => patchStep({ extracts: withoutItem(step.extracts, extractIdx) })}
                  />
                </div>
              </div>
            ))}
          </div>
          <p className="text-xs text-slate-500">Extracted values become variables for subsequent steps.</p>
        </div>
      )}
    </div>
  );
}
