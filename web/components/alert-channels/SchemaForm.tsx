'use client';

import { useState } from 'react';
import Button from '@/components/ui/Button';
import FormField from '@/components/ui/FormField';
import type { PluginField, PluginManifest } from '@/lib/types';

interface SchemaFormProps {
  manifest: PluginManifest;
  value: Record<string, unknown>;
  onChange: (next: Record<string, unknown>) => void;
  errors: Record<string, string>;
  /**
   * Existing channel — when set, secret fields render empty with a
   * "leave blank to keep current value" placeholder and the parent should
   * drop empty secret fields from the submitted payload.
   */
  isEdit?: boolean;
}

export default function SchemaForm({
  manifest,
  value,
  onChange,
  errors,
  isEdit = false,
}: SchemaFormProps) {
  const update = (key: string, v: unknown) => onChange({ ...value, [key]: v });

  return (
    <div className="space-y-4">
      {manifest.fields.map((field) => (
        <FieldRenderer
          key={field.key}
          field={field}
          value={value[field.key]}
          error={errors[field.key]}
          isEdit={isEdit}
          onChange={(v) => update(field.key, v)}
        />
      ))}
    </div>
  );
}

interface FieldRendererProps {
  field: PluginField;
  value: unknown;
  error?: string;
  isEdit: boolean;
  onChange: (v: unknown) => void;
}

function FieldRenderer({ field, value, error, isEdit, onChange }: FieldRendererProps) {
  switch (field.type) {
    case 'bool':
      return (
        <BoolField field={field} value={Boolean(value)} onChange={onChange} error={error} />
      );
    case 'textarea':
      return (
        <FormField
          label={field.label}
          required={field.required}
          description={field.help}
          error={error}
        >
          <textarea
            className="input min-h-[120px] resize-y"
            rows={6}
            placeholder={field.placeholder}
            value={typeof value === 'string' ? value : ''}
            onChange={(e) => onChange(e.target.value)}
          />
        </FormField>
      );
    case 'email_list':
      return (
        <FormField
          label={field.label}
          required={field.required}
          description={field.help ?? 'Separate emails with commas, semicolons, or new lines.'}
          error={error}
        >
          <input
            type="text"
            className="input"
            placeholder={field.placeholder ?? 'oncall@example.com, team@example.com'}
            value={emailListToString(value)}
            onChange={(e) => onChange(splitEmails(e.target.value))}
          />
        </FormField>
      );
    case 'secret':
      return (
        <SecretField
          field={field}
          value={typeof value === 'string' ? value : ''}
          error={error}
          isEdit={isEdit}
          onChange={onChange}
        />
      );
    case 'url':
    case 'string':
    default:
      return (
        <FormField
          label={field.label}
          required={field.required}
          description={field.help}
          error={error}
        >
          <input
            type={field.type === 'url' ? 'url' : 'text'}
            className="input"
            placeholder={field.placeholder}
            value={typeof value === 'string' ? value : ''}
            onChange={(e) => onChange(e.target.value)}
          />
        </FormField>
      );
  }
}

function BoolField({
  field,
  value,
  error,
  onChange,
}: {
  field: PluginField;
  value: boolean;
  error?: string;
  onChange: (v: boolean) => void;
}) {
  return (
    <div className="rounded-lg border border-white/[0.06] bg-slate-900/40 px-4 py-3">
      <div className="flex items-center justify-between">
        <div>
          <p className="text-sm font-medium text-white">{field.label}</p>
          {field.help && <p className="text-xs text-slate-500">{field.help}</p>}
        </div>
        <button
          type="button"
          onClick={() => onChange(!value)}
          className={`relative h-5 w-9 rounded-full transition-colors ${
            value ? 'bg-cyan-500' : 'bg-slate-700'
          }`}
          aria-pressed={value}
          aria-label={`Toggle ${field.label}`}
        >
          <span
            className={`absolute left-0.5 top-0.5 h-4 w-4 rounded-full bg-white transition-transform ${
              value ? 'translate-x-4' : ''
            }`}
          />
        </button>
      </div>
      {error && <p className="mt-2 text-xs text-rose-400">{error}</p>}
    </div>
  );
}

function SecretField({
  field,
  value,
  error,
  isEdit,
  onChange,
}: {
  field: PluginField;
  value: string;
  error?: string;
  isEdit: boolean;
  onChange: (v: string) => void;
}) {
  const [reveal, setReveal] = useState(false);
  const placeholder = isEdit
    ? 'Leave blank to keep existing value'
    : field.placeholder;

  return (
    <FormField
      label={field.label}
      required={field.required && !isEdit}
      description={field.help}
      error={error}
    >
      <div className="flex items-center gap-2">
        <input
          type={reveal ? 'text' : 'password'}
          className="input"
          placeholder={placeholder}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          autoComplete="off"
        />
        <Button variant="ghost" size="sm" type="button" onClick={() => setReveal(!reveal)}>
          {reveal ? 'Hide' : 'Show'}
        </Button>
      </div>
    </FormField>
  );
}

function emailListToString(value: unknown): string {
  if (Array.isArray(value)) return value.join(', ');
  if (typeof value === 'string') return value;
  return '';
}

function splitEmails(input: string): string[] {
  return input
    .split(/[,\n;]+/)
    .map((s) => s.trim())
    .filter(Boolean);
}
