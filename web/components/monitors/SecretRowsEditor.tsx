'use client';

import { Eye, EyeOff, Lock, LockOpen, Plus, X } from 'lucide-react';
import Button from '@/components/ui/Button';
import IconButton from '@/components/ui/IconButton';
import {
  ForceSecret,
  SecretKV,
  isSecretRow,
  keepsStoredValue,
  renameSecretRow,
  toggleSecretRow,
} from '@/lib/monitor-form/secret-map';

const STORED_PLACEHOLDER = 'Stored value (leave blank to keep)';

/**
 * Key/value rows of a map whose values may be secrets (lib/monitor-form/
 * secret-map.ts holds the rules). With `toggleable`, each row has a lock to
 * send it to the secret map; `forceSecret` names rows that are always secret
 * (credential headers the API moves there anyway), whose lock is fixed on.
 * Without `toggleable` every row is secret (secret variables). A stored
 * secret shows blank with a placeholder and is kept until edited or renamed.
 */
export default function SecretRowsEditor({
  rows,
  onChange,
  toggleable,
  forceSecret,
  itemNoun,
  namePlaceholder,
  valuePlaceholder,
  keepOneRow = false,
}: {
  rows: SecretKV[];
  onChange: (rows: SecretKV[]) => void;
  toggleable: boolean;
  forceSecret?: ForceSecret;
  /** Singular, lowercase: "header", "metadata entry", "secret variable". */
  itemNoun: string;
  namePlaceholder: string;
  valuePlaceholder: string;
  /** Removing the last row blanks it instead (lists that always show a row). */
  keepOneRow?: boolean;
}) {
  const setRow = (idx: number, row: SecretKV) => {
    const next = [...rows];
    next[idx] = row;
    onChange(next);
  };
  const blankRow = (): SecretKV => ({ key: '', value: '', reveal: false, secret: !toggleable });
  const Noun = itemNoun.charAt(0).toUpperCase() + itemNoun.slice(1);

  return (
    <div className="space-y-2">
      {rows.map((row, idx) => {
        const forced = Boolean(forceSecret?.(row.key));
        const secret = toggleable ? isSecretRow(row, forceSecret) : true;
        const stored = toggleable ? keepsStoredValue(row, forceSecret) : Boolean(row.hasStoredValue);
        return (
          <div key={idx} className="flex items-center gap-2">
            <input
              className="input min-w-0 flex-1 font-mono"
              placeholder={namePlaceholder}
              aria-label={`${Noun} ${idx + 1} name`}
              value={row.key}
              onChange={(e) => setRow(idx, renameSecretRow(row, e.target.value))}
            />
            <input
              className="input min-w-0 flex-[2] font-mono"
              placeholder={stored ? STORED_PLACEHOLDER : valuePlaceholder}
              aria-label={`${Noun} ${idx + 1} value`}
              type={secret && !row.reveal ? 'password' : 'text'}
              autoComplete="off"
              value={row.value}
              onChange={(e) => setRow(idx, { ...row, value: e.target.value })}
            />
            {secret && (
              <IconButton
                size="sm"
                icon={row.reveal ? <EyeOff strokeWidth={1.75} /> : <Eye strokeWidth={1.75} />}
                label={row.reveal ? `Hide ${itemNoun} ${idx + 1} value` : `Show ${itemNoun} ${idx + 1} value`}
                onClick={() => setRow(idx, { ...row, reveal: !row.reveal })}
              />
            )}
            {toggleable && (
              <IconButton
                size="sm"
                icon={secret ? <Lock strokeWidth={1.75} /> : <LockOpen strokeWidth={1.75} />}
                label={
                  forced
                    ? `${Noun} ${idx + 1} carries a credential, so it is always stored encrypted`
                    : secret
                    ? `Store ${itemNoun} ${idx + 1} as plain text`
                    : `Store ${itemNoun} ${idx + 1} encrypted (write-only)`
                }
                disabled={forced}
                onClick={() => setRow(idx, toggleSecretRow(row, !row.secret))}
              />
            )}
            <IconButton
              size="sm"
              icon={<X strokeWidth={1.75} />}
              label={`Remove ${itemNoun} ${idx + 1}`}
              danger
              onClick={() => {
                if (keepOneRow && rows.length === 1) {
                  onChange([blankRow()]);
                } else {
                  onChange(rows.filter((_, j) => j !== idx));
                }
              }}
            />
          </div>
        );
      })}
      <Button
        type="button"
        variant="ghost"
        size="xs"
        icon={<Plus className="h-3.5 w-3.5" strokeWidth={1.75} />}
        onClick={() => onChange([...rows, blankRow()])}
      >
        Add {itemNoun}
      </Button>
    </div>
  );
}
