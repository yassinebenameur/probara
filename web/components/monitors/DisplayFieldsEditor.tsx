'use client';

import { Plus, X } from 'lucide-react';
import Button from '@/components/ui/Button';
import FormField from '@/components/ui/FormField';
import IconButton from '@/components/ui/IconButton';
import { HTTPDisplayField, MAX_HTTP_DISPLAY_FIELDS } from '@/lib/types';

const COLUMNS = { gridTemplateColumns: '2fr 1fr 2rem' };

// Editor for HTTP display_fields: values read from the JSON response and shown
// next to the monitor's name on the monitors list. Never assertions.
export default function DisplayFieldsEditor({
  value,
  onChange,
  error,
}: {
  value: HTTPDisplayField[];
  onChange: (fields: HTTPDisplayField[]) => void;
  error?: string;
}) {
  const update = (idx: number, patch: Partial<HTTPDisplayField>) =>
    onChange(value.map((f, i) => (i === idx ? { ...f, ...patch } : f)));
  const full = value.length >= MAX_HTTP_DISPLAY_FIELDS;

  return (
    <FormField label="List display values (optional)" error={error}>
      {value.length > 0 && (
        <div className="mb-2 space-y-2">
          <div className="grid gap-2 text-[11px] text-slate-500" style={COLUMNS}>
            <span>JSON path</span>
            <span>Label</span>
          </div>
          {value.map((f, idx) => (
            <div key={`display-${idx}`} className="grid gap-2 items-center" style={COLUMNS}>
              <input
                className="input font-mono"
                placeholder="version"
                aria-label="JSON path"
                value={f.path}
                onChange={(e) => update(idx, { path: e.target.value })}
              />
              <input
                className="input"
                placeholder="None"
                aria-label="Label"
                maxLength={32}
                value={f.label || ''}
                onChange={(e) => update(idx, { label: e.target.value })}
              />
              <IconButton
                label="Remove display value"
                icon={<X strokeWidth={1.75} />}
                onClick={() => onChange(value.filter((_, i) => i !== idx))}
              />
            </div>
          ))}
        </div>
      )}

      <Button
        type="button"
        variant="ghost"
        size="xs"
        icon={<Plus className="h-3.5 w-3.5" strokeWidth={1.75} />}
        disabled={full}
        onClick={() => onChange([...value, { label: '', path: '' }])}
      >
        {full ? `Up to ${MAX_HTTP_DISPLAY_FIELDS} values` : 'Add value'}
      </Button>

      {!error && (
        <p className="mt-1 text-xs text-slate-500">
          Shown next to the name in the monitors list, from the latest check (gjson path, e.g.{' '}
          <code className="text-slate-300">checks.db</code>). Leave the label empty to show only the value.
        </p>
      )}
    </FormField>
  );
}
