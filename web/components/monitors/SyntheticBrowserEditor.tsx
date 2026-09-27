'use client';

import { useState } from 'react';
import type { SyntheticBrowserAction, SyntheticFailureMode } from '@/lib/types';
import type { FormErrors } from '@/lib/monitor-form/common';
import {
  type SyntheticBrowserFormData,
  type SyntheticBrowserGuidedTemplate,
  type SyntheticBrowserSetupMode,
  type SyntheticBrowserStepDraft,
  SYNTHETIC_BROWSER_ERROR_KEYS,
  applyExpertTemplate,
  applyGuidedTemplate,
  browserActionNeedsSelector,
  browserActionNeedsURL,
  browserActionNeedsValue,
  defaultSyntheticBrowserStep,
  guidedJourneyOf,
  syncGuidedIntoExpert,
  syntheticBrowserActionMeta,
} from '@/lib/monitor-form/synthetic-browser';
import {
  BTN_GHOST_SM,
  FormInput,
  FormSelect,
  FormToggle,
  ReorderRemoveButtons,
  SectionHeader,
  SegmentedControl,
  SummaryChip,
  removeKeepingOne,
  swapItems,
  updateItem,
} from './MonitorFormControls';
import SyntheticBrowserGuidedSetup, { type GuidedWizardStep } from './SyntheticBrowserGuidedSetup';
import SyntheticLiveTestPanel, { type SyntheticLiveTestTarget } from './SyntheticLiveTestPanel';
import SyntheticVariablesEditor from './SyntheticVariablesEditor';
import { useSyntheticLiveTest } from './useSyntheticLiveTest';

type EditorMode = 'basic' | 'advanced';

const FAILURE_MODES = [
  { value: 'fail_fast', label: 'Fail fast' },
  { value: 'continue', label: 'Continue after failures' },
];

const ACTION_OPTIONS = (Object.keys(syntheticBrowserActionMeta) as SyntheticBrowserAction[]).map((action) => ({
  value: action,
  label: syntheticBrowserActionMeta[action].label,
}));

interface SyntheticBrowserEditorProps {
  /** Includes `setup_mode`: guided or expert decides which config is saved. */
  value: SyntheticBrowserFormData;
  onChange: (next: SyntheticBrowserFormData) => void;
  errors: FormErrors;
  /** Clears stale messages after a template or mode switch replaces the journey. */
  onClearErrors: (keys: string[]) => void;
  /** Saved journeys that use advanced features open the expert editor in Advanced mode. */
  advancedByDefault: boolean;
  liveTest: SyntheticLiveTestTarget;
}

/**
 * Browser journey editor: a guided wizard for common flows and an expert step
 * editor. The journey and setup mode are owned by the parent form (they are
 * submitted); the wizard position, Basic/Advanced view and realtime test are
 * owned here.
 */
export default function SyntheticBrowserEditor({
  value,
  onChange,
  errors,
  onClearErrors,
  advancedByDefault,
  liveTest,
}: SyntheticBrowserEditorProps) {
  const [mode, setMode] = useState<EditorMode>(advancedByDefault ? 'advanced' : 'basic');
  const [wizardStep, setWizardStep] = useState<GuidedWizardStep>(1);
  const test = useSyntheticLiveTest({ monitorId: liveTest.monitorId, locationIds: liveTest.locationIds });

  const patch = (next: Partial<SyntheticBrowserFormData>) => onChange({ ...value, ...next });

  const switchSetupMode = (next: SyntheticBrowserSetupMode) => {
    if (value.setup_mode === next) return;
    if (next === 'expert') {
      onChange({ ...syncGuidedIntoExpert(value), setup_mode: next });
      onClearErrors(SYNTHETIC_BROWSER_ERROR_KEYS);
    } else {
      patch({ setup_mode: next });
      setWizardStep(1);
    }
  };

  const pickGuidedTemplate = (template: SyntheticBrowserGuidedTemplate) => {
    onChange(applyGuidedTemplate(value, template));
    onClearErrors(SYNTHETIC_BROWSER_ERROR_KEYS);
    setWizardStep(2);
  };

  const pickExpertTemplate = (template: SyntheticBrowserGuidedTemplate) => {
    onChange(applyExpertTemplate(value, template));
    onClearErrors(SYNTHETIC_BROWSER_ERROR_KEYS);
  };

  return (
    <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-6">
      <SectionHeader title="Synthetic Browser configuration" description="Guided setup for normal users, expert editor for full control." />
      <div className="space-y-4">
        <div className="rounded-lg border border-cyan-500/20 bg-cyan-500/5 p-4">
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div>
              <p className="text-sm font-medium text-white">Setup Mode</p>
              <p className="text-xs text-slate-400">Guided mode hides step-level complexity and applies safe defaults.</p>
            </div>
            <SegmentedControl
              value={value.setup_mode}
              onChange={switchSetupMode}
              options={[
                { value: 'guided', label: 'Guided' },
                { value: 'expert', label: 'Expert' },
              ]}
            />
          </div>
          <p className="mt-2 text-xs text-slate-500">
            {value.setup_mode === 'guided'
              ? 'Guided enforces: fail-fast, Desktop Chrome, screenshot-on-failure only.'
              : 'Expert mode exposes full step/action configuration.'}
          </p>
        </div>

        {value.setup_mode === 'guided' && (
          <SyntheticBrowserGuidedSetup
            value={value}
            journey={guidedJourneyOf(value)}
            errors={errors}
            wizardStep={wizardStep}
            onWizardStepChange={setWizardStep}
            onStartURLChange={(start_url) => patch({ start_url })}
            onInputsChange={(inputs) => patch({ guided: { ...value.guided, ...inputs } })}
            onPickTemplate={pickGuidedTemplate}
            onPickCustom={() => switchSetupMode('expert')}
          />
        )}

        {value.setup_mode === 'expert' && (
          <>
            <div className="rounded-lg border border-cyan-500/20 bg-cyan-500/5 p-4">
              <div className="flex flex-wrap items-center justify-between gap-3">
                <div>
                  <p className="text-sm font-medium text-white">Quick start templates</p>
                  <p className="text-xs text-slate-400">Start from a realistic browser flow and adjust selectors/URLs.</p>
                </div>
                <div className="flex flex-wrap gap-2">
                  <button type="button" className={BTN_GHOST_SM} onClick={() => pickExpertTemplate('homepage_smoke')}>
                    Homepage Smoke
                  </button>
                  <button type="button" className={BTN_GHOST_SM} onClick={() => pickExpertTemplate('login_flow')}>
                    Login Flow
                  </button>
                </div>
              </div>
              <div className="mt-3 flex flex-wrap gap-2 text-xs">
                <SummaryChip>{value.steps.length} steps</SummaryChip>
                <SummaryChip>{value.variables.filter((v) => v.key.trim()).length} variables</SummaryChip>
                <SummaryChip>Artifacts: {value.screenshot_on_failure ? 'Screenshot' : 'No screenshot'}</SummaryChip>
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
                  Basic is optimized for common user journeys. Advanced unlocks device/variables/step timeout tuning.
                </p>
              </div>
            </div>

            <SyntheticLiveTestPanel test={test} target={liveTest} />

            <ExpertRunSettings value={value} advanced={mode === 'advanced'} errors={errors} onChange={patch} />

            <div className="space-y-3">
              <div className="flex items-center justify-between">
                <label className="block text-xs font-medium text-slate-400">Steps</label>
                <button
                  type="button"
                  className={BTN_GHOST_SM}
                  onClick={() => patch({ steps: [...value.steps, defaultSyntheticBrowserStep(value.steps.length + 1)] })}
                >
                  + Add step
                </button>
              </div>
              {errors.synthetic_browser_steps && <p className="text-xs text-rose-400">{errors.synthetic_browser_steps}</p>}
              {value.steps.map((step, idx) => (
                <BrowserStepEditor
                  key={idx}
                  step={step}
                  index={idx}
                  count={value.steps.length}
                  advanced={mode === 'advanced'}
                  onChange={(next) => patch({ steps: updateItem(value.steps, idx, () => next) })}
                  onMove={(from, to) => patch({ steps: swapItems(value.steps, from, to) })}
                  onRemove={() => patch({ steps: removeKeepingOne(value.steps, idx, () => defaultSyntheticBrowserStep(1)) })}
                />
              ))}
            </div>
          </>
        )}
      </div>
    </div>
  );
}

function ExpertRunSettings({
  value,
  advanced,
  errors,
  onChange,
}: {
  value: SyntheticBrowserFormData;
  advanced: boolean;
  errors: FormErrors;
  onChange: (patch: Partial<SyntheticBrowserFormData>) => void;
}) {
  return (
    <>
      <div className="grid grid-cols-2 gap-4">
        <FormInput
          label="Start URL"
          value={value.start_url}
          onChange={(start_url) => onChange({ start_url })}
          placeholder="https://app.example.com/login"
          error={errors.synthetic_browser_start_url}
        />
        {advanced ? (
          <FormInput
            label="Device (optional)"
            value={value.device}
            onChange={(device) => onChange({ device })}
            placeholder="Desktop Chrome"
          />
        ) : (
          <div className="rounded-lg border border-white/[0.08] bg-slate-900/40 px-3 py-3">
            <p className="text-xs font-medium text-slate-300">Device profile</p>
            <p className="mt-1 text-xs text-slate-500">Using saved/default device. Switch to Advanced to customize it.</p>
          </div>
        )}
      </div>

      <div className="grid grid-cols-2 gap-4">
        <FormSelect
          label="Failure Mode"
          value={value.failure_mode}
          onChange={(failure_mode) => onChange({ failure_mode: failure_mode as SyntheticFailureMode })}
          options={FAILURE_MODES}
          hint="Fail fast is recommended for user journey checks."
        />
        <div className="space-y-2">
          <label className="block text-xs font-medium text-slate-400 mb-1.5">Artifacts on Failure</label>
          <div className="grid grid-cols-1 gap-2">
            <FormToggle
              label="Screenshot"
              description="Best default for quick triage in UI."
              checked={value.screenshot_on_failure}
              onChange={(screenshot_on_failure) => onChange({ screenshot_on_failure })}
            />
            <FormToggle
              label="Trace"
              description="Deep Playwright timeline for debugging."
              checked={value.trace_on_failure}
              onChange={(trace_on_failure) => onChange({ trace_on_failure })}
            />
            <FormToggle
              label="HAR"
              description="Capture network waterfall and payload metadata."
              checked={value.har_on_failure}
              onChange={(har_on_failure) => onChange({ har_on_failure })}
            />
          </div>
        </div>
      </div>

      {advanced && (
        <SyntheticVariablesEditor
          rows={value.variables}
          onChange={(variables) => onChange({ variables })}
          error={errors.synthetic_browser_variables}
          hint={<>Use <code>{'{{variable_name}}'}</code> inside selectors and values.</>}
        />
      )}
    </>
  );
}

function BrowserStepEditor({
  step,
  index,
  count,
  advanced,
  onChange,
  onMove,
  onRemove,
}: {
  step: SyntheticBrowserStepDraft;
  index: number;
  count: number;
  advanced: boolean;
  onChange: (step: SyntheticBrowserStepDraft) => void;
  onMove: (from: number, to: number) => void;
  onRemove: () => void;
}) {
  const patch = (next: Partial<SyntheticBrowserStepDraft>) => onChange({ ...step, ...next });
  const actionMeta = syntheticBrowserActionMeta[step.action];
  const summary =
    step.action === 'goto'
      ? step.url || '(missing URL)'
      : step.action === 'assert_url'
      ? step.value || '(missing expected URL)'
      : step.selector || '(missing selector)';

  return (
    <div className="rounded-lg border border-white/[0.08] bg-slate-800/20 p-4 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <p className="text-xs font-medium text-slate-300">Step {index + 1}</p>
          <p className="text-[11px] text-slate-500">{actionMeta.label} · {summary}</p>
        </div>
        <ReorderRemoveButtons index={index} count={count} onMove={onMove} onRemove={onRemove} />
      </div>

      <div className="grid grid-cols-2 gap-3">
        <FormInput label="Step ID" value={step.id} onChange={(id) => patch({ id })} placeholder="open_login" />
        <FormSelect
          label="Action"
          value={step.action}
          onChange={(action) => patch({ action: action as SyntheticBrowserAction })}
          options={ACTION_OPTIONS}
          hint={actionMeta.hint}
        />
      </div>

      {browserActionNeedsURL(step.action) && (
        <FormInput label="URL" value={step.url} onChange={(url) => patch({ url })} placeholder="https://app.example.com/login" />
      )}

      {browserActionNeedsSelector(step.action) && (
        <FormInput
          label="Selector"
          value={step.selector}
          onChange={(selector) => patch({ selector })}
          placeholder={actionMeta.selectorPlaceholder || '[data-test=target]'}
          hint="Supports any valid CSS selector."
        />
      )}

      {browserActionNeedsValue(step.action) && (
        <FormInput
          label={step.action === 'assert_url' ? 'Expected URL / pattern' : 'Value'}
          value={step.value}
          onChange={(v) => patch({ value: v })}
          placeholder={step.action === 'assert_url' ? '/dashboard' : actionMeta.valuePlaceholder || 'Text or value'}
        />
      )}

      {advanced && (
        <FormInput
          label="Step Timeout (seconds, optional)"
          type="number"
          value={step.timeout_seconds}
          onChange={(timeout_seconds) => patch({ timeout_seconds })}
          placeholder="20"
          min={1}
        />
      )}
    </div>
  );
}
