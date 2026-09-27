'use client';

import { useState } from 'react';
import type { SyntheticFailureMode } from '@/lib/types';
import type { FormErrors } from '@/lib/monitor-form/common';
import {
  type SyntheticAPIFormData,
  type SyntheticAPIStepDraft,
  type SyntheticAPITemplate,
  SYNTHETIC_API_ERROR_KEYS,
  defaultSyntheticAPIStep,
  syntheticAPITemplate,
} from '@/lib/monitor-form/synthetic-api';
import {
  BTN_GHOST_SM,
  FormInput,
  FormSelect,
  SectionHeader,
  SegmentedControl,
  SummaryChip,
  removeKeepingOne,
  swapItems,
  updateItem,
} from './MonitorFormControls';
import SyntheticApiStepEditor from './SyntheticApiStepEditor';
import SyntheticLiveTestPanel, { type SyntheticLiveTestTarget } from './SyntheticLiveTestPanel';
import SyntheticVariablesEditor from './SyntheticVariablesEditor';
import { useSyntheticLiveTest } from './useSyntheticLiveTest';

type EditorMode = 'basic' | 'advanced';

const FAILURE_MODES = [
  { value: 'fail_fast', label: 'Fail fast' },
  { value: 'continue', label: 'Continue after failures' },
];

interface SyntheticApiEditorProps {
  value: SyntheticAPIFormData;
  onChange: (next: SyntheticAPIFormData) => void;
  errors: FormErrors;
  /** Clears stale messages after a template replaces the journey. */
  onClearErrors: (keys: string[]) => void;
  /** Saved journeys that use advanced features open in Advanced mode. */
  advancedByDefault: boolean;
  liveTest: SyntheticLiveTestTarget;
}

/**
 * Multi-step API journey editor. The journey itself is owned by the parent
 * form (it is submitted); the Basic/Advanced view and the realtime test are
 * owned here.
 */
export default function SyntheticApiEditor({
  value,
  onChange,
  errors,
  onClearErrors,
  advancedByDefault,
  liveTest,
}: SyntheticApiEditorProps) {
  const [mode, setMode] = useState<EditorMode>(advancedByDefault ? 'advanced' : 'basic');
  const test = useSyntheticLiveTest({ monitorId: liveTest.monitorId, locationIds: liveTest.locationIds });
  const advanced = mode === 'advanced';

  const patch = (next: Partial<SyntheticAPIFormData>) => onChange({ ...value, ...next });
  const setSteps = (steps: SyntheticAPIStepDraft[]) => patch({ steps });

  const applyTemplate = (template: SyntheticAPITemplate) => {
    onChange(syntheticAPITemplate(template));
    onClearErrors(SYNTHETIC_API_ERROR_KEYS);
  };

  return (
    <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-6">
      <SectionHeader title="Synthetic API configuration" description="Build a multi-step API workflow with request, assertions, and extracts." />
      <div className="space-y-4">
        <div className="rounded-lg border border-cyan-500/20 bg-cyan-500/5 p-4">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div>
              <p className="text-sm font-medium text-white">Quick start templates</p>
              <p className="text-xs text-slate-400">Load a starter workflow, then customize fields for your environment.</p>
            </div>
            <div className="flex flex-wrap gap-2">
              <button type="button" className={BTN_GHOST_SM} onClick={() => applyTemplate('single_health')}>
                Health Check
              </button>
              <button type="button" className={BTN_GHOST_SM} onClick={() => applyTemplate('health_auth')}>
                Health + Auth Flow
              </button>
            </div>
          </div>
          <div className="mt-3 flex flex-wrap gap-2 text-xs">
            <SummaryChip>{value.steps.length} steps</SummaryChip>
            <SummaryChip>{value.variables.filter((v) => v.key.trim()).length} variables</SummaryChip>
            <SummaryChip>{value.steps.reduce((sum, step) => sum + step.extracts.length, 0)} extracts</SummaryChip>
          </div>
          <div className="mt-3">
            <SegmentedControl
              value={mode}
              onChange={setMode}
              options={[
                { value: 'basic', label: 'Basic' },
                { value: 'advanced', label: 'Advanced' },
              ]}
            />
            <p className="mt-2 text-xs text-slate-500">
              Basic keeps the journey builder focused. Advanced unlocks headers, extracts, and assertion tuning.
            </p>
          </div>
        </div>

        <SyntheticLiveTestPanel test={test} target={liveTest} />

        <div className="grid grid-cols-2 gap-4">
          <FormInput
            label="Base URL (optional)"
            value={value.base_url}
            onChange={(base_url) => patch({ base_url })}
            placeholder="https://api.example.com"
            error={errors.synthetic_api_base_url}
            hint="Relative step URLs resolve against this base URL."
          />
          <FormSelect
            label="Failure Mode"
            value={value.failure_mode}
            onChange={(failure_mode) => patch({ failure_mode: failure_mode as SyntheticFailureMode })}
            options={FAILURE_MODES}
            hint="Fail fast is recommended for linear user journeys."
          />
        </div>

        {advanced && (
          <SyntheticVariablesEditor
            rows={value.variables}
            onChange={(variables) => patch({ variables })}
            error={errors.synthetic_api_variables}
            hint={<>Use <code>{'{{variable_name}}'}</code> in URLs, headers, and request bodies.</>}
          />
        )}

        <div className="space-y-3">
          <div className="flex items-center justify-between">
            <label className="block text-xs font-medium text-slate-400">Steps</label>
            <button
              type="button"
              className={BTN_GHOST_SM}
              onClick={() => setSteps([...value.steps, defaultSyntheticAPIStep(value.steps.length + 1)])}
            >
              + Add step
            </button>
          </div>
          {errors.synthetic_api_steps && <p className="text-xs text-rose-400">{errors.synthetic_api_steps}</p>}
          {value.steps.map((step, idx) => (
            <SyntheticApiStepEditor
              key={idx}
              step={step}
              index={idx}
              count={value.steps.length}
              advanced={advanced}
              onChange={(next) => setSteps(updateItem(value.steps, idx, () => next))}
              onMove={(from, to) => setSteps(swapItems(value.steps, from, to))}
              onRemove={() => setSteps(removeKeepingOne(value.steps, idx, () => defaultSyntheticAPIStep(1)))}
            />
          ))}
        </div>
      </div>
    </div>
  );
}
