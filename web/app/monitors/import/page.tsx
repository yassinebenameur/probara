'use client';

import { useState, useCallback, useRef } from 'react';
import { useRouter } from 'next/navigation';
import PageHeader from '@/components/ui/PageHeader';
import Button from '@/components/ui/Button';
import { previewImport, executeImport } from '@/lib/api';
import type { 
  ImportPreviewResponse, 
  ImportRow, 
  FieldMapping, 
  ImportExecuteResponse,
  ImportRowResult 
} from '@/lib/types';

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
];

type WizardStep = 'upload' | 'mapping' | 'review' | 'importing' | 'results';

// Supported monitor types for mapping
const SUPPORTED_TYPES = [
  { value: 'http', label: 'HTTP', description: 'HTTP/HTTPS endpoint monitoring' },
  { value: 'ping', label: 'Ping', description: 'ICMP ping checks' },
  { value: 'dns', label: 'DNS', description: 'DNS record checks' },
  { value: 'grpc', label: 'gRPC', description: 'gRPC health checks' },
  { value: 'group', label: 'Group', description: 'Group of monitors' },
  { value: 'agent', label: 'Agent', description: 'Heartbeat checks from installed agents' },
  { value: 'push', label: 'Push', description: 'Token-based push heartbeat checks' },
  { value: 'sip', label: 'SIP', description: 'SIP endpoint checks' },
  { value: 'synthetic_api', label: 'Synthetic API', description: 'Multi-step API workflow checks' },
  { value: 'synthetic_browser', label: 'Synthetic Browser', description: 'Browser workflow checks' },
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
    <div className="flex items-center justify-center gap-2 mb-8">
      {steps.map((step, index) => (
        <div key={step.key} className="flex items-center">
          <div className={`flex items-center justify-center w-8 h-8 rounded-full text-xs font-medium transition-all ${
            index < currentIndex
              ? 'bg-emerald-500 text-white'
              : index === currentIndex
              ? 'bg-cyan-500 text-white'
              : 'bg-slate-700 text-slate-400'
          }`}>
            {index < currentIndex ? (
              <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 13l4 4L19 7" />
              </svg>
            ) : (
              index + 1
            )}
          </div>
          <span className={`ml-2 text-xs font-medium ${
            index <= currentIndex ? 'text-white' : 'text-slate-500'
          }`}>
            {step.label}
          </span>
          {index < steps.length - 1 && (
            <div className={`w-12 h-0.5 mx-3 ${
              index < currentIndex ? 'bg-emerald-500' : 'bg-slate-700'
            }`} />
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
      className={`relative flex flex-col items-center justify-center w-full h-64 rounded-xl border-2 border-dashed transition-all cursor-pointer ${
        dragActive
          ? 'border-cyan-500 bg-cyan-500/10'
          : 'border-white/[0.1] bg-slate-800/30 hover:border-white/[0.2] hover:bg-slate-800/50'
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
          <div className="w-12 h-12 border-4 border-cyan-500/30 border-t-cyan-500 rounded-full animate-spin" />
          <p className="mt-4 text-sm text-slate-400">Parsing file...</p>
        </div>
      ) : (
        <>
          <div className="flex items-center justify-center w-16 h-16 rounded-xl bg-slate-700/50 mb-4">
            <svg className="w-8 h-8 text-slate-400" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M7 16a4 4 0 01-.88-7.903A5 5 0 1115.9 6L16 6a5 5 0 011 9.9M15 13l-3-3m0 0l-3 3m3-3v12" />
            </svg>
          </div>
          <p className="text-sm font-medium text-white mb-1">
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
    <div className="flex items-center gap-4 py-3 border-b border-white/[0.05] last:border-0">
      <div className="flex-1 min-w-0">
        <p className="text-sm font-medium text-white truncate">{sourceField}</p>
        {sampleValue !== undefined && sampleValue !== null && sampleValue !== '' && (
          <p className="text-xs text-slate-500 truncate mt-0.5">
            Sample: {String(sampleValue).substring(0, 50)}{String(sampleValue).length > 50 ? '...' : ''}
          </p>
        )}
      </div>
      <svg className="w-4 h-4 text-slate-500 flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor">
        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M17 8l4 4m0 0l-4 4m4-4H3" />
      </svg>
      <div className="w-48 flex-shrink-0">
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
    <div className="rounded-xl border border-white/[0.06] overflow-hidden">
      <div className="overflow-x-auto">
        <table className="w-full">
          <thead>
            <tr className="bg-slate-800/50">
              <th className="px-4 py-3 text-left text-xs font-medium text-slate-400 uppercase tracking-wider">#</th>
              <th className="px-4 py-3 text-left text-xs font-medium text-slate-400 uppercase tracking-wider">Name</th>
              <th className="px-4 py-3 text-left text-xs font-medium text-slate-400 uppercase tracking-wider">Type</th>
              <th className="px-4 py-3 text-left text-xs font-medium text-slate-400 uppercase tracking-wider">Target</th>
              <th className="px-4 py-3 text-left text-xs font-medium text-slate-400 uppercase tracking-wider">Status</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-white/[0.04]">
            {rows.slice(0, 50).map((row) => {
              const type = getMonitorType(row);
              const supported = isSupported(row);
              return (
                <tr key={row.index} className={`${supported ? '' : 'opacity-50'}`}>
                  <td className="px-4 py-3 text-sm text-slate-400">{row.index + 1}</td>
                  <td className="px-4 py-3 text-sm text-white font-medium truncate max-w-[200px]">
                    {getFieldValue(row, 'name') || `Monitor ${row.index + 1}`}
                  </td>
                  <td className="px-4 py-3">
                    <span className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium ${
                      type === 'http' ? 'bg-cyan-500/20 text-cyan-400' :
                      type === 'ping' ? 'bg-violet-500/20 text-violet-400' :
                      type === 'dns' ? 'bg-sky-500/20 text-sky-400' :
                      type === 'grpc' ? 'bg-teal-500/20 text-teal-400' :
                      type === 'group' ? 'bg-indigo-500/20 text-indigo-400' :
                      type === 'agent' ? 'bg-amber-500/20 text-amber-400' :
                      'bg-slate-500/20 text-slate-400'
                    }`}>
                      {type.toUpperCase() || 'HTTP'}
                    </span>
                  </td>
                  <td className="px-4 py-3 text-sm text-slate-400 truncate max-w-[200px]">
                    {type === 'ping' || type === 'dns'
                      ? getFieldValue(row, 'host')
                      : type === 'grpc'
                        ? `${getFieldValue(row, 'host')}:${getFieldValue(row, 'port') === '-' ? '443' : getFieldValue(row, 'port')}`
                        : getFieldValue(row, 'url')}
                  </td>
                  <td className="px-4 py-3">
                    {supported ? (
                      <span className="inline-flex items-center gap-1 text-xs text-emerald-400">
                        <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 13l4 4L19 7" />
                        </svg>
                        Ready
                      </span>
                    ) : (
                      <span className="inline-flex items-center gap-1 text-xs text-amber-400">
                        <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z" />
                        </svg>
                        Skip
                      </span>
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      {rows.length > 50 && (
        <div className="px-4 py-3 bg-slate-800/30 text-xs text-slate-500 text-center">
          Showing 50 of {rows.length} rows
        </div>
      )}
    </div>
  );
}

// Results table
function ResultsTable({ results }: { results: ImportRowResult[] }) {
  return (
    <div className="rounded-xl border border-white/[0.06] overflow-hidden">
      <div className="overflow-x-auto max-h-96">
        <table className="w-full">
          <thead className="sticky top-0 bg-slate-900">
            <tr className="bg-slate-800/50">
              <th className="px-4 py-3 text-left text-xs font-medium text-slate-400 uppercase tracking-wider">#</th>
              <th className="px-4 py-3 text-left text-xs font-medium text-slate-400 uppercase tracking-wider">Name</th>
              <th className="px-4 py-3 text-left text-xs font-medium text-slate-400 uppercase tracking-wider">Type</th>
              <th className="px-4 py-3 text-left text-xs font-medium text-slate-400 uppercase tracking-wider">Status</th>
              <th className="px-4 py-3 text-left text-xs font-medium text-slate-400 uppercase tracking-wider">Details</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-white/[0.04]">
            {results.map((result) => (
              <tr key={result.index}>
                <td className="px-4 py-3 text-sm text-slate-400">{result.index + 1}</td>
                <td className="px-4 py-3 text-sm text-white font-medium truncate max-w-[200px]">
                  {result.name}
                </td>
                <td className="px-4 py-3">
                  <span className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium ${
                    result.type === 'http' ? 'bg-cyan-500/20 text-cyan-400' :
                    result.type === 'ping' ? 'bg-violet-500/20 text-violet-400' :
                    result.type === 'dns' ? 'bg-sky-500/20 text-sky-400' :
                    result.type === 'grpc' ? 'bg-teal-500/20 text-teal-400' :
                    result.type === 'group' ? 'bg-indigo-500/20 text-indigo-400' :
                    'bg-slate-500/20 text-slate-400'
                  }`}>
                    {result.type.toUpperCase()}
                  </span>
                </td>
                <td className="px-4 py-3">
                  <span className={`inline-flex items-center gap-1 text-xs ${
                    result.status === 'success' ? 'text-emerald-400' :
                    result.status === 'skipped' ? 'text-amber-400' :
                    'text-rose-400'
                  }`}>
                    {result.status === 'success' && (
                      <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 13l4 4L19 7" />
                      </svg>
                    )}
                    {result.status === 'skipped' && (
                      <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 9v2m0 4h.01" />
                      </svg>
                    )}
                    {result.status === 'failed' && (
                      <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
                      </svg>
                    )}
                    {result.status.charAt(0).toUpperCase() + result.status.slice(1)}
                  </span>
                </td>
                <td className="px-4 py-3 text-xs text-slate-400 truncate max-w-[300px]">
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
    <div className="mx-auto max-w-4xl space-y-6">
      <PageHeader
        breadcrumb={[{ label: 'Monitors', href: '/monitors' }, { label: 'Import' }]}
        title="Import monitors"
        subtitle="Batch import monitors from JSON, YAML, or CSV files."
      />

      {/* Step Indicator */}
      <StepIndicator currentStep={step} />

      {/* Error Display */}
      {error && (
        <div className="mb-6 rounded-lg border border-rose-500/20 bg-rose-500/10 px-4 py-3">
          <div className="flex items-center gap-2">
            <svg className="w-4 h-4 text-rose-400 flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>
            <p className="text-sm text-rose-400">{error}</p>
          </div>
        </div>
      )}

      {/* Step Content */}
      <div className="rounded-xl border border-white/[0.06] bg-slate-900/40 p-6">
        {/* Upload Step */}
        {step === 'upload' && (
          <div>
            <h2 className="text-lg font-medium text-white mb-2">Upload File</h2>
            <p className="text-sm text-slate-400 mb-6">
              Upload a file containing your monitor definitions. We&apos;ll automatically detect the format.
            </p>
            <FileUpload onFileSelect={handleFileSelect} loading={loading} />
            
            <div className="mt-6 p-4 rounded-lg bg-slate-800/30 border border-white/[0.04]">
              <h3 className="text-sm font-medium text-white mb-2">Supported Formats</h3>
              <ul className="text-xs text-slate-400 space-y-1">
                <li className="flex items-center gap-2">
                  <span className="w-12 text-cyan-400 font-mono">JSON</span>
                  <span>Array of objects or object with &quot;items&quot;/&quot;monitors&quot; array</span>
                </li>
                <li className="flex items-center gap-2">
                  <span className="w-12 text-violet-400 font-mono">YAML</span>
                  <span>List of monitors or document with monitors list</span>
                </li>
                <li className="flex items-center gap-2">
                  <span className="w-12 text-emerald-400 font-mono">CSV</span>
                  <span>Header row followed by data rows</span>
                </li>
              </ul>
            </div>
          </div>
        )}

        {/* Mapping Step */}
        {step === 'mapping' && previewData && (
          <div>
            <div className="flex items-center justify-between mb-6">
              <div>
                <h2 className="text-lg font-medium text-white">Map Fields</h2>
                <p className="text-sm text-slate-400 mt-0.5">
                  {previewData.schema === 'portable_monitor_export'
                    ? `Detected ${previewData.total_rows} monitors from a portable YAML export`
                    : `Detected ${previewData.total_rows} rows in ${previewData.format.toUpperCase()} format`}
                </p>
              </div>
              <span className="inline-flex items-center px-2 py-1 rounded bg-slate-700/50 text-xs text-slate-300">
                {previewData.detected_fields.length} fields detected
              </span>
            </div>

            {/* Warnings */}
            {previewData.warnings && previewData.warnings.length > 0 && (
              <div className="mb-6 rounded-lg border border-amber-500/20 bg-amber-500/10 px-4 py-3">
                <h4 className="text-xs font-medium text-amber-400 uppercase tracking-wider mb-2">Warnings</h4>
                <ul className="text-xs text-amber-300/80 space-y-1">
                  {previewData.warnings.map((warning, i) => (
                    <li key={i} className="flex items-start gap-2">
                      <svg className="w-3 h-3 mt-0.5 flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 9v2m0 4h.01" />
                      </svg>
                      {warning}
                    </li>
                  ))}
                </ul>
              </div>
            )}

            {previewData.schema === 'portable_monitor_export' && (
              <div className="mb-6 rounded-lg border border-cyan-500/20 bg-cyan-500/10 px-4 py-3">
                <p className="text-sm text-cyan-300">
                  Portable export detected. The suggested mappings preserve raw monitor config, alert policy names,
                  and group membership by monitor name so this file can be reimported into another instance.
                </p>
              </div>
            )}

            {/* Type Mapping (for unknown types) */}
            {getUnmappedTypes().length > 0 && (
              <div className="rounded-lg border border-amber-500/20 bg-amber-500/5 p-4 mb-6">
                <div className="flex items-center gap-2 mb-4">
                  <svg className="w-4 h-4 text-amber-400" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z" />
                  </svg>
                  <h4 className="text-sm font-medium text-amber-400">Map Unknown Types</h4>
                </div>
                <p className="text-xs text-amber-300/70 mb-4">
                  The following monitor types are not directly supported. Map them to a supported type:
                </p>
                <div className="space-y-3">
                  {getUnmappedTypes().map((sourceType) => (
                    <div key={sourceType} className="flex items-center gap-4">
                      <div className="flex-1 min-w-0">
                        <span className="inline-flex items-center px-2.5 py-1 rounded-md bg-slate-700/50 text-sm font-medium text-white">
                          {sourceType}
                        </span>
                      </div>
                      <svg className="w-4 h-4 text-slate-500 flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M17 8l4 4m0 0l-4 4m4-4H3" />
                      </svg>
                      <div className="w-48 flex-shrink-0">
                        <select
                          value={typeMapping[sourceType] || ''}
                          onChange={(e) => updateTypeMapping(sourceType, e.target.value)}
                          className="input border-amber-500/40 focus:border-amber-500/60"
                        >
                          <option value="">-- Skip these monitors --</option>
                          {SUPPORTED_TYPES.map((type) => (
                            <option key={type.value} value={type.value}>
                              {type.label} - {type.description}
                            </option>
                          ))}
                        </select>
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            )}

            {/* Field Mapping Table */}
            <div className="rounded-lg border border-white/[0.06] bg-slate-800/30 p-4 mb-6">
              <h4 className="text-sm font-medium text-white mb-4">Field Mapping</h4>
              <div className="flex items-center justify-between text-xs text-slate-500 uppercase tracking-wider mb-4 px-1">
                <span>Source Field</span>
                <span>Target Field</span>
              </div>
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

            {/* Actions */}
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

        {/* Review Step */}
        {step === 'review' && previewData && (
          <div>
            <div className="flex items-center justify-between mb-6">
              <div>
                <h2 className="text-lg font-medium text-white">Review Import</h2>
                <p className="text-sm text-slate-400 mt-0.5">
                  {previewData.schema === 'portable_monitor_export'
                    ? `${previewData.total_rows} monitors from the portable export will be recreated`
                    : `${previewData.total_rows} monitors will be processed`}
                </p>
              </div>
            </div>

            <PreviewTable rows={previewData.rows} mapping={mapping} typeMapping={typeMapping} />

            {/* Actions */}
            <div className="flex items-center justify-between mt-6">
              <Button variant="ghost" size="sm" onClick={() => setStep('mapping')}>
                Back to mapping
              </Button>
              <Button variant="accent" size="sm" onClick={handleImport}>
                Start import
              </Button>
            </div>
          </div>
        )}

        {/* Importing Step */}
        {step === 'importing' && (
          <div className="flex flex-col items-center justify-center py-12">
            <div className="w-16 h-16 border-4 border-cyan-500/30 border-t-cyan-500 rounded-full animate-spin" />
            <p className="mt-6 text-lg font-medium text-white">Importing monitors...</p>
            <p className="mt-2 text-sm text-slate-400">This may take a moment</p>
          </div>
        )}

        {/* Results Step */}
        {step === 'results' && importResults && (
          <div>
            <h2 className="text-lg font-medium text-white mb-2">Import Complete</h2>
            
            {/* Summary Cards */}
            <div className="grid grid-cols-3 gap-4 mb-6">
              <div className="rounded-lg bg-emerald-500/10 border border-emerald-500/20 p-4 text-center">
                <p className="text-2xl font-bold text-emerald-400">{importResults.success_count}</p>
                <p className="text-xs text-emerald-400/80 mt-1">Imported</p>
              </div>
              <div className="rounded-lg bg-amber-500/10 border border-amber-500/20 p-4 text-center">
                <p className="text-2xl font-bold text-amber-400">{importResults.skipped_count}</p>
                <p className="text-xs text-amber-400/80 mt-1">Skipped</p>
              </div>
              <div className="rounded-lg bg-rose-500/10 border border-rose-500/20 p-4 text-center">
                <p className="text-2xl font-bold text-rose-400">{importResults.failed_count}</p>
                <p className="text-xs text-rose-400/80 mt-1">Failed</p>
              </div>
            </div>

            <ResultsTable results={importResults.results} />

            {/* Actions */}
            <div className="flex items-center justify-between mt-6">
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
