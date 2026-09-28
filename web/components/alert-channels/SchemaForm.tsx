'use client';

import { useEffect, useState } from 'react';
import Button from '@/components/ui/Button';
import FormField from '@/components/ui/FormField';
import type { PluginField, PluginManifest } from '@/lib/types';

// What the API substitutes for stored secret values on read. It is only
// present when a value is stored, so it doubles as "there is something to
// clear".
const MASKED_SECRET = '***';

// A null secret in the submitted config asks the API to delete the stored
// value; blank means "keep" (see MergePreserveSecrets). Only optional secrets
// offer it — clearing a required one would just fail validation.
export const CLEARED_SECRET = null;

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
    case 'textarea': {
      // A secret textarea (e.g. webhook custom headers) arrives masked as
      // "***" on edit; show it empty so the mask is never edited into the
      // value, and let an untouched field keep the stored secret.
      const masked = field.secret && value === MASKED_SECRET;
      return (
        <FormField
          label={field.label}
          required={field.required && !(field.secret && isEdit)}
          description={field.help}
          error={error}
        >
          <textarea
            className="input min-h-[120px] resize-y"
            rows={6}
            placeholder={
              field.secret && isEdit ? 'Leave blank to keep existing value' : field.placeholder
            }
            value={masked ? '' : typeof value === 'string' ? value : ''}
            onChange={(e) => onChange(e.target.value)}
          />
          <StoredSecretClear field={field} value={value} isEdit={isEdit} onChange={onChange} />
        </FormField>
      );
    }
    case 'select':
      return (
        <FormField
          label={field.label}
          required={field.required}
          description={field.help}
          error={error}
        >
          <select
            className="input"
            value={typeof value === 'string' ? value : String(field.default ?? '')}
            onChange={(e) => onChange(e.target.value)}
          >
            {(field.options ?? []).map((option) => (
              <option key={option.value} value={option.value}>
                {option.label}
              </option>
            ))}
          </select>
        </FormField>
      );
    case 'email_list':
      return (
        <EmailListField field={field} value={value} error={error} onChange={onChange} />
      );
    case 'secret':
      return (
        <SecretField
          field={field}
          value={value}
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

/**
 * Comma/semicolon/newline separated address input over a string[] value.
 *
 * The separator has to survive keystrokes: parsing on every change and echoing
 * the parsed array back drops the separator the user just typed (splitEmails
 * discards the empty trailing segment), which makes a second address
 * impossible to enter. So the raw text is local state while the field is being
 * edited and only the parsed array travels to the parent.
 */
function EmailListField({
  field,
  value,
  error,
  onChange,
}: {
  field: PluginField;
  value: unknown;
  error?: string;
  onChange: (v: unknown) => void;
}) {
  const incoming = emailListToString(value);
  const [text, setText] = useState(incoming);

  // Adopt values arriving from outside (edit form hydrating, plugin switch,
  // reset) without clobbering in-progress typing: skip when the local text
  // already parses to the same addresses.
  useEffect(() => {
    setText((current) =>
      splitEmails(current).join(', ') === splitEmails(incoming).join(', ') ? current : incoming
    );
  }, [incoming]);

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
        value={text}
        onChange={(e) => {
          setText(e.target.value);
          onChange(splitEmails(e.target.value));
        }}
        onBlur={() => setText(splitEmails(text).join(', '))}
      />
    </FormField>
  );
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
  value: unknown;
  error?: string;
  isEdit: boolean;
  onChange: (v: unknown) => void;
}) {
  const [reveal, setReveal] = useState(false);
  const text = typeof value === 'string' && value !== MASKED_SECRET ? value : '';
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
          value={text}
          onChange={(e) => onChange(e.target.value)}
          autoComplete="off"
        />
        <Button variant="ghost" size="sm" type="button" onClick={() => setReveal(!reveal)}>
          {reveal ? 'Hide' : 'Show'}
        </Button>
      </div>
      <StoredSecretClear field={field} value={value} isEdit={isEdit} onChange={onChange} />
    </FormField>
  );
}

/**
 * Remove a stored optional secret (a webhook's custom headers or signing
 * secret). A blank input keeps the stored value, so without this there is no
 * way to drop one short of recreating the channel.
 */
function StoredSecretClear({
  field,
  value,
  isEdit,
  onChange,
}: {
  field: PluginField;
  value: unknown;
  isEdit: boolean;
  onChange: (v: unknown) => void;
}) {
  if (!isEdit || !field.secret || field.required) return null;
  if (value === CLEARED_SECRET) {
    return (
      <p className="mt-2 flex items-center gap-2 text-xs text-amber-400">
        The stored value will be removed when you save.
        <Button variant="ghost" size="sm" type="button" onClick={() => onChange(MASKED_SECRET)}>
          Undo
        </Button>
      </p>
    );
  }
  if (value !== MASKED_SECRET) return null;
  return (
    <div className="mt-2">
      <Button variant="ghost" size="sm" type="button" onClick={() => onChange(CLEARED_SECRET)}>
        Remove stored value
      </Button>
    </div>
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
