'use client';

import { useState, useCallback, useRef } from 'react';
import { useRouter } from 'next/navigation';
import { AlertTriangle, ArrowRight, Check, Loader2, UploadCloud, X } from 'lucide-react';
import PageHeader from '@/components/ui/PageHeader';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import Pill from '@/components/ui/Pill';
import TypeBadge from '@/components/ui/TypeBadge';
import { previewImport, executeImport } from '@/lib/api';
import { monitorTypeLabel } from '@/lib/monitor-type-labels';
import type {
  ImportPreviewResponse,
  ImportRow,
  FieldMapping,
  ImportExecuteResponse,
  ImportRowResult,
  ImportSkippedRow
} from '@/lib/types';
import { IMPORT_SCHEMA_PORTABLE, IMPORT_SCHEMA_UPTIME_KUMA } from '@/lib/types';

// Recognized schemas arrive pre-translated: every field is already mapped, so
// the mapping step is informational rather than something to fill in.
const PRE_MAPPED_SCHEMAS: string[] = [IMPORT_SCHEMA_PORTABLE, IMPORT_SCHEMA_UPTIME_KUMA];

function isPreMapped(schema?: string): boolean {
  return !!schema && PRE_MAPPED_SCHEMAS.includes(schema);
}

function describeSource(preview: ImportPreviewResponse): string {
  switch (preview.schema) {
    case IMPORT_SCHEMA_PORTABLE:
      return `${preview.total_rows} monitors from a portable YAML export`;
    case IMPORT_SCHEMA_UPTIME_KUMA:
      return `${preview.total_rows} monitors translated from an Uptime Kuma export`;
    default:
      return `${preview.total_rows} rows in ${preview.format.toUpperCase()} format`;
  }
}

// Target fields that can be mapped to
const TARGET_FIELDS = [
  { key: 'name', label: 'Name', required: true },
  { key: 'type', label: 'Type', required: false },
  { key: 'config', label: 'Raw Config', required: false },
  { key: 'url', label: 'URL (HTTP)', required: false },
  { key: 'method', label: 'Method (HTTP)', required: false },
  { key: 'expected_status', label: 'Expected Status (HTTP)', required: false },
  { key: 'expected_body', label: 'Expected Body (HTTP)', required: false },
  { key: 'host', label: 'Host (Ping/DNS/gRPC)', required: false },
  { key: 'port', label: 'Port (gRPC)', required: false },
  { key: 'service', label: 'Service (gRPC)', required: false },
  { key: 'use_tls', label: 'Use TLS (gRPC)', required: false },
  { key: 'interval_seconds', label: 'Interval (seconds)', required: false },
  { key: 'timeout_seconds', label: 'Timeout (seconds)', required: false },
  { key: 'tags', label: 'Tags', required: false },
  { key: 'enabled', label: 'Enabled', required: false },
  { key: 'group_members', label: 'Group Members', required: false },
  { key: 'alert_policy_names', label: 'Alert Policy Names', required: false },
  { key: 'consecutive_failures_threshold', label: 'Retries before down', required: false },
];

type WizardStep = 'upload' | 'mapping' | 'review' | 'importing' | 'results';

// Supported monitor types for mapping
const SUPPORTED_TYPES = [
  { value: 'http', description: 'HTTP/HTTPS endpoint monitoring' },
  { value: 'ping', description: 'ICMP ping checks' },
  { value: 'dns', description: 'DNS record checks' },
  { value: 'grpc', description: 'gRPC health checks' },
  { value: 'tcp', description: 'TCP connect (host:port) checks' },
  { value: 'group', description: 'Group of monitors' },
  { value: 'agent', description: 'Heartbeat checks from installed agents' },
  { value: 'push', description: 'Token-based push heartbeat checks' },
  { value: 'sip', description: 'SIP endpoint checks' },
  { value: 'synthetic_api', description: 'Multi-step API workflow checks' },
  { value: 'synthetic_browser', description: 'Browser workflow checks' },
  { value: 'redis', description: 'Redis connect + PING checks' },
  { value: 'postgres', description: 'PostgreSQL connect + query checks' },
  { value: 'mysql', description: 'MySQL connect + query checks' },
  { value: 'mongodb', description: 'MongoDB connect + ping checks' },
  { value: 'rabbitmq', description: 'AMQP connect + auth checks' },
  { value: 'websocket', description: 'WebSocket connect + message checks' },
  { value: 'prometheus', description: 'PromQL query + numeric threshold checks' },
];

// Step indicator component
function StepIndicator({ currentStep }: { currentStep: WizardStep }) {
  const steps: { key: WizardStep; label: string }[] = [
    { key: 'upload', label: 'Upload' },
    { key: 'mapping', label: 'Mapping' },
    { key: 'review', label: 'Review' },
    { key: 'results', label: 'Results' },
  ];

  const getStepIndex = (step: WizardStep) => {
    if (step === 'importing') return 2;
    return steps.findIndex(s => s.key === step);
  };

  const currentIndex = getStepIndex(currentStep);

  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-2 sm:gap-x-0">
      {steps.map((step, index) => (
        <div key={step.key} className="flex items-center">
          <div className={`flex h-7 w-7 items-center justify-center rounded-full text-xs font-semibold tabular-nums transition-colors ${
            index < currentIndex
              ? 'bg-emerald-500/[0.14] text-emerald-300'
              : index === currentIndex
              ? 'bg-cyan-500 text-onaccent'
              : 'bg-well-hover text-slate-500'
          }`}>
            {index < currentIndex ? (
              <Check className="h-3.5 w-3.5" strokeWidth={2.25} />
            ) : (
              index + 1
            )}
          </div>
          <span className={`ml-2 text-ui font-medium ${
            index <= currentIndex ? 'text-white' : 'text-slate-500'
          }`}>
            {step.label}
          </span>
          {index < steps.length - 1 && (
            <div className="mx-3 hidden h-px w-12 bg-edge-strong sm:block" />
          )}
        </div>
      ))}
    </div>
  );
}

// File upload component with drag & drop
function FileUpload({ onFileSelect, loading }: { onFileSelect: (file: File) => void; loading: boolean }) {
  const [dragActive, setDragActive] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);

  const handleDrag = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    if (e.type === 'dragenter' || e.type === 'dragover') {
      setDragActive(true);
    } else if (e.type === 'dragleave') {
      setDragActive(false);
    }
  }, []);

  const handleDrop = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    setDragActive(false);
    
    if (e.dataTransfer.files && e.dataTransfer.files[0]) {
      onFileSelect(e.dataTransfer.files[0]);
    }
  }, [onFileSelect]);

  const handleChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    if (e.target.files && e.target.files[0]) {
      onFileSelect(e.target.files[0]);
    }
  };

  return (
    <div
      onDragEnter={handleDrag}
      onDragLeave={handleDrag}
      onDragOver={handleDrag}
      onDrop={handleDrop}
      onClick={() => inputRef.current?.click()}
      className={`relative flex h-64 w-full cursor-pointer flex-col items-center justify-center rounded-2xl border border-dashed transition-colors ${
        dragActive
          ? 'border-cyan-500 bg-cyan-500/[0.08]'
          : 'border-[var(--control-edge-strong)] bg-well hover:bg-well-hover'
      } ${loading ? 'pointer-events-none opacity-50' : ''}`}
    >
      <input
        ref={inputRef}
        type="file"
        accept=".json,.yaml,.yml,.csv"
        onChange={handleChange}
        className="hidden"
      />
      
      {loading ? (
        <div className="flex flex-col items-center">
          <Loader2 className="h-8 w-8 animate-spin text-cyan-500" strokeWidth={1.75} />
          <p className="mt-4 text-sm text-slate-400">Parsing file...</p>
        </div>
      ) : (
        <>
          <div className="mb-4 flex h-11 w-11 items-center justify-center rounded-full bg-well-hover">
            <UploadCloud className="h-5 w-5 text-slate-400" strokeWidth={1.75} />
          </div>
          <p className="mb-1 text-sm font-medium text-white">
            Drop your file here or click to browse
          </p>
          <p className="text-xs text-slate-500">
            Supports JSON, YAML, and CSV formats (max 10MB)
          </p>
        </>
      )}
    </div>
  );
}

// Field mapping row component
function MappingRow({
  sourceField,
  targetField,
  onTargetChange,
  availableTargets,
  sampleValue,
}: {
  sourceField: string;
  targetField: string;
  onTargetChange: (target: string) => void;
  availableTargets: { key: string; label: string; required: boolean }[];
  sampleValue?: unknown;
}) {
  return (
    <div className="flex flex-col gap-2 py-3 sm:flex-row sm:items-center sm:gap-4">
      <div className="flex-1 min-w-0">
        <p className="text-sm font-medium text-white truncate">{sourceField}</p>
        {sampleValue !== undefined && sampleValue !== null && sampleValue !== '' && (
          <p className="text-xs text-slate-500 truncate mt-0.5">
            Sample: {String(sampleValue).substring(0, 50)}{String(sampleValue).length > 50 ? '...' : ''}
          </p>
        )}
      </div>
      <ArrowRight className="hidden h-4 w-4 flex-shrink-0 text-slate-500 sm:block" strokeWidth={1.75} />
      <div className="w-full flex-shrink-0 sm:w-48">
        <select
          value={targetField}
          onChange={(e) => onTargetChange(e.target.value)}
          className="input"
        >
          <option value="">-- Skip --</option>
          {availableTargets.map((target) => (
            <option key={target.key} value={target.key}>
              {target.label}{target.required ? ' *' : ''}
            </option>
          ))}
        </select>
      </div>
    </div>
  );
}

// Preview table for review step
function PreviewTable({ rows, mapping, typeMapping }: { rows: ImportRow[]; mapping: FieldMapping; typeMapping: Record<string, string> }) {
  const getFieldValue = (row: ImportRow, mappingKey: keyof FieldMapping) => {
    const fieldName = mapping[mappingKey];
    if (!fieldName) return '-';
    const value = row.fields[fieldName];
    if (value === undefined || value === null || value === '') return '-';
    return String(value);
  };

  // Pre-translated sources (portable, Uptime Kuma) carry the target inside the
  // nested config rather than as a mapped top-level field, so read through to
  // it — otherwise the whole column is dashes for exactly the imports where
  // reviewing the target matters most.
  const getConfigValue = (row: ImportRow, key: string) => {
    const configField = mapping.config;
    if (!configField) return undefined;
    const config = row.fields[configField];
    if (!config || typeof config !== 'object' || Array.isArray(config)) return undefined;
    const value = (config as Record<string, unknown>)[key];
    if (value === undefined || value === null || value === '') return undefined;
    return String(value);
  };

  const resolve = (row: ImportRow, mappingKey: keyof FieldMapping, configKey: string) => {
    const mapped = getFieldValue(row, mappingKey);
    if (mapped !== '-') return mapped;
    return getConfigValue(row, configKey) ?? '-';
  };

  const getTarget = (row: ImportRow, type: string) => {
    switch (type) {
      case 'ping':
      case 'dns':
        return resolve(row, 'host', 'host');
      case 'grpc':
      case 'tcp': {
        const host = resolve(row, 'host', 'host');
        if (host === '-') return '-';
        const port = resolve(row, 'port', 'port');
        return port === '-' ? host : `${host}:${port}`;
      }
      case 'group': {
        const members = mapping.group_members ? row.fields[mapping.group_members] : undefined;
        if (Array.isArray(members)) return `${members.length} member${members.length === 1 ? '' : 's'}`;
        return '-';
      }
      case 'postgres':
      case 'mysql':
      case 'mongodb':
      case 'redis':
      case 'rabbitmq': {
        const host = getConfigValue(row, 'host');
        if (!host) return getConfigValue(row, 'connection_string') ?? '-';
        const port = getConfigValue(row, 'port');
        return port ? `${host}:${port}` : host;
      }
      case 'push':
        return getConfigValue(row, 'expected_interval_seconds')
          ? `every ${getConfigValue(row, 'expected_interval_seconds')}s`
          : '-';
      default:
        return resolve(row, 'url', 'url');
    }
  };

  const getRawType = (row: ImportRow) => {
    const typeField = mapping.type;
    if (typeField) {
      const type = String(row.fields[typeField] || '').toLowerCase().trim();
      if (type) return type;
    }
    return '';
  };

  const getMonitorType = (row: ImportRow) => {
    const rawType = getRawType(row);
    
    // Apply type mapping
    if (rawType && typeMapping[rawType]) {
      return typeMapping[rawType].toLowerCase();
    }
    
    if (rawType) return rawType;
    
    // Infer type
    if (mapping.host && row.fields[mapping.host]) {
      if (
        (mapping.service && row.fields[mapping.service]) ||
        (mapping.use_tls && row.fields[mapping.use_tls] !== undefined) ||
        (mapping.port && row.fields[mapping.port] !== undefined)
      ) {
        return 'grpc';
      }
      return 'ping';
    }
    if (mapping.group_members && row.fields[mapping.group_members]) return 'group';
    return 'http';
  };

  const isSupported = (row: ImportRow) => {
    const type = getMonitorType(row);
    return ['', ...SUPPORTED_TYPES.map((supportedType) => supportedType.value)].includes(type);
  };

  return (
    <div className="glass-well overflow-hidden">
      <div className="overflow-x-auto">
        <table className="data-table">
          <thead>
            <tr>
              <th>#</th>
              <th>Name</th>
              <th>Type</th>
              <th>Target</th>
              <th>Status</th>
              <th>Notes</th>
            </tr>
          </thead>
          <tbody>
            {rows.slice(0, 50).map((row) => {
              const type = getMonitorType(row);
              const supported = isSupported(row);
              const target = getTarget(row, type);
              return (
                <tr key={row.index} className={`${supported ? '' : 'opacity-50'}`}>
                  <td className="text-sm tabular-nums text-slate-400">{row.index + 1}</td>
                  <td className="max-w-[200px] truncate text-sm font-medium text-white">
                    {getFieldValue(row, 'name') || `Monitor ${row.index + 1}`}
                  </td>
                  <td><TypeBadge type={type || 'http'} /></td>
                  <td className="max-w-[200px] truncate font-mono text-xs text-slate-400" title={target === '-' ? undefined : target}>
                    {target === '-' ? <span className="text-slate-500">—</span> : target}
                  </td>
                  <td>
                    {supported ? (
                      <span className="inline-flex items-center gap-1 text-xs text-emerald-300">
                        <Check className="h-3.5 w-3.5 text-emerald-400" strokeWidth={2} />
                        Ready
                      </span>
                    ) : (
                      <span className="inline-flex items-center gap-1 text-xs text-amber-300">
                        <AlertTriangle className="h-3.5 w-3.5 text-amber-400" strokeWidth={1.75} />
                        Skip
                      </span>
                    )}
                  </td>
                  <td className="align-top">
                    {row.warnings && row.warnings.length > 0 ? (
                      <details className="group/notes max-w-[380px]">
                        <summary className="cursor-pointer list-none text-xs text-amber-300 hover:text-amber-200">
                          {row.warnings.length} note{row.warnings.length === 1 ? '' : 's'}
                        </summary>
                        <ul className="mt-2 space-y-1 text-xs text-slate-400">
                          {row.warnings.map((warning, i) => (
                            <li key={i}>{warning}</li>
                          ))}
                        </ul>
                      </details>
                    ) : (
                      <span className="text-xs text-slate-500">—</span>
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      {rows.length > 50 && (
        <div className="border-t border-divider px-4 py-3 text-center text-xs text-slate-500">
          Showing 50 of {rows.length} rows
        </div>
      )}
    </div>
  );
}

// Records a format adapter refused to translate. Shown separately from the
// preview rows so "34 of my 41 monitors" has a visible answer.
function SkippedTable({ rows }: { rows: ImportSkippedRow[] }) {
  return (
    <div className="glass-well overflow-hidden">
      <div className="flex items-start gap-3 px-4 pb-2 pt-4">
        <AlertTriangle className="mt-0.5 h-4 w-4 flex-shrink-0 text-amber-400" strokeWidth={1.75} />
        <div>
          <h4 className="text-sm font-medium text-white">
            Not imported ({rows.length})
          </h4>
          <p className="mt-0.5 text-ui text-slate-400">
            These monitors have no faithful equivalent and were left out rather than approximated.
          </p>
        </div>
      </div>
      <div className="overflow-x-auto">
        <table className="data-table">
          <thead>
            <tr>
              <th>Name</th>
              <th>Source type</th>
              <th>Reason</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((row, i) => (
              <tr key={`${row.name}-${i}`}>
                <td className="max-w-[200px] truncate text-sm text-white">{row.name}</td>
                <td className="font-mono text-xs text-slate-400">{row.source_type || 'unknown'}</td>
                <td className="text-sm text-slate-400">{row.reason}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

// Results table
function ResultsTable({ results }: { results: ImportRowResult[] }) {
  return (
    <div className="glass-well overflow-hidden">
      <div className="max-h-96 overflow-x-auto">
        <table className="data-table">
          <thead className="sticky top-0 z-10 [background:linear-gradient(var(--well),var(--well)),var(--glass-fill-strong)]">
            <tr>
              <th>#</th>
              <th>Name</th>
              <th>Type</th>
              <th>Status</th>
              <th>Details</th>
            </tr>
          </thead>
          <tbody>
            {results.map((result) => (
              <tr key={result.index}>
                <td className="text-sm tabular-nums text-slate-400">{result.index + 1}</td>
                <td className="max-w-[200px] truncate text-sm font-medium text-white">
                  {result.name}
                </td>
                <td><TypeBadge type={result.type} /></td>
                <td>
                  <span className={`inline-flex items-center gap-1 text-xs ${
                    result.status === 'success' ? 'text-emerald-300' :
                    result.status === 'skipped' ? 'text-amber-300' :
                    'text-rose-300'
                  }`}>
                    {result.status === 'success' && (
                      <Check className="h-3.5 w-3.5 text-emerald-400" strokeWidth={2} />
                    )}
                    {result.status === 'skipped' && (
                      <AlertTriangle className="h-3.5 w-3.5 text-amber-400" strokeWidth={1.75} />
                    )}
                    {result.status === 'failed' && (
                      <X className="h-3.5 w-3.5 text-rose-400" strokeWidth={2} />
                    )}
                    {result.status.charAt(0).toUpperCase() + result.status.slice(1)}
                  </span>
                </td>
                <td className="max-w-[300px] truncate text-xs text-slate-400">
                  {result.error || result.skip_reason || (result.monitor_id ? `ID: ${result.monitor_id}` : '-')}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

export default function ImportPage() {
  const router = useRouter();
  const [step, setStep] = useState<WizardStep>('upload');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string>('');
  
  // Data from preview
  const [previewData, setPreviewData] = useState<ImportPreviewResponse | null>(null);
  const [mapping, setMapping] = useState<FieldMapping>({});
  const [typeMapping, setTypeMapping] = useState<Record<string, string>>({});
  
  // Import results
  const [importResults, setImportResults] = useState<ImportExecuteResponse | null>(null);

  // Handle file upload
  const handleFileSelect = async (file: File) => {
    setLoading(true);
    setError('');

    try {
      const result = await previewImport(file);
      setPreviewData(result);
      setMapping(result.suggested_mapping);
      setTypeMapping(result.suggested_type_mapping || {});
      setStep('mapping');
    } catch (err: unknown) {
      const errorMessage = err instanceof Error ? err.message : 
        (typeof err === 'object' && err !== null && 'message' in err) ? String((err as { message: unknown }).message) : 
        'Failed to parse file';
      setError(errorMessage);
    } finally {
      setLoading(false);
    }
  };

  // Update mapping
  const updateMapping = (sourceField: string, targetKey: string) => {
    // Find which target field currently has this source field and clear it
    const newMapping = { ...mapping };
    for (const key of Object.keys(newMapping) as (keyof FieldMapping)[]) {
      if (newMapping[key] === sourceField) {
        newMapping[key] = undefined;
      }
    }
    
    // Set the new mapping
    if (targetKey) {
      newMapping[targetKey as keyof FieldMapping] = sourceField;
    }
    
    setMapping(newMapping);
  };

  // Get current target for a source field
  const getTargetForSource = (sourceField: string): string => {
    for (const [key, value] of Object.entries(mapping)) {
      if (value === sourceField) {
        return key;
      }
    }
    return '';
  };

  // Execute import
  const handleImport = async () => {
    if (!previewData) return;

    setStep('importing');
    setError('');

    try {
      const result = await executeImport({
        rows: previewData.rows,
        mapping: mapping,
        type_mapping: typeMapping,
      });
      setImportResults(result);
      setStep('results');
    } catch (err: unknown) {
      const errorMessage = err instanceof Error ? err.message : 
        (typeof err === 'object' && err !== null && 'message' in err) ? String((err as { message: unknown }).message) : 
        'Import failed';
      setError(errorMessage);
      setStep('review');
    }
  };

  // Get types that need mapping (not supported types)
  const getUnmappedTypes = () => {
    if (!previewData?.detected_types) return [];
    const supportedTypes = SUPPORTED_TYPES.map((type) => type.value);
    return previewData.detected_types.filter(t => !supportedTypes.includes(t.toLowerCase()));
  };

  // Update type mapping
  const updateTypeMapping = (sourceType: string, targetType: string) => {
    setTypeMapping(prev => ({
      ...prev,
      [sourceType]: targetType,
    }));
  };

  // Get sample value for a field
  const getSampleValue = (fieldName: string) => {
    if (!previewData || previewData.rows.length === 0) return undefined;
    // Find first non-empty value
    for (const row of previewData.rows.slice(0, 5)) {
      const value = row.fields[fieldName];
      if (value !== undefined && value !== null && value !== '') {
        return value;
      }
    }
    return undefined;
  };

  return (
    <div className="max-w-4xl space-y-6">
      <PageHeader
        breadcrumb={[{ label: 'Monitors', href: '/monitors' }, { label: 'Import' }]}
        title="Import monitors"
        subtitle="Batch import monitors from JSON, YAML, or CSV files."
      />

      <StepIndicator currentStep={step} />

      {error && <Callout tone="danger">{error}</Callout>}

      <div className="glass p-6">
        {step === 'upload' && (
          <div>
            <h2 className="text-title font-semibold text-white">Upload file</h2>
            <p className="mb-5 mt-1 text-sm text-slate-400">
              Upload a file containing your monitor definitions. We&apos;ll automatically detect the format.
            </p>
            <FileUpload onFileSelect={handleFileSelect} loading={loading} />

            <div className="glass-well mt-5 p-4">
              <h3 className="mb-2 text-ui font-medium text-white">Supported formats</h3>
              <ul className="space-y-1 text-xs text-slate-400">
                <li className="flex items-start gap-2">
                  <span className="w-12 flex-shrink-0 font-medium text-slate-200">JSON</span>
                  <span>Array of objects or object with &quot;items&quot;/&quot;monitors&quot; array</span>
                </li>
                <li className="flex items-start gap-2">
                  <span className="w-12 flex-shrink-0 font-medium text-slate-200">YAML</span>
                  <span>List of monitors or document with monitors list</span>
                </li>
                <li className="flex items-start gap-2">
                  <span className="w-12 flex-shrink-0 font-medium text-slate-200">CSV</span>
                  <span>Header row followed by data rows</span>
                </li>
              </ul>
            </div>
          </div>
        )}

        {step === 'mapping' && previewData && (
          <div className="space-y-6">
            <div className="flex items-center justify-between gap-4">
              <div>
                <h2 className="text-title font-semibold text-white">Map fields</h2>
                <p className="mt-1 text-sm text-slate-400">
                  Detected {describeSource(previewData)}
                </p>
              </div>
              <Pill tone="neutral">
                {previewData.detected_fields.length} fields detected
              </Pill>
            </div>

            {previewData.warnings && previewData.warnings.length > 0 && (
              <Callout tone="warning" title="Warnings">
                <ul className="space-y-1 text-ui">
                  {previewData.warnings.map((warning, i) => (
                    <li key={i}>{warning}</li>
                  ))}
                </ul>
              </Callout>
            )}

            {previewData.schema === IMPORT_SCHEMA_PORTABLE && (
              <Callout tone="info">
                Portable export detected. The suggested mappings preserve raw monitor config, alert policy names,
                and group membership by monitor name so this file can be reimported into another instance.
              </Callout>
            )}

            {previewData.schema === IMPORT_SCHEMA_UPTIME_KUMA && (
              <Callout tone="info">
                <p>
                  Uptime Kuma export detected. Monitor types, assertions and group nesting were translated
                  automatically, so the mappings below are already complete.
                </p>
                <p className="mt-2 text-ui text-slate-400">
                  Uptime Kuma notifications are never migrated and no credential is imported. Check the per-monitor
                  notes on the review step, then attach Probara alert channels once the monitors exist.
                </p>
              </Callout>
            )}

            {previewData.skipped_rows && previewData.skipped_rows.length > 0 && (
              <SkippedTable rows={previewData.skipped_rows} />
            )}

            {getUnmappedTypes().length > 0 && (
              <div className="glass-well p-4">
                <div className="flex items-center gap-2">
                  <AlertTriangle className="h-4 w-4 text-amber-400" strokeWidth={1.75} />
                  <h4 className="text-sm font-medium text-white">Map unknown types</h4>
                </div>
                <p className="mb-4 mt-1 text-ui text-slate-400">
                  The following monitor types are not directly supported. Map them to a supported type:
                </p>
                <div className="space-y-3">
                  {getUnmappedTypes().map((sourceType) => (
                    <div key={sourceType} className="flex flex-col gap-2 sm:flex-row sm:items-center sm:gap-4">
                      <div className="min-w-0 flex-1">
                        <span className="inline-flex items-center rounded-md bg-well-hover px-2 py-0.5 font-mono text-ui text-slate-200">
                          {sourceType}
                        </span>
                      </div>
                      <ArrowRight className="hidden h-4 w-4 flex-shrink-0 text-slate-500 sm:block" strokeWidth={1.75} />
                      <div className="w-full flex-shrink-0 sm:w-80">
                        <select
                          value={typeMapping[sourceType] || ''}
                          onChange={(e) => updateTypeMapping(sourceType, e.target.value)}
                          className="input"
                        >
                          <option value="">-- Skip these monitors --</option>
                          {SUPPORTED_TYPES.map((type) => (
                            <option key={type.value} value={type.value}>
                              {monitorTypeLabel(type.value)} - {type.description}
                            </option>
                          ))}
                        </select>
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            )}

            <div>
              <h4 className="text-sm font-medium text-white">Field mapping</h4>
              <div className="mt-3 hidden items-center justify-between border-b border-divider px-1 pb-2 text-xs text-slate-500 sm:flex">
                <span>Source field</span>
                <span className="w-48">Target field</span>
              </div>
              <div className="divide-y divide-divider">
                {previewData.detected_fields.map((field) => (
                  <MappingRow
                    key={field}
                    sourceField={field}
                    targetField={getTargetForSource(field)}
                    onTargetChange={(target) => updateMapping(field, target)}
                    availableTargets={TARGET_FIELDS}
                    sampleValue={getSampleValue(field)}
                  />
                ))}
              </div>
            </div>

            <div className="flex items-center justify-between">
              <Button
                variant="ghost"
                size="sm"
                onClick={() => {
                  setStep('upload');
                  setPreviewData(null);
                  setMapping({});
                  setTypeMapping({});
                }}
              >
                Back
              </Button>
              <Button variant="accent" size="sm" onClick={() => setStep('review')}>
                Continue to review
              </Button>
            </div>
          </div>
        )}

        {step === 'review' && previewData && (
          <div className="space-y-6">
            <div>
              <h2 className="text-title font-semibold text-white">Review import</h2>
              <p className="mt-1 text-sm text-slate-400">
                {isPreMapped(previewData.schema)
                  ? `${previewData.total_rows} monitors will be recreated`
                  : `${previewData.total_rows} monitors will be processed`}
              </p>
            </div>

            <PreviewTable rows={previewData.rows} mapping={mapping} typeMapping={typeMapping} />

            {previewData.skipped_rows && previewData.skipped_rows.length > 0 && (
              <SkippedTable rows={previewData.skipped_rows} />
            )}

            <div className="flex items-center justify-between">
              <Button variant="ghost" size="sm" onClick={() => setStep('mapping')}>
                Back to mapping
              </Button>
              <Button variant="accent" size="sm" onClick={handleImport}>
                Start import
              </Button>
            </div>
          </div>
        )}

        {step === 'importing' && (
          <div className="flex flex-col items-center justify-center py-12">
            <Loader2 className="h-10 w-10 animate-spin text-cyan-500" strokeWidth={1.75} />
            <p className="mt-5 text-title font-semibold text-white">Importing monitors...</p>
            <p className="mt-1 text-sm text-slate-400">This may take a moment</p>
          </div>
        )}

        {step === 'results' && importResults && (
          <div className="space-y-6">
            <h2 className="text-title font-semibold text-white">Import complete</h2>

            <div className="grid grid-cols-3 gap-4">
              {[
                { label: 'Imported', value: importResults.success_count, dot: 'bg-emerald-500' },
                { label: 'Skipped', value: importResults.skipped_count, dot: 'bg-amber-500' },
                { label: 'Failed', value: importResults.failed_count, dot: 'bg-rose-500' },
              ].map((stat) => (
                <div key={stat.label} className="glass-well p-4">
                  <p className="flex items-center gap-2 text-xs text-slate-400">
                    <span className={`h-2 w-2 rounded-full ${stat.dot}`} aria-hidden="true" />
                    {stat.label}
                  </p>
                  <p className="mt-1 text-2xl font-semibold tabular-nums text-white">{stat.value}</p>
                </div>
              ))}
            </div>

            <ResultsTable results={importResults.results} />

            <div className="flex items-center justify-between">
              <Button
                variant="ghost"
                size="sm"
                onClick={() => {
                  setStep('upload');
                  setPreviewData(null);
                  setMapping({});
                  setTypeMapping({});
                  setImportResults(null);
                }}
              >
                Import more
              </Button>
              <Button variant="accent" size="sm" onClick={() => router.push('/monitors')}>
                Go to monitors
              </Button>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
