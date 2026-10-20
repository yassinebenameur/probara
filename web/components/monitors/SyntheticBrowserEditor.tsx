'use client';

import { Dispatch, ReactNode, SetStateAction, useEffect, useState } from 'react';
import { ChevronDown, ChevronUp, Plus, Trash2 } from 'lucide-react';
import Button from '@/components/ui/Button';
import FilterChip from '@/components/ui/FilterChip';
import FormSection from '@/components/ui/FormSection';
import IconButton from '@/components/ui/IconButton';
import Tabs from '@/components/ui/Tabs';
import { MonitorType, SyntheticBrowserAction } from '@/lib/types';
import {
  MonitorFormState,
  syntheticBrowserGuidedDraftFromForm,
  syntheticBrowserGuidedTemplateFields,
  syntheticBrowserGuidedToExpertFields,
  syntheticBrowserTemplateFields,
} from '@/lib/monitor-form/inline';
import {
  SyntheticBrowserGuidedStep,
  SyntheticBrowserGuidedTemplate,
  SyntheticBrowserSetupMode,
  SyntheticBrowserStepDraft,
  SyntheticEditorMode,
  buildSyntheticBrowserGuidedDraft,
  defaultSyntheticBrowserStep,
  syntheticBrowserActionMeta,
} from '@/lib/monitor-form/synthetic';
import { FIELD_LABEL, FormInput, FormSelect, TILE_SELECTED, ToggleRow } from './form-kit';
import { SyntheticModeHeader, SyntheticTemplatesWell, SyntheticVariablesField, swapped, withItem, withoutItem } from './synthetic-kit';

const BROWSER_SETUP_TABS = [
  { id: 'guided', label: 'Guided' },
  { id: 'expert', label: 'Expert' },
] as const;

const ACTION_OPTIONS = (
  ['goto', 'click', 'fill', 'wait_for', 'assert_visible', 'assert_text', 'assert_url'] as SyntheticBrowserAction[]
).map((action) => ({ value: action, label: syntheticBrowserActionMeta[action].label }));

const GUIDED_STEP_LABELS: Record<SyntheticBrowserGuidedStep, string> = { 1: '1. Journey', 2: '2. Inputs', 3: '3. Review' };

/**
 * Guided vs Expert setup and the guided wizard's step. MonitorForm owns it
 * because a submit builds the guided journey (`guided`) rather than the
 * expert step list. A create that lands on the browser type starts Guided at
 * step 1; edit starts Expert.
 */
export function useSyntheticBrowserSetup(monitorType: MonitorType, isEditMode: boolean) {
  const [mode, setMode] = useState<SyntheticBrowserSetupMode>(isEditMode ? 'expert' : 'guided');
  const [guidedStep, setGuidedStep] = useState<SyntheticBrowserGuidedStep>(1);
  useEffect(() => {
    if (monitorType === 'synthetic_browser' && !isEditMode) {
      setMode('guided');
      setGuidedStep(1);
    }
  }, [monitorType, isEditMode]);
  return {
    mode,
    setMode,
    guidedStep,
    setGuidedStep,
    guided: monitorType === 'synthetic_browser' && mode === 'guided',
  };
}

export type SyntheticBrowserSetup = ReturnType<typeof useSyntheticBrowserSetup>;

interface EditorProps {
  form: MonitorFormState;
  setForm: Dispatch<SetStateAction<MonitorFormState>>;
  errors: Record<string, string>;
  setErrors: Dispatch<SetStateAction<Record<string, string>>>;
}

const clearBrowserErrors = (prev: Record<string, string>) => ({
  ...prev,
  synthetic_browser_steps: '',
  synthetic_browser_start_url: '',
  synthetic_browser_variables: '',
});

/**
 * The synthetic browser journey's Check section: the Guided wizard or the
 * Expert step editor. Fields live in MonitorForm's state, as do the setup
 * (useSyntheticBrowserSetup) and the Basic/Advanced `mode`.
 */
export default function SyntheticBrowserEditor({
  form,
  setForm,
  errors,
  setErrors,
  setup,
  mode,
  onModeChange,
  liveTest,
}: EditorProps & {
  setup: SyntheticBrowserSetup;
  mode: SyntheticEditorMode;
  onModeChange: (mode: SyntheticEditorMode) => void;
  liveTest: ReactNode;
}) {
  const switchSetupMode = (nextMode: SyntheticBrowserSetupMode) => {
    if (setup.mode === nextMode) return;
    if (nextMode === 'expert') {
      // Carry the guided journey over as editable steps.
      const next = syntheticBrowserGuidedToExpertFields(form);
      setForm((prev) => ({ ...prev, ...next }));
      setErrors(clearBrowserErrors);
    }
    setup.setMode(nextMode);
    if (nextMode === 'guided') {
      setup.setGuidedStep(1);
    }
  };

  return (
    <FormSection title="Check">
      <div>
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <p className="text-sm font-medium text-white">Setup mode</p>
            <p className="text-ui text-slate-400">
              Guided mode hides step-level complexity and applies safe defaults.
            </p>
          </div>
          <Tabs tabs={BROWSER_SETUP_TABS} active={setup.mode} onChange={switchSetupMode} />
        </div>
        <p className="mt-2 text-xs text-slate-500">
          {setup.mode === 'guided'
            ? 'Guided enforces: fail-fast, Desktop Chrome, screenshot-on-failure only.'
            : 'Expert mode exposes full step/action configuration.'}
        </p>
      </div>

      {setup.mode === 'guided' && (
        <GuidedSetup
          form={form}
          setForm={setForm}
          errors={errors}
          setErrors={setErrors}
          step={setup.guidedStep}
          setStep={setup.setGuidedStep}
          onCustom={() => switchSetupMode('expert')}
        />
      )}

      {setup.mode === 'expert' && (
        <ExpertSetup
          form={form}
          setForm={setForm}
          errors={errors}
          setErrors={setErrors}
          mode={mode}
          onModeChange={onModeChange}
          liveTest={liveTest}
        />
      )}
    </FormSection>
  );
}

function GuidedSetup({
  form,
  setForm,
  errors,
  setErrors,
  step: guidedStep,
  setStep,
  onCustom,
}: EditorProps & {
  step: SyntheticBrowserGuidedStep;
  setStep: Dispatch<SetStateAction<SyntheticBrowserGuidedStep>>;
  onCustom: () => void;
}) {
  const patch = (p: Partial<MonitorFormState>) => setForm({ ...form, ...p });
  const template = form.synthetic_browser_guided_template;

  const applyTemplate = (next: SyntheticBrowserGuidedTemplate) => {
    setForm((prev) => ({ ...prev, ...syntheticBrowserGuidedTemplateFields(prev, next) }));
    setErrors(clearBrowserErrors);
    setStep(2);
  };

  const templateTile = (id: SyntheticBrowserGuidedTemplate, title: string, description: string) => (
    <button
      type="button"
      className={`glass-well px-4 py-3 text-left transition-colors ${
        template === id ? TILE_SELECTED : 'hover:bg-well-hover'
      }`}
      onClick={() => applyTemplate(id)}
    >
      <p className="text-sm font-medium text-white">{title}</p>
      <p className="mt-1 text-xs text-slate-500">{description}</p>
    </button>
  );

  const preview = guidedStep === 3 ? buildSyntheticBrowserGuidedDraft(syntheticBrowserGuidedDraftFromForm(form)) : null;

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        {([1, 2, 3] as const).map((step) => (
          <FilterChip key={`guided-step-${step}`} selected={guidedStep === step} onClick={() => setStep(step)}>
            {GUIDED_STEP_LABELS[step]}
          </FilterChip>
        ))}
      </div>

      {guidedStep === 1 && (
        <div className="space-y-3">
          <p className="text-ui text-slate-400">Pick the journey type you want to monitor.</p>
          <div className="grid grid-cols-1 gap-3 md:grid-cols-3">
            {templateTile('homepage_smoke', 'Homepage smoke', 'Open page and assert key element is visible.')}
            {templateTile('login_flow', 'Login flow', 'Open login page, submit form, and validate destination.')}
            <button
              type="button"
              className="glass-well px-4 py-3 text-left transition-colors hover:bg-well-hover"
              onClick={onCustom}
            >
              <p className="text-sm font-medium text-white">Custom</p>
              <p className="mt-1 text-xs text-slate-500">Switch to Expert for full step control.</p>
            </button>
          </div>
        </div>
      )}

      {guidedStep === 2 && (
        <div className="space-y-4">
          <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
            <FormInput
              label={template === 'login_flow' ? 'Login URL' : 'Start URL'}
              value={form.synthetic_browser_start_url}
              onChange={(v) => patch({ synthetic_browser_start_url: v })}
              placeholder={template === 'login_flow' ? 'https://app.example.com/login' : 'https://app.example.com'}
              error={errors.synthetic_browser_start_url}
            />
            {template === 'homepage_smoke' ? (
              <FormInput
                label="Must-see selector"
                value={form.synthetic_browser_guided_must_see_selector}
                onChange={(v) => patch({ synthetic_browser_guided_must_see_selector: v })}
                placeholder="main"
                hint="CSS selector that must be visible."
              />
            ) : (
              <FormInput
                label="Expected URL contains"
                value={form.synthetic_browser_guided_expected_url}
                onChange={(v) => patch({ synthetic_browser_guided_expected_url: v })}
                placeholder="/dashboard"
              />
            )}
          </div>

          {template === 'login_flow' && (
            <>
              <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
                <FormInput
                  label="Email selector"
                  value={form.synthetic_browser_guided_email_selector}
                  onChange={(v) => patch({ synthetic_browser_guided_email_selector: v })}
                  placeholder="[name=email]"
                />
                <FormInput
                  label="Password selector"
                  value={form.synthetic_browser_guided_password_selector}
                  onChange={(v) => patch({ synthetic_browser_guided_password_selector: v })}
                  placeholder="[name=password]"
                />
              </div>
              <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
                <FormInput
                  label="Submit selector"
                  value={form.synthetic_browser_guided_submit_selector}
                  onChange={(v) => patch({ synthetic_browser_guided_submit_selector: v })}
                  placeholder="button[type=submit]"
                />
                <FormInput
                  label="Post-login selector"
                  value={form.synthetic_browser_guided_post_login_selector}
                  onChange={(v) => patch({ synthetic_browser_guided_post_login_selector: v })}
                  placeholder="[data-test=dashboard]"
                />
              </div>
              <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
                <FormInput
                  label="Username (optional)"
                  value={form.synthetic_browser_guided_username}
                  onChange={(v) => patch({ synthetic_browser_guided_username: v })}
                  placeholder="user@example.com"
                  hint="If empty, fill step for username is skipped."
                />
                <FormInput
                  label="Password (optional)"
                  type="password"
                  value={form.synthetic_browser_guided_password}
                  onChange={(v) => patch({ synthetic_browser_guided_password: v })}
                  placeholder="********"
                  hint="If empty, fill step for password is skipped."
                />
              </div>
            </>
          )}

          {errors.synthetic_browser_steps && (
            <p className="text-xs text-rose-300">{errors.synthetic_browser_steps}</p>
          )}
        </div>
      )}

      {preview && (
        <div className="space-y-3">
          <div className="flex flex-wrap items-baseline justify-between gap-2">
            <p className="text-ui text-slate-400">Review generated journey before creating the monitor.</p>
            <p className="flex flex-wrap gap-x-3 text-xs tabular-nums text-slate-500">
              <span>{preview.steps.length} steps</span>
              <span>{Object.keys(preview.variables).length} variables</span>
              <span>Screenshot on failure</span>
            </p>
          </div>
          <div className="glass-well divide-y divide-divider">
            {preview.steps.map((step, idx) => (
              <div key={`guided-preview-step-${idx}`} className="px-4 py-2.5">
                <p className="text-ui font-medium text-slate-200">
                  Step {idx + 1}: {syntheticBrowserActionMeta[step.action].label}
                </p>
                <p className="break-all font-mono text-xs text-slate-500">
                  {step.action === 'goto'
                    ? step.url
                    : step.action === 'assert_url'
                    ? step.value
                    : step.selector}
                </p>
              </div>
            ))}
          </div>
          <p className="text-xs text-slate-500">Click Create monitor at the bottom to save this guided setup.</p>
        </div>
      )}

      <div className="flex items-center justify-between">
        <Button
          type="button"
          variant="ghost"
          onClick={() => setStep((prev) => (prev > 1 ? ((prev - 1) as SyntheticBrowserGuidedStep) : prev))}
          disabled={guidedStep === 1}
        >
          Back
        </Button>
        <Button
          type="button"
          variant="ghost"
          onClick={() => setStep((prev) => (prev < 3 ? ((prev + 1) as SyntheticBrowserGuidedStep) : prev))}
          disabled={guidedStep === 3}
        >
          Next
        </Button>
      </div>
    </div>
  );
}

function ExpertSetup({
  form,
  setForm,
  errors,
  setErrors,
  mode,
  onModeChange,
  liveTest,
}: EditorProps & {
  mode: SyntheticEditorMode;
  onModeChange: (mode: SyntheticEditorMode) => void;
  liveTest: ReactNode;
}) {
  const patch = (p: Partial<MonitorFormState>) => setForm({ ...form, ...p });
  const steps = form.synthetic_browser_steps;
  const setSteps = (next: SyntheticBrowserStepDraft[]) => patch({ synthetic_browser_steps: next });

  const applyTemplate = (template: 'login_flow' | 'homepage_smoke') => {
    setForm((prev) => ({ ...prev, ...syntheticBrowserTemplateFields(template) }));
    setErrors(clearBrowserErrors);
  };

  return (
    <>
      <SyntheticModeHeader
        mode={mode}
        onModeChange={onModeChange}
        facts={[
          `${steps.length} steps`,
          `${(form.synthetic_browser_variables || []).filter((v) => v.key.trim()).length} variables`,
          form.synthetic_browser_screenshot_on_failure ? 'Screenshot on failure' : 'No screenshot',
        ]}
        description="Basic is optimized for common user journeys. Advanced unlocks device/variables/step timeout tuning."
      />

      <SyntheticTemplatesWell
        description="Start from a realistic browser flow and adjust selectors/URLs."
        templates={[
          { label: 'Homepage smoke', onApply: () => applyTemplate('homepage_smoke') },
          { label: 'Login flow', onApply: () => applyTemplate('login_flow') },
        ]}
      />

      {liveTest}

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <FormInput
          label="Start URL"
          value={form.synthetic_browser_start_url}
          onChange={(v) => patch({ synthetic_browser_start_url: v })}
          placeholder="https://app.example.com/login"
          error={errors.synthetic_browser_start_url}
        />
        {mode === 'advanced' ? (
          <FormInput
            label="Device (optional)"
            value={form.synthetic_browser_device}
            onChange={(v) => patch({ synthetic_browser_device: v })}
            placeholder="Desktop Chrome"
          />
        ) : (
          <div>
            <p className={`${FIELD_LABEL} mb-1.5`}>Device profile</p>
            <p className="text-xs text-slate-500">Using saved/default device. Switch to Advanced to customize it.</p>
          </div>
        )}
      </div>

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <FormSelect
          label="Failure mode"
          value={form.synthetic_browser_failure_mode}
          onChange={(v) => patch({ synthetic_browser_failure_mode: v as 'fail_fast' | 'continue' })}
          options={[
            { value: 'fail_fast', label: 'Fail fast' },
            { value: 'continue', label: 'Continue after failures' },
          ]}
          hint="Fail fast is recommended for user journey checks."
        />
        <div>
          <label className={`${FIELD_LABEL} mb-1.5`}>Artifacts on failure</label>
          <div className="glass-well space-y-3 p-4">
            <ToggleRow
              title="Screenshot"
              description="Best default for quick triage in UI."
              checked={form.synthetic_browser_screenshot_on_failure}
              onChange={(v) => patch({ synthetic_browser_screenshot_on_failure: v })}
            />
            <ToggleRow
              title="Trace"
              description="Deep Playwright timeline for debugging."
              checked={form.synthetic_browser_trace_on_failure}
              onChange={(v) => patch({ synthetic_browser_trace_on_failure: v })}
            />
            <ToggleRow
              title="HAR"
              description="Capture network waterfall and payload metadata."
              checked={form.synthetic_browser_har_on_failure}
              onChange={(v) => patch({ synthetic_browser_har_on_failure: v })}
            />
          </div>
        </div>
      </div>

      {mode === 'advanced' && (
        <SyntheticVariablesField
          rows={form.synthetic_browser_variables}
          onChange={(rows) => patch({ synthetic_browser_variables: rows })}
          error={errors.synthetic_browser_variables}
          hint={<>Use <code>{'{{variable_name}}'}</code> inside selectors and values.</>}
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
            onClick={() => setSteps([...steps, defaultSyntheticBrowserStep(steps.length + 1)])}
          >
            Add step
          </Button>
        </div>
        {errors.synthetic_browser_steps && <p className="text-xs text-rose-300">{errors.synthetic_browser_steps}</p>}
        {steps.map((step, stepIdx) => (
          <BrowserStepCard
            key={`synthetic-browser-step-${stepIdx}`}
            step={step}
            index={stepIdx}
            count={steps.length}
            advanced={mode === 'advanced'}
            onChange={(next) => setSteps(withItem(steps, stepIdx, next))}
            onMove={(to) => setSteps(swapped(steps, stepIdx, to))}
            onRemove={() =>
              setSteps(steps.length === 1 ? [defaultSyntheticBrowserStep(1)] : withoutItem(steps, stepIdx))
            }
          />
        ))}
      </div>
    </>
  );
}

function BrowserStepCard({
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
  onMove: (to: number) => void;
  onRemove: () => void;
}) {
  const patchStep = (p: Partial<SyntheticBrowserStepDraft>) => onChange({ ...step, ...p });
  const needsURL = step.action === 'goto';
  const needsSelector =
    step.action === 'click' ||
    step.action === 'fill' ||
    step.action === 'wait_for' ||
    step.action === 'assert_visible' ||
    step.action === 'assert_text';
  const needsValue = step.action === 'fill' || step.action === 'assert_text' || step.action === 'assert_url';
  const actionMeta = syntheticBrowserActionMeta[step.action];
  const stepSummary =
    step.action === 'goto'
      ? step.url || '(missing URL)'
      : step.action === 'assert_url'
      ? step.value || '(missing expected URL)'
      : step.selector || '(missing selector)';

  return (
    <div className="glass-well space-y-4 p-4">
      <div className="flex items-center justify-between">
        <div>
          <p className="text-sm font-medium text-white">Step {index + 1}</p>
          <p className="flex flex-wrap gap-x-3 text-xs text-slate-500">
            <span>{actionMeta.label}</span>
            {stepSummary && <span className="min-w-0 truncate">{stepSummary}</span>}
          </p>
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

      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <FormInput label="Step ID" value={step.id} onChange={(v) => patchStep({ id: v })} placeholder="open_login" />
        <FormSelect
          label="Action"
          value={step.action}
          onChange={(v) => patchStep({ action: v as SyntheticBrowserAction })}
          options={ACTION_OPTIONS}
          hint={actionMeta.hint}
        />
      </div>

      {needsURL && (
        <FormInput
          label="URL"
          value={step.url}
          onChange={(v) => patchStep({ url: v })}
          placeholder="https://app.example.com/login"
        />
      )}

      {needsSelector && (
        <FormInput
          label="Selector"
          value={step.selector}
          onChange={(v) => patchStep({ selector: v })}
          placeholder={actionMeta.selectorPlaceholder || '[data-test=target]'}
          hint="Supports any valid CSS selector."
        />
      )}

      {needsValue && (
        <FormInput
          label={step.action === 'assert_url' ? 'Expected URL / pattern' : 'Value'}
          value={step.value}
          onChange={(v) => patchStep({ value: v })}
          placeholder={step.action === 'assert_url' ? '/dashboard' : actionMeta.valuePlaceholder || 'Text or value'}
        />
      )}

      {advanced && (
        <FormInput
          label="Step timeout (seconds, optional)"
          type="number"
          value={step.timeout_seconds}
          onChange={(v) => patchStep({ timeout_seconds: v })}
          placeholder="20"
          min={1}
        />
      )}
    </div>
  );
}
