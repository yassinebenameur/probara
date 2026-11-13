'use client';

import { type ReactNode } from 'react';
import { ConnectionMode, DB_TYPE_META, DatabaseMonitorType } from '@/lib/monitor-form/database';
import FormField from '@/components/ui/FormField';
import { FieldHint, FormPanel, ToggleRow } from '@/components/monitors/form-kit';
import { handleRovingKeyDown, SEGMENT_ITEM, SELECTED_SEGMENT } from '@/components/ui/roving';
import { FOCUS_RING } from '@/components/ui/focus';
import { type DatabaseFormData, type SetDatabaseFormData, type ClearedDatabaseSecrets } from '@/components/monitors/database/types';
import SecretInput from '@/components/monitors/database/SecretInput';
import DatabaseTlsMaterial from '@/components/monitors/database/DatabaseTlsMaterial';

const CONNECTION_MODES: { mode: ConnectionMode; label: string }[] = [
  { mode: 'fields', label: 'Form fields' },
  { mode: 'connection_string', label: 'Connection string' },
];
const CONNECTION_MODE_IDS = CONNECTION_MODES.map((m) => m.mode);

interface DatabaseTargetPanelProps {
  type: DatabaseMonitorType;
  formData: DatabaseFormData;
  setFormData: SetDatabaseFormData;
  errors: Record<string, string>;
  usingConnString: boolean;
  /** A stored secret exists and was not cleared. */
  hasStoredConnString: boolean;
  hasStoredPassword: boolean;
  hasStoredClientKey: boolean;
  cleared: ClearedDatabaseSecrets;
  setCleared: (cleared: ClearedDatabaseSecrets) => void;
  /** PEM material only makes sense when the connection can use TLS. */
  tlsMaterialRelevant: boolean;
  hasTLSMaterial: boolean;
  certsOpen: boolean;
  setCertsOpen: (open: boolean) => void;
  /** The Test connection row, at the end of the panel. */
  children: ReactNode;
}

/** DatabaseForm's Target panel: name, connection mode, connection fields per type, and TLS material. */
export default function DatabaseTargetPanel({
  type,
  formData,
  setFormData,
  errors,
  usingConnString,
  hasStoredConnString,
  hasStoredPassword,
  hasStoredClientKey,
  cleared,
  setCleared,
  tlsMaterialRelevant,
  hasTLSMaterial,
  certsOpen,
  setCertsOpen,
  children,
}: DatabaseTargetPanelProps) {
  const meta = DB_TYPE_META[type];

  return (
    <FormPanel title="Target">
      <FormField label="Monitor name" required error={errors.name}>
        <input
          type="text"
          value={formData.name}
          onChange={(e) => setFormData({ ...formData, name: e.target.value })}
          placeholder={`${meta.label} production`}
          className="input"
        />
      </FormField>

      <div>
        <div
          className="mb-2 flex w-fit max-w-full gap-0.5 rounded-xl bg-well p-1 shadow-[inset_0_0_0_1px_var(--glass-edge)]"
          role="radiogroup"
          aria-label="Connection mode"
          onKeyDown={(e) =>
            handleRovingKeyDown(e, CONNECTION_MODE_IDS, formData.connection_mode, (mode) =>
              setFormData({ ...formData, connection_mode: mode })
            )
          }
        >
          {CONNECTION_MODES.map(({ mode, label }) => (
            <button
              key={mode}
              type="button"
              role="radio"
              aria-checked={formData.connection_mode === mode}
              data-roving-id={mode}
              tabIndex={formData.connection_mode === mode ? 0 : -1}
              onClick={() => setFormData({ ...formData, connection_mode: mode })}
              className={`whitespace-nowrap rounded-lg px-3.5 py-1.5 text-ui font-medium ${SEGMENT_ITEM} ${FOCUS_RING} ${
                formData.connection_mode === mode
                  ? SELECTED_SEGMENT
                  : 'text-slate-400 hover:text-slate-100'
              }`}
            >
              {label}
            </button>
          ))}
        </div>
        <p className="text-xs text-slate-500">
          {usingConnString
            ? 'Paste a full connection URI. It is stored encrypted and never shown again.'
            : 'Enter connection details field by field. The password is stored encrypted and never shown again.'}
        </p>
      </div>

      {usingConnString ? (
        <>
          <FormField label="Connection string" required={!hasStoredConnString} error={errors.connection_string}>
            <SecretInput
              value={formData.connection_string}
              onChange={(v) => setFormData({ ...formData, connection_string: v })}
              hasStored={hasStoredConnString}
              placeholder={meta.csPlaceholder}
              storedPlaceholder="Unchanged — paste a new connection string to replace"
              mono
              ariaLabel="Connection string"
            />
            <p className="mt-1 text-xs text-slate-500">{meta.csHint}</p>
          </FormField>
          {meta.supportsTLSToggle && (
            <ToggleRow
              title="Skip TLS certificate verification"
              description="Accept self-signed or mismatched certificates when the URI enables TLS"
              checked={formData.tls_skip_verify}
              onChange={(v) => setFormData({ ...formData, tls_skip_verify: v })}
              tone="warning"
            />
          )}
        </>
      ) : (
        <>
          <div className="grid grid-cols-[1fr_120px] gap-4">
            <FormField label="Host" required error={errors.host}>
              {(control) => (
                <>
                  <input
                    {...control}
                    type="text"
                    value={formData.host}
                    onChange={(e) => setFormData({ ...formData, host: e.target.value })}
                    placeholder={meta.hostPlaceholder}
                    className="input"
                  />
                  <FieldHint>{meta.label} hostname or IP</FieldHint>
                </>
              )}
            </FormField>
            <FormField label="Port" error={errors.port}>
              {(control) => (
                <>
                  <input
                    {...control}
                    type="number"
                    value={formData.port}
                    onChange={(e) => setFormData({ ...formData, port: parseInt(e.target.value, 10) || meta.defaultPort })}
                    min={1}
                    max={65535}
                    className="input"
                  />
                  <FieldHint>Default: {meta.defaultPort}</FieldHint>
                </>
              )}
            </FormField>
          </div>

          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <FormField
              label={meta.usernameRequired ? 'Username' : 'Username (optional)'}
              required={meta.usernameRequired}
              error={errors.username}
            >
              <input
                type="text"
                value={formData.username}
                onChange={(e) => setFormData({ ...formData, username: e.target.value })}
                placeholder={meta.usernameRequired ? 'monitoring' : ''}
                autoComplete="off"
                className="input"
              />
              {type === 'redis' && (
                <p className="mt-1 text-xs text-slate-500">ACL user — leave blank for the default user</p>
              )}
            </FormField>
            <FormField label="Password" error={errors.password}>
              <SecretInput
                value={formData.password}
                onChange={(v) => setFormData({ ...formData, password: v })}
                hasStored={hasStoredPassword}
                onClear={() => setCleared({ ...cleared, password: true })}
                placeholder="••••••••"
                storedPlaceholder="Unchanged"
                ariaLabel="Password"
              />
              {!errors.password && (
                <p className="mt-1 text-xs text-slate-500">
                  {hasStoredPassword ? 'Leave blank to keep the current password' : 'Stored encrypted, never displayed'}
                </p>
              )}
            </FormField>
          </div>

          {type === 'redis' && (
            <FormField label="Database index">
              {(control) => (
                <>
                  <input
                    {...control}
                    type="number"
                    value={formData.db_index}
                    onChange={(e) => setFormData({ ...formData, db_index: parseInt(e.target.value, 10) || 0 })}
                    min={0}
                    max={15}
                    className="input"
                  />
                  <FieldHint>0–15, default 0</FieldHint>
                </>
              )}
            </FormField>
          )}

          {type === 'postgres' && (
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <FormField label="Database">
                {(control) => (
                  <>
                    <input
                      {...control}
                      type="text"
                      value={formData.database}
                      onChange={(e) => setFormData({ ...formData, database: e.target.value })}
                      placeholder="postgres"
                      className="input"
                    />
                    <FieldHint>Default: postgres</FieldHint>
                  </>
                )}
              </FormField>
              <FormField label="TLS (sslmode)">
                <select
                  value={formData.ssl_mode}
                  onChange={(e) => setFormData({ ...formData, ssl_mode: e.target.value as typeof formData.ssl_mode })}
                  className="input"
                >
                  <option value="">Prefer (TLS if available)</option>
                  <option value="disable">Disable</option>
                  <option value="require">Require</option>
                  <option value="verify-full">Require + verify certificate</option>
                </select>
                <p className="mt-1 text-xs text-slate-500">How to negotiate TLS</p>
              </FormField>
            </div>
          )}

          {type === 'mongodb' && (
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <FormField label="Auth source">
                {(control) => (
                  <>
                    <input
                      {...control}
                      type="text"
                      value={formData.auth_source}
                      onChange={(e) => setFormData({ ...formData, auth_source: e.target.value })}
                      placeholder="admin"
                      className="input"
                    />
                    <FieldHint>Database to authenticate against. Default: admin</FieldHint>
                  </>
                )}
              </FormField>
              <FormField label="Replica set (optional)">
                {(control) => (
                  <>
                    <input
                      {...control}
                      type="text"
                      value={formData.replica_set}
                      onChange={(e) => setFormData({ ...formData, replica_set: e.target.value })}
                      placeholder="rs0"
                      className="input"
                    />
                    <FieldHint>Discover the topology and assert a reachable primary</FieldHint>
                  </>
                )}
              </FormField>
            </div>
          )}

          {type === 'rabbitmq' && (
            <FormField label="Virtual host">
              {(control) => (
                <>
                  <input
                    {...control}
                    type="text"
                    value={formData.vhost}
                    onChange={(e) => setFormData({ ...formData, vhost: e.target.value })}
                    placeholder="/"
                    className="input"
                  />
                  <FieldHint>Default: /</FieldHint>
                </>
              )}
            </FormField>
          )}

          {type === 'mysql' && (
            <FormField label="Database (optional)">
              {(control) => (
                <>
                  <input
                    {...control}
                    type="text"
                    value={formData.database}
                    onChange={(e) => setFormData({ ...formData, database: e.target.value })}
                    placeholder="app"
                    className="input"
                  />
                  <FieldHint>Default schema to select after connecting</FieldHint>
                </>
              )}
            </FormField>
          )}

          {meta.supportsTLSToggle && (
            <div className="space-y-2">
              <ToggleRow
                title="Use TLS"
                description={`Connect to ${meta.label} over an encrypted connection`}
                checked={formData.tls_enabled}
                onChange={(v) => setFormData({ ...formData, tls_enabled: v, tls_skip_verify: v ? formData.tls_skip_verify : false })}
              />
              {formData.tls_enabled && (
                <ToggleRow
                  title="Skip certificate verification"
                  description="Accept self-signed or mismatched certificates"
                  checked={formData.tls_skip_verify}
                  onChange={(v) => setFormData({ ...formData, tls_skip_verify: v })}
                  tone="warning"
                />
              )}
            </div>
          )}
        </>
      )}

      {tlsMaterialRelevant && (
        <DatabaseTlsMaterial
          formData={formData}
          setFormData={setFormData}
          errors={errors}
          hasStoredClientKey={hasStoredClientKey}
          cleared={cleared}
          setCleared={setCleared}
          hasTLSMaterial={hasTLSMaterial}
          certsOpen={certsOpen}
          setCertsOpen={setCertsOpen}
        />
      )}

    {children}
    </FormPanel>
  );
}
