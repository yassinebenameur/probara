'use client';

import { Location } from '@/lib/types';
import { useConfigTest } from '@/components/monitors/useConfigTest';
import { CheckCircle2, Loader2, PlugZap, XCircle } from 'lucide-react';
import Button from '@/components/ui/Button';
import { type DatabaseFormData } from '@/components/monitors/database/types';

interface DatabaseTestRowProps {
  formData: DatabaseFormData;
  configTest: ReturnType<typeof useConfigTest>;
  handleTestConnection: () => void;
  canTestConnection: boolean;
  /** The form is saving. */
  loading: boolean;
  availableLocations: Location[];
}

/** DatabaseForm's Test connection row: the button, the location to test from and the last result. */
export default function DatabaseTestRow({
  formData,
  configTest,
  handleTestConnection,
  canTestConnection,
  loading,
  availableLocations,
}: DatabaseTestRowProps) {
  const test = configTest.state;

  return (
    <div className="glass-well flex flex-wrap items-center gap-3 px-4 py-3">
      <Button
        type="button"
        onClick={handleTestConnection}
        disabled={!canTestConnection || test.phase === 'running' || loading}
        title={!canTestConnection ? 'Write access is required to test saved connections' : undefined}
        icon={
          test.phase === 'running' ? (
            <Loader2 className="h-4 w-4 animate-spin" strokeWidth={1.75} />
          ) : (
            <PlugZap className="h-4 w-4" strokeWidth={1.75} />
          )
        }
      >
        {test.phase === 'running' ? 'Testing…' : 'Test connection'}
      </Button>
      {formData.location_ids.length > 0 && (
        <select
          aria-label="Test from location"
          value={configTest.locationId}
          onChange={(e) => configTest.setLocationId(e.target.value)}
          className="input input-sm !w-auto"
        >
          <option value="">Test from: default fleet</option>
          {availableLocations
            .filter((loc) => formData.location_ids.includes(loc.id))
            .map((loc) => (
              <option key={loc.id} value={loc.id}>Test from: {loc.name}</option>
            ))}
        </select>
      )}
      {test.phase === 'idle' && (
        <p className="text-xs text-slate-500">Runs one check from a worker without saving anything.</p>
      )}
      {test.phase === 'done' && test.result.status === 'success' && (
        <p className="flex items-center gap-1.5 text-xs text-emerald-300">
          <CheckCircle2 className="h-3.5 w-3.5" strokeWidth={1.75} />
          Connected{typeof test.result.latency_ms === 'number' ? ` in ${test.result.latency_ms}ms` : ''}
        </p>
      )}
      {test.phase === 'done' &&
        test.result.status === 'success' &&
        test.result.metrics_data?.mongodb?.unavailable?.some((u) => u.reason === 'unauthorized') && (
          <p className="text-xs text-amber-300">
            Cluster checks skipped — grant the monitoring user the clusterMonitor role
          </p>
        )}
      {test.phase === 'done' && test.result.status !== 'success' && (
        <p className="flex min-w-0 items-center gap-1.5 text-xs text-rose-300">
          <XCircle className="h-3.5 w-3.5 shrink-0" strokeWidth={1.75} />
          <span className="truncate" title={test.result.error_message}>
            {test.result.error_message || 'Check failed'}
          </span>
        </p>
      )}
      {test.phase === 'request_failed' && (
        <p className="flex min-w-0 items-center gap-1.5 text-xs text-amber-300">
          <XCircle className="h-3.5 w-3.5 shrink-0" strokeWidth={1.75} />
          <span className="truncate" title={test.message}>{test.message}</span>
        </p>
      )}
    </div>
  );
}
