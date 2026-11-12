'use client';

import { DBQueryValueOp } from '@/lib/types';
import { DatabaseMonitorType, QUERY_CAPABLE_TYPES } from '@/lib/monitor-form/database';
import { Activity, ChevronDown } from 'lucide-react';
import FormField from '@/components/ui/FormField';
import InfoTip from '@/components/ui/InfoTip';
import Pill from '@/components/ui/Pill';
import { FieldHint, FormPanel, ToggleRow } from '@/components/monitors/form-kit';
import { FOCUS_RING_INSET } from '@/components/ui/focus';
import { type DatabaseFormData, type SetDatabaseFormData } from '@/components/monitors/database/types';

const QUERY_VALUE_OPS: { value: DBQueryValueOp; label: string }[] = [
  { value: 'equals', label: 'equals' },
  { value: 'not_equals', label: 'does not equal' },
  { value: 'contains', label: 'contains' },
  { value: 'number_gt', label: '> (number)' },
  { value: 'number_gte', label: '≥ (number)' },
  { value: 'number_lt', label: '< (number)' },
  { value: 'number_lte', label: '≤ (number)' },
];

interface DatabaseCheckPanelProps {
  type: DatabaseMonitorType;
  formData: DatabaseFormData;
  setFormData: SetDatabaseFormData;
  errors: Record<string, string>;
  /** How many MongoDB cluster checks are on. */
  clusterChecksEnabled: number;
  clusterChecksOpen: boolean;
  setClusterChecksOpen: (open: boolean) => void;
}

/** DatabaseForm's Check panel: the query assertion, latency thresholds and per-type checks. */
export default function DatabaseCheckPanel({
  type,
  formData,
  setFormData,
  errors,
  clusterChecksEnabled,
  clusterChecksOpen,
  setClusterChecksOpen,
}: DatabaseCheckPanelProps) {
  return (
    <FormPanel title="Check">
      {QUERY_CAPABLE_TYPES.includes(type) && (
        <>
          <FormField
            label="Assertion query (optional)"
            error={errors.query}
          >
            {(control) => (
              <>
                <input
                  {...control}
                  type="text"
                  value={formData.query}
                  onChange={(e) => setFormData({ ...formData, query: e.target.value })}
                  placeholder="SELECT 1"
                  className="input font-mono"
                />
                <FieldHint>Runs after connecting; the check fails if it errors or returns 0 rows. Example: SELECT 1</FieldHint>
              </>
            )}
          </FormField>
          {formData.query.trim() !== '' && (
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <FormField label="Value assertion (optional)">
                <select
                  value={formData.query_value_op}
                  onChange={(e) => setFormData({ ...formData, query_value_op: e.target.value as typeof formData.query_value_op })}
                  className="input"
                >
                  <option value="">Row count only (≥ 1 row)</option>
                  {QUERY_VALUE_OPS.map((op) => (
                    <option key={op.value} value={op.value}>
                      Value {op.label}
                    </option>
                  ))}
                </select>
                <p className="mt-1 text-xs text-slate-500">Compare the first column of the first row</p>
              </FormField>
              {formData.query_value_op && (
                <FormField label="Expected value" error={errors.query_value}>
                  <input
                    type="text"
                    value={formData.query_value}
                    onChange={(e) => setFormData({ ...formData, query_value: e.target.value })}
                    placeholder={formData.query_value_op.startsWith('number_') ? '100' : 'ok'}
                    className="input font-mono"
                  />
                </FormField>
              )}
            </div>
          )}
        </>
      )}
      {type === 'redis' && (
        <FormField label="Expected role (optional)">
          <select
            value={formData.expected_role}
            onChange={(e) => setFormData({ ...formData, expected_role: e.target.value as typeof formData.expected_role })}
            className="input"
          >
            <option value="">Any role</option>
            <option value="master">Master</option>
            <option value="replica">Replica</option>
          </select>
          <p className="mt-1 text-xs text-slate-500">Fail if this node&apos;s replication role changes — catches monitoring the wrong node after a failover</p>
        </FormField>
      )}
      {type === 'mongodb' && (
        <div className="glass-well">
          <button
            type="button"
            onClick={() => setClusterChecksOpen(!clusterChecksOpen)}
            className={`flex w-full items-center justify-between gap-3 rounded-[inherit] px-4 py-3 text-left transition-colors hover:bg-well-hover ${FOCUS_RING_INSET}`}
            aria-expanded={clusterChecksOpen}
            aria-controls="db-cluster-checks"
          >
            <span className="flex min-w-0 flex-wrap items-center gap-2 text-ui font-medium text-slate-200">
              <Activity className="h-4 w-4 text-slate-400" strokeWidth={1.75} />
              Cluster checks
              <span className="text-xs font-normal text-slate-500">serverStatus / replSetGetStatus</span>
              <Pill tone="warning" size="xs">
                requires clusterMonitor
              </Pill>
              {clusterChecksEnabled > 0 && (
                <Pill tone="neutral" size="xs">
                  {clusterChecksEnabled} enabled
                </Pill>
              )}
            </span>
            <ChevronDown
              className={`h-4 w-4 shrink-0 text-slate-400 transition-transform duration-base ease-standard motion-reduce:transition-none ${clusterChecksOpen ? 'rotate-180' : ''}`}
              strokeWidth={1.75}
              aria-hidden="true"
            />
          </button>
          {clusterChecksOpen && (
            <div id="db-cluster-checks" className="space-y-3 px-4 pb-4 pt-1">
              <div className="flex items-center gap-1.5 text-xs text-slate-500">
                Read-only admin commands — skipped and flagged when the role is missing, never failing the check.
                <InfoTip inLabel ariaLabel="About cluster checks and permissions">
                  Both commands are covered by MongoDB&apos;s built-in <code>clusterMonitor</code> role — no write or
                  admin privileges: <code>db.grantRolesToUser(&quot;&lt;user&gt;&quot;, [&#123;role: &quot;clusterMonitor&quot;,
                  db: &quot;admin&quot;&#125;])</code>. Without it the data is skipped and flagged on the monitor; only a
                  configured max-lag threshold turns missing replication data into a failure.
                </InfoTip>
              </div>
              <ToggleRow
                title="Replication status"
                description="Member health and replication lag (replica sets only)"
                checked={formData.collect_replication}
                onChange={(v) =>
                  setFormData({
                    ...formData,
                    collect_replication: v,
                    // Lag thresholds are meaningless without the check.
                    max_replication_lag_seconds: v ? formData.max_replication_lag_seconds : '',
                    warn_replication_lag_seconds: v ? formData.warn_replication_lag_seconds : '',
                  })
                }
              />
              <ToggleRow
                title="Connections"
                description="Current and available connections"
                checked={formData.collect_connections}
                onChange={(v) => setFormData({ ...formData, collect_connections: v })}
              />
              <ToggleRow
                title="WiredTiger cache"
                description="Cache used, configured max, and dirty bytes"
                checked={formData.collect_cache}
                onChange={(v) => setFormData({ ...formData, collect_cache: v })}
              />
              <ToggleRow
                title="Memory"
                description="Resident and virtual memory"
                checked={formData.collect_memory}
                onChange={(v) => setFormData({ ...formData, collect_memory: v })}
              />
              <ToggleRow
                title="Network & operations"
                description="Network I/O and operation counters"
                checked={formData.collect_network}
                onChange={(v) => setFormData({ ...formData, collect_network: v })}
              />
              <ToggleRow
                title="Process CPU"
                description="CPU time consumed by mongod (Linux servers)"
                checked={formData.collect_cpu}
                onChange={(v) => setFormData({ ...formData, collect_cpu: v })}
              />
              {formData.collect_replication && (
                <div className="grid grid-cols-1 gap-4 pt-1 sm:grid-cols-2">
                  <FormField
                    label="Warn replication lag (s)"
                    error={errors.warn_replication_lag_seconds}
                    infoTip="Optional. Flags the check with a warning above this lag — without failing it."
                  >
                    <input
                      type="number"
                      value={formData.warn_replication_lag_seconds}
                      onChange={(e) => setFormData({ ...formData, warn_replication_lag_seconds: e.target.value })}
                      min={1}
                      placeholder="e.g. 10"
                      className="input"
                    />
                  </FormField>
                  <FormField
                    label="Max replication lag (s)"
                    error={errors.max_replication_lag_seconds}
                    infoTip="Optional. Fails the check when lag exceeds this — or when replication status can't be read at all (missing role, standalone, no primary) — and triggers availability alerts."
                  >
                    <input
                      type="number"
                      value={formData.max_replication_lag_seconds}
                      onChange={(e) => setFormData({ ...formData, max_replication_lag_seconds: e.target.value })}
                      min={1}
                      placeholder="e.g. 30"
                      className="input"
                    />
                  </FormField>
                </div>
              )}
            </div>
          )}
        </div>
      )}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <FormField
          label="Warn latency (ms)"
          error={errors.warn_latency_ms}
          infoTip="Optional. Flags the check with a warning above this round-trip time — without failing it."
        >
          <input
            type="number"
            value={formData.warn_latency_ms}
            onChange={(e) => setFormData({ ...formData, warn_latency_ms: e.target.value })}
            min={1}
            placeholder="e.g. 100"
            className="input"
          />
        </FormField>
        <FormField
          label="Max latency (ms)"
          error={errors.max_latency_ms}
          infoTip="Optional. Fails the check if the round trip takes longer than this — triggers availability alerts."
        >
          <input
            type="number"
            value={formData.max_latency_ms}
            onChange={(e) => setFormData({ ...formData, max_latency_ms: e.target.value })}
            min={1}
            placeholder="e.g. 250"
            className="input"
          />
        </FormField>
      </div>
    </FormPanel>
  );
}
