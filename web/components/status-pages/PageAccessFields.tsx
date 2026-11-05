'use client';

import { useId } from 'react';
import type { StatusPageVisibility } from '@/lib/types';
import { VISIBILITY_OPTIONS, isGatedVisibility } from '@/lib/status-page-access';
import SegmentedControl from '@/components/ui/SegmentedControl';
import FormField from '@/components/ui/FormField';
import Button from '@/components/ui/Button';

interface PageAccessFieldsProps {
  visibility: StatusPageVisibility;
  /** A password is stored on the saved page. */
  hasStoredPassword: boolean;
  password: string;
  clearPassword: boolean;
  error?: string;
  onVisibilityChange: (value: StatusPageVisibility) => void;
  onPasswordChange: (value: string) => void;
  onClearPasswordChange: (value: boolean) => void;
}

/**
 * Who may view the public page: the visibility choice and the write-only
 * visitor password. Kiosk tokens live on the edit page (KioskTokensPanel)
 * because each is shown once, outside the form's save cycle.
 */
export default function PageAccessFields({
  visibility,
  hasStoredPassword,
  password,
  clearPassword,
  error,
  onVisibilityChange,
  onPasswordChange,
  onClearPasswordChange,
}: PageAccessFieldsProps) {
  const headingId = useId();
  const describedId = useId();
  const current = VISIBILITY_OPTIONS.find((o) => o.value === visibility) ?? VISIBILITY_OPTIONS[0];
  const storedPassword = hasStoredPassword && !clearPassword;

  return (
    <div className="glass-well space-y-3 p-4">
      <div>
        <p id={headingId} className="mb-2 text-ui font-medium text-white">
          Visibility
        </p>
        <SegmentedControl
          options={VISIBILITY_OPTIONS.map(({ value, label }) => ({ value, label }))}
          value={visibility}
          onChange={onVisibilityChange}
          labelledBy={headingId}
          describedBy={describedId}
          wrap
        />
        <p id={describedId} className="mt-2 text-xs text-slate-400">
          {current.description}
        </p>
      </div>

      {visibility === 'password' && (
        <FormField
          label={storedPassword ? 'New password' : 'Password'}
          required={!storedPassword}
          error={error}
          description={
            storedPassword
              ? 'Leave blank to keep the current password.'
              : 'At least 8 characters. Share it with the people who should see the page.'
          }
        >
          <input
            type="password"
            value={password}
            onChange={(event) => onPasswordChange(event.target.value)}
            autoComplete="new-password"
            className="input"
            placeholder={storedPassword ? '••••••••' : 'Visitor password'}
          />
        </FormField>
      )}

      {visibility !== 'password' && error && (
        <p className="text-xs text-rose-300" role="alert">
          {error}
        </p>
      )}

      {visibility !== 'password' && hasStoredPassword && (
        <div className="flex flex-wrap items-center gap-2 text-xs text-slate-400">
          {clearPassword ? (
            <>
              <span>The stored password will be removed when you save.</span>
              <Button variant="subtle" size="xs" type="button" onClick={() => onClearPasswordChange(false)}>
                Keep it
              </Button>
            </>
          ) : (
            <>
              <span>A visitor password is still stored for this page.</span>
              <Button variant="subtle" size="xs" type="button" onClick={() => onClearPasswordChange(true)}>
                Remove stored password
              </Button>
            </>
          )}
        </div>
      )}

      {isGatedVisibility(visibility) && (
        <p className="text-xs text-slate-500">
          Needs <code className="font-mono">STATUS_PAGE_SESSION_SECRET</code> on the status-page service; without it the
          page is unavailable. Browser notifications are offered on public and unlisted pages only.
        </p>
      )}
    </div>
  );
}
