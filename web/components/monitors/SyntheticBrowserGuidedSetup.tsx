'use client';

import type { FormErrors } from '@/lib/monitor-form/common';
import {
  type SyntheticBrowserFormData,
  type SyntheticBrowserGuidedInputs,
  type SyntheticBrowserGuidedJourney,
  type SyntheticBrowserGuidedTemplate,
  syntheticBrowserActionMeta,
} from '@/lib/monitor-form/synthetic-browser';
import { BTN_GHOST_SM, FormInput, SummaryChip } from './MonitorFormControls';

export type GuidedWizardStep = 1 | 2 | 3;

const WIZARD_LABELS: Record<GuidedWizardStep, string> = { 1: '1. Journey', 2: '2. Inputs', 3: '3. Review' };

interface SyntheticBrowserGuidedSetupProps {
  value: SyntheticBrowserFormData;
  journey: SyntheticBrowserGuidedJourney;
  errors: FormErrors;
  wizardStep: GuidedWizardStep;
  onWizardStepChange: (step: GuidedWizardStep) => void;
  onStartURLChange: (url: string) => void;
  onInputsChange: (patch: Partial<SyntheticBrowserGuidedInputs>) => void;
  onPickTemplate: (template: SyntheticBrowserGuidedTemplate) => void;
  onPickCustom: () => void;
}

/** Three-step wizard: pick a journey template, fill its inputs, review the generated steps. */
export default function SyntheticBrowserGuidedSetup({
  value,
  journey,
  errors,
  wizardStep,
  onWizardStepChange,
  onStartURLChange,
  onInputsChange,
  onPickTemplate,
  onPickCustom,
}: SyntheticBrowserGuidedSetupProps) {
  const inputs = value.guided;
  const isLogin = inputs.template === 'login_flow';

  const templateCard = (template: SyntheticBrowserGuidedTemplate, title: string, description: string) => (
    <button
      type="button"
      className={`rounded-lg border px-3 py-3 text-left transition-colors ${
        inputs.template === template ? 'border-cyan-400/50 bg-cyan-500/10' : 'border-white/[0.08] bg-slate-800/40 hover:border-white/[0.16]'
      }`}
      onClick={() => onPickTemplate(template)}
    >
      <p className="text-sm font-medium text-white">{title}</p>
      <p className="mt-1 text-xs text-slate-500">{description}</p>
    </button>
  );

  return (
    <div className="space-y-4">
      <div className="rounded-lg border border-white/[0.08] bg-slate-900/40 p-4">
        <div className="mb-4 flex items-center gap-2">
          {([1, 2, 3] as const).map((step) => (
            <button
              key={step}
              type="button"
              onClick={() => onWizardStepChange(step)}
              className={`rounded-full px-2.5 py-1 text-xs transition-colors ${
                wizardStep === step
                  ? 'bg-cyan-500/20 text-cyan-200 border border-cyan-400/40'
                  : 'bg-slate-800/60 text-slate-400 border border-white/[0.08]'
              }`}
            >
              {WIZARD_LABELS[step]}
            </button>
          ))}
        </div>

        {wizardStep === 1 && (
          <div className="space-y-3">
            <p className="text-xs text-slate-400">Pick the journey type you want to monitor.</p>
            <div className="grid grid-cols-1 gap-3 md:grid-cols-3">
              {templateCard('homepage_smoke', 'Homepage Smoke', 'Open page and assert key element is visible.')}
              {templateCard('login_flow', 'Login Flow', 'Open login page, submit form, and validate destination.')}
              <button
                type="button"
                className="rounded-lg border border-white/[0.08] bg-slate-800/40 px-3 py-3 text-left transition-colors hover:border-white/[0.16]"
                onClick={onPickCustom}
              >
                <p className="text-sm font-medium text-white">Custom</p>
                <p className="mt-1 text-xs text-slate-500">Switch to Expert for full step control.</p>
              </button>
            </div>
          </div>
        )}

        {wizardStep === 2 && (
          <div className="space-y-4">
            <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
              <FormInput
                label={isLogin ? 'Login URL' : 'Start URL'}
                value={value.start_url}
                onChange={onStartURLChange}
                placeholder={isLogin ? 'https://app.example.com/login' : 'https://app.example.com'}
                error={errors.synthetic_browser_start_url}
              />
              {inputs.template === 'homepage_smoke' ? (
                <FormInput
                  label="Must-see selector"
                  value={inputs.must_see_selector}
                  onChange={(must_see_selector) => onInputsChange({ must_see_selector })}
                  placeholder="main"
                  hint="CSS selector that must be visible."
                />
              ) : (
                <FormInput
                  label="Expected URL contains"
                  value={inputs.expected_url}
                  onChange={(expected_url) => onInputsChange({ expected_url })}
                  placeholder="/dashboard"
                />
              )}
            </div>

            {isLogin && (
              <>
                <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
                  <FormInput
                    label="Email selector"
                    value={inputs.email_selector}
                    onChange={(email_selector) => onInputsChange({ email_selector })}
                    placeholder="[name=email]"
                  />
                  <FormInput
                    label="Password selector"
                    value={inputs.password_selector}
                    onChange={(password_selector) => onInputsChange({ password_selector })}
                    placeholder="[name=password]"
                  />
                </div>
                <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
                  <FormInput
                    label="Submit selector"
                    value={inputs.submit_selector}
                    onChange={(submit_selector) => onInputsChange({ submit_selector })}
                    placeholder="button[type=submit]"
                  />
                  <FormInput
                    label="Post-login selector"
                    value={inputs.post_login_selector}
                    onChange={(post_login_selector) => onInputsChange({ post_login_selector })}
                    placeholder="[data-test=dashboard]"
                  />
                </div>
                <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
                  <FormInput
                    label="Username (optional)"
                    value={inputs.username}
                    onChange={(username) => onInputsChange({ username })}
                    placeholder="user@example.com"
                    hint="If empty, fill step for username is skipped."
                  />
                  <FormInput
                    label="Password (optional)"
                    type="password"
                    value={inputs.password}
                    onChange={(password) => onInputsChange({ password })}
                    placeholder="********"
                    hint="If empty, fill step for password is skipped."
                  />
                </div>
              </>
            )}

            {errors.synthetic_browser_steps && <p className="text-xs text-rose-400">{errors.synthetic_browser_steps}</p>}
          </div>
        )}

        {wizardStep === 3 && (
          <div className="space-y-3">
            <p className="text-xs text-slate-400">Review generated journey before creating the monitor.</p>
            <div className="flex flex-wrap gap-2 text-xs">
              <SummaryChip tone="strong">{journey.steps.length} steps</SummaryChip>
              <SummaryChip tone="strong">{Object.keys(journey.variables).length} variables</SummaryChip>
              <SummaryChip tone="strong">Screenshot on failure</SummaryChip>
            </div>
            <div className="space-y-2">
              {journey.steps.map((step, idx) => (
                <div key={idx} className="rounded-lg border border-white/[0.08] bg-slate-800/40 px-3 py-2">
                  <p className="text-xs font-medium text-slate-300">
                    Step {idx + 1}: {syntheticBrowserActionMeta[step.action].label}
                  </p>
                  <p className="text-[11px] text-slate-500">
                    {step.action === 'goto' ? step.url : step.action === 'assert_url' ? step.value : step.selector}
                  </p>
                </div>
              ))}
            </div>
            <p className="text-xs text-slate-500">Click Create Monitor at the bottom to save this guided setup.</p>
          </div>
        )}

        <div className="mt-4 flex items-center justify-between border-t border-white/[0.08] pt-3">
          <button
            type="button"
            className={BTN_GHOST_SM}
            onClick={() => wizardStep > 1 && onWizardStepChange((wizardStep - 1) as GuidedWizardStep)}
            disabled={wizardStep === 1}
          >
            Back
          </button>
          <button
            type="button"
            className={BTN_GHOST_SM}
            onClick={() => wizardStep < 3 && onWizardStepChange((wizardStep + 1) as GuidedWizardStep)}
            disabled={wizardStep === 3}
          >
            Next
          </button>
        </div>
      </div>
    </div>
  );
}
