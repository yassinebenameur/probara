'use client';

import { useState, useEffect } from 'react';
import { Download } from 'lucide-react';
import { Monitor, CreateMonitorRequest, UpdateMonitorRequest, AlertPolicy, AgentMonitorConfig, AgentInstallCommand, ApiKey } from '@/lib/types';
import { getAlertPolicies, getAgentInstallCommand, getApiKeys } from '@/lib/api';
import { getApiKey } from '@/lib/auth';
import { loadStoredApiKeys } from '@/lib/api-keys';
import FormField from '@/components/ui/FormField';
import FormSection from '@/components/ui/FormSection';
import FormActions from '@/components/ui/FormActions';
import Button from '@/components/ui/Button';

type ServerType =
  | 'linux-amd64'
  | 'linux-arm64'
  | 'macos-amd64'
  | 'macos-arm64'
  | 'windows-amd64';

type ServerTypeOption = {
  value: ServerType;
  label: string;
  os: 'linux' | 'darwin' | 'windows';
  arch: 'amd64' | 'arm64';
  binaryExt: string;
  family: 'unix' | 'windows';
};

const SERVER_TYPE_OPTIONS: ServerTypeOption[] = [
  { value: 'linux-amd64', label: 'Linux (x86_64)', os: 'linux', arch: 'amd64', binaryExt: '', family: 'unix' },
  { value: 'linux-arm64', label: 'Linux (ARM64)', os: 'linux', arch: 'arm64', binaryExt: '', family: 'unix' },
  { value: 'macos-amd64', label: 'macOS (Intel)', os: 'darwin', arch: 'amd64', binaryExt: '', family: 'unix' },
  { value: 'macos-arm64', label: 'macOS (Apple Silicon)', os: 'darwin', arch: 'arm64', binaryExt: '', family: 'unix' },
  { value: 'windows-amd64', label: 'Windows (x86_64)', os: 'windows', arch: 'amd64', binaryExt: '.exe', family: 'windows' },
];

const DEFAULT_SERVER_TYPE: ServerType = 'linux-amd64';
const UNIX_INSTALL_PATH = '/usr/local/bin/probara-agent';
const WINDOWS_INSTALL_DIR = 'C:\\\\Program Files\\\\ProbaraAgent';
const WINDOWS_INSTALL_PATH = `${WINDOWS_INSTALL_DIR}\\\\probara-agent.exe`;

type ApiKeyOption = {
  id: string;
  label: string;
  key: string;
};

interface AgentFormProps {
  monitor?: Monitor;
  initialData?: CreateMonitorRequest;
  onSubmit: (data: CreateMonitorRequest | UpdateMonitorRequest) => Promise<void>;
  onCancel?: () => void;
  loading?: boolean;
}

export default function AgentForm({
  monitor,
  initialData,
  onSubmit,
  onCancel,
  loading = false,
}: AgentFormProps) {
  const [alertPolicies, setAlertPolicies] = useState<AlertPolicy[]>([]);
  const [showInstallInstructions, setShowInstallInstructions] = useState(false);
  const [installCommand, setInstallCommand] = useState<AgentInstallCommand | null>(null);
  const [loadingInstallCmd, setLoadingInstallCmd] = useState(false);
  const [apiKey, setApiKeyState] = useState<string>('');
  const [copiedField, setCopiedField] = useState<string | null>(null);
  const [serverType, setServerType] = useState<ServerType>(DEFAULT_SERVER_TYPE);
  const [apiKeyOptions, setApiKeyOptions] = useState<ApiKeyOption[]>([]);
  const [selectedApiKeyId, setSelectedApiKeyId] = useState<string>('');
  const [loadingApiKeys, setLoadingApiKeys] = useState(false);
  const [apiKeyError, setApiKeyError] = useState<string>('');
  const [apiKeysLoaded, setApiKeysLoaded] = useState(false);
  const isEditMode = Boolean(monitor);
  const initialAgentConfig =
    !isEditMode && initialData?.type === 'agent' ? (initialData.config as AgentMonitorConfig) : undefined;

  const [formData, setFormData] = useState({
    name: monitor?.name || initialData?.name || '',
    expected_interval_seconds: monitor && monitor.type === 'agent'
      ? (monitor.config as AgentMonitorConfig).expected_interval_seconds
      : initialAgentConfig?.expected_interval_seconds || 60,
    alert_policy_ids:
      monitor?.alert_policy_ids ||
      (monitor?.alert_policy_id ? [monitor.alert_policy_id] : initialData?.alert_policy_ids || []),
    enabled: monitor?.enabled ?? initialData?.enabled ?? true,
    tags: monitor?.tags?.join(', ') || (initialData?.tags || []).join(', '),
  });

  const [errors, setErrors] = useState<Record<string, string>>({});

  useEffect(() => {
    loadAlertPolicies();
    const key = getApiKey();
    if (key) setApiKeyState(key);
  }, []);

  const loadAlertPolicies = async () => {
    try {
      const response = await getAlertPolicies({ page_size: 100 });
      setAlertPolicies(response?.items || []);
    } catch (error) {
      console.error('Failed to load alert policies:', error);
    }
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setErrors({});

    const newErrors: Record<string, string> = {};
    if (!formData.name.trim()) newErrors.name = 'Name is required';
    if (formData.expected_interval_seconds < 10) newErrors.expected_interval_seconds = 'Minimum 10 seconds';

    if (Object.keys(newErrors).length > 0) {
      setErrors(newErrors);
      return;
    }

    const config: AgentMonitorConfig = {
      agent_id: isEditMode ? monitor?.agent_id || '' : '',
      expected_interval_seconds: formData.expected_interval_seconds,
    };

    const requestData: CreateMonitorRequest | UpdateMonitorRequest = {
      name: formData.name.trim(),
      type: 'agent',
      config,
      interval_seconds: formData.expected_interval_seconds,
      timeout_seconds: formData.expected_interval_seconds,
      enabled: formData.enabled,
    };

    requestData.alert_policy_ids = formData.alert_policy_ids;
    if (formData.tags.trim()) {
      requestData.tags = formData.tags.split(',').map(t => t.trim()).filter(t => t);
    }

    try {
      await onSubmit(requestData);
      if (!isEditMode) setShowInstallInstructions(true);
    } catch (error) {
      console.error('Failed to save monitor:', error);
    }
  };

  const loadInstallCommand = async () => {
    if (!monitor?.id) return;
    setLoadingInstallCmd(true);
    try {
      const cmd = await getAgentInstallCommand(monitor.id);
      setInstallCommand(cmd);
    } catch (error) {
      console.error('Failed to load install command:', error);
    } finally {
      setLoadingInstallCmd(false);
    }
  };

  const copyToClipboard = (text: string, field: string) => {
    if (!text) return;
    navigator.clipboard.writeText(text);
    setCopiedField(field);
    setTimeout(() => setCopiedField(null), 2000);
  };

  const hashApiKey = async (key: string): Promise<string> => {
    const encoder = new TextEncoder();
    const data = encoder.encode(key);
    const digest = await crypto.subtle.digest('SHA-256', data);
    return Array.from(new Uint8Array(digest))
      .map((byte) => byte.toString(16).padStart(2, '0'))
      .join('');
  };

  const loadApiKeyOptions = async () => {
    if (loadingApiKeys) return;
    setLoadingApiKeys(true);
    setApiKeyError('');

    try {
      const storedKeys = loadStoredApiKeys();
      const storedMap = new Map(storedKeys.map((item) => [item.id, item]));
      let apiKeys: ApiKey[] = [];

      try {
        const response = await getApiKeys({ page_size: 100 });
        apiKeys = response.items || [];
      } catch (err) {
        console.warn('Failed to load API keys list:', err);
      }

      const options: ApiKeyOption[] = [];
      const usedIds = new Set<string>();

      apiKeys
        .filter((item) => !item.revoked_at)
        .forEach((item) => {
          const stored = storedMap.get(item.id);
          if (stored?.key) {
            options.push({
              id: item.id,
              label: `${item.name} | ${item.key_prefix?.slice(0, 8) || 'key'}`,
              key: stored.key,
            });
            usedIds.add(item.id);
          }
        });

      const currentKey = apiKey || getApiKey() || '';
      if (currentKey) {
        let browserMatched = false;
        try {
          const prefix = await hashApiKey(currentKey);
          const matched = apiKeys.find((item) => item.key_prefix === prefix && !item.revoked_at);
          if (matched && !usedIds.has(matched.id)) {
            options.unshift({
              id: matched.id,
              label: `${matched.name} | current`,
              key: currentKey,
            });
            usedIds.add(matched.id);
            browserMatched = true;
          }
        } catch (err) {
          console.warn('Failed to hash API key:', err);
        }

        if (!browserMatched) {
          options.unshift({
            id: 'current-browser',
            label: 'Current browser key',
            key: currentKey,
          });
        }
      }

      storedKeys.forEach((stored) => {
        if (usedIds.has(stored.id)) return;
        options.push({
          id: stored.id,
          label: `${stored.name || 'Saved key'} | saved`,
          key: stored.key,
        });
      });

      setApiKeyOptions(options);

      setSelectedApiKeyId((prev) => {
        if (prev && options.some((item) => item.id === prev)) {
          return prev;
        }
        return options[0]?.id || '';
      });

      if (options.length === 0 && apiKeys.length > 0) {
        setApiKeyError('No stored API key secrets found. Create a key in Settings to use here.');
      }
    } finally {
      setLoadingApiKeys(false);
      setApiKeysLoaded(true);
    }
  };

  if (isEditMode && monitor && showInstallInstructions) {
    if (!installCommand && !loadingInstallCmd) loadInstallCommand();
    if (!loadingApiKeys && !apiKeysLoaded) loadApiKeyOptions();

    const selectedServer = SERVER_TYPE_OPTIONS.find((option) => option.value === serverType) || SERVER_TYPE_OPTIONS[0];
    const isWindows = selectedServer.family === 'windows';
    const activeApiKey = apiKeyOptions.find((option) => option.id === selectedApiKeyId)?.key || apiKey || '';
    const resolvedApiKey = activeApiKey || 'YOUR_API_KEY';
    const binaryName = `probara-agent-${selectedServer.os}-${selectedServer.arch}${selectedServer.binaryExt}`;
    const downloadUrl = installCommand ? `${installCommand.download_url}${binaryName}` : '';
    const installScriptUrl = installCommand
      ? `${installCommand.backend_url}/api/v1/monitors/${monitor.id}/agent/install/script.sh`
      : '';
    const linuxQuickInstallCommand = installCommand
      ? `curl -sSL -H "Authorization: Bearer ${resolvedApiKey}" \
  ${installScriptUrl} | bash`
      : '';
    const linuxQuickInstallCopy = installCommand
      ? `curl -sSL -H "Authorization: Bearer ${resolvedApiKey}" ${installScriptUrl} | bash`
      : '';
    const windowsQuickInstallCommand = installCommand
      ? `powershell -Command "$p='${WINDOWS_INSTALL_DIR}'; New-Item -ItemType Directory -Force -Path $p | Out-Null; $exe=Join-Path $p 'probara-agent.exe'; Invoke-WebRequest -Uri '${downloadUrl}' -OutFile $exe; Start-Process -FilePath $exe -ArgumentList '-backend-url','${installCommand.backend_url}','-agent-id','${installCommand.agent_id}','-api-key','${resolvedApiKey}','-interval','${installCommand.interval_seconds}'"`
      : '';
    const quickInstallCommand = installCommand
      ? (isWindows ? windowsQuickInstallCommand : linuxQuickInstallCommand)
      : '';
    const quickInstallCopy = installCommand
      ? (isWindows ? windowsQuickInstallCommand : linuxQuickInstallCopy)
      : '';
    const downloadCommand = installCommand
      ? isWindows
        ? `powershell -Command "New-Item -ItemType Directory -Force -Path '${WINDOWS_INSTALL_DIR}' | Out-Null; Invoke-WebRequest -Uri '${downloadUrl}' -OutFile '${WINDOWS_INSTALL_PATH}'"`
        : `curl -sSL -o ${UNIX_INSTALL_PATH} \
  ${downloadUrl}
chmod +x ${UNIX_INSTALL_PATH}`
      : '';
    const runCommand = installCommand
      ? isWindows
        ? `"${WINDOWS_INSTALL_PATH}" -backend-url ${installCommand.backend_url} -agent-id ${installCommand.agent_id} -api-key ${resolvedApiKey} -interval ${installCommand.interval_seconds}`
        : `${UNIX_INSTALL_PATH} -backend-url ${installCommand.backend_url} -agent-id ${installCommand.agent_id} -api-key ${resolvedApiKey} -interval ${installCommand.interval_seconds}`
      : '';

    return (
      <div className="space-y-5">
        <FormSection title="Agent installation">
          {loadingInstallCmd ? (
            <div className="text-sm text-slate-500">Loading installation details…</div>
          ) : installCommand ? (
            <>
              <div className="grid gap-3 sm:grid-cols-2">
                <FormField label="Agent ID">
                  <div className="flex gap-2">
                    <code className="flex-1 overflow-x-auto rounded-lg border border-white/[0.06] bg-slate-900/60 px-3 py-2 font-mono text-xs text-cyan-300">
                      {installCommand.agent_id}
                    </code>
                    <Button
                      variant="ghost"
                      size="xs"
                      type="button"
                      onClick={() => copyToClipboard(installCommand.agent_id, 'agent_id')}
                    >
                      {copiedField === 'agent_id' ? 'Copied' : 'Copy'}
                    </Button>
                  </div>
                </FormField>
                <FormField label="API key">
                  <div className="space-y-2">
                    {loadingApiKeys ? (
                      <p className="text-xs text-slate-500">Loading API keys…</p>
                    ) : apiKeyOptions.length > 1 ? (
                      <select
                        value={selectedApiKeyId}
                        onChange={(e) => setSelectedApiKeyId(e.target.value)}
                        className="input input-xs"
                      >
                        {apiKeyOptions.map((option) => (
                          <option key={option.id} value={option.id}>{option.label}</option>
                        ))}
                      </select>
                    ) : null}
                    <div className="flex gap-2">
                      <code className="flex-1 overflow-x-auto rounded-lg border border-white/[0.06] bg-slate-900/60 px-3 py-2 font-mono text-xs text-cyan-300">
                        {activeApiKey || 'YOUR_API_KEY'}
                      </code>
                      <Button
                        variant="ghost"
                        size="xs"
                        type="button"
                        disabled={!activeApiKey}
                        onClick={() => copyToClipboard(activeApiKey, 'api_key')}
                      >
                        {copiedField === 'api_key' ? 'Copied' : 'Copy'}
                      </Button>
                    </div>
                    {apiKeyError && <p className="text-xs text-amber-300">{apiKeyError}</p>}
                    {!apiKeyError && apiKeyOptions.length === 0 && (
                      <p className="text-xs text-slate-500">No stored API keys. Create one in Settings to auto-fill.</p>
                    )}
                  </div>
                </FormField>
              </div>

              <FormField label="Server type" description="Where the agent will run">
                <select
                  value={serverType}
                  onChange={(e) => setServerType(e.target.value as ServerType)}
                  className="input input-xs"
                >
                  {SERVER_TYPE_OPTIONS.map((option) => (
                    <option key={option.value} value={option.value}>{option.label}</option>
                  ))}
                </select>
              </FormField>

              <FormField label={isWindows ? 'Quick install (Windows)' : 'Quick install (Linux/macOS)'}>
                <div className="flex gap-2">
                  <pre className="flex-1 overflow-x-auto whitespace-pre-wrap rounded-lg border border-white/[0.06] bg-slate-900/60 px-3 py-2 font-mono text-xs text-slate-300">{quickInstallCommand}</pre>
                  <Button
                    variant="ghost"
                    size="xs"
                    type="button"
                    className="self-start"
                    onClick={() => copyToClipboard(quickInstallCopy, 'install')}
                  >
                    {copiedField === 'install' ? 'Copied' : 'Copy'}
                  </Button>
                </div>
              </FormField>

              <FormField label="Download binary">
                <div className="flex gap-2">
                  <pre className="flex-1 overflow-x-auto whitespace-pre-wrap rounded-lg border border-white/[0.06] bg-slate-900/60 px-3 py-2 font-mono text-xs text-slate-300">{downloadCommand}</pre>
                  <Button
                    variant="ghost"
                    size="xs"
                    type="button"
                    className="self-start"
                    onClick={() => copyToClipboard(downloadCommand, 'download')}
                  >
                    {copiedField === 'download' ? 'Copied' : 'Copy'}
                  </Button>
                </div>
              </FormField>

              <FormField label="Run agent">
                <div className="flex gap-2">
                  <code className="flex-1 overflow-x-auto rounded-lg border border-white/[0.06] bg-slate-900/60 px-3 py-2 font-mono text-xs text-slate-300">
                    {runCommand}
                  </code>
                  <Button
                    variant="ghost"
                    size="xs"
                    type="button"
                    className="self-start"
                    onClick={() => copyToClipboard(runCommand, 'manual')}
                  >
                    {copiedField === 'manual' ? 'Copied' : 'Copy'}
                  </Button>
                </div>
              </FormField>

              <div className="rounded-lg border border-emerald-500/20 bg-emerald-500/5 px-4 py-3">
                <p className="text-xs font-medium text-white">Collected metrics</p>
                <p className="mt-0.5 text-xs text-slate-400">
                  CPU, memory, disk, network I/O, system load, process count
                </p>
              </div>
            </>
          ) : (
            <p className="text-sm text-rose-400">Failed to load installation details</p>
          )}
        </FormSection>

        <div className="flex items-center justify-end gap-2">
          <Button variant="ghost" size="sm" type="button" onClick={() => setShowInstallInstructions(false)}>
            Back to settings
          </Button>
          {onCancel && (
            <Button variant="accent" size="sm" type="button" onClick={onCancel}>
              Done
            </Button>
          )}
        </div>
      </div>
    );
  }

  return (
    <form onSubmit={handleSubmit} className="space-y-5">
      <FormSection title="Agent">
        <FormField
          label="Agent name"
          required
          error={errors.name}
          description="Identifier for this monitored server"
        >
          <input
            type="text"
            value={formData.name}
            onChange={(e) => setFormData({ ...formData, name: e.target.value })}
            placeholder="Production server"
            className="input"
          />
        </FormField>

        <FormField
          label="Reporting interval (seconds)"
          required
          error={errors.expected_interval_seconds}
          infoTip="Agent is marked stale if no report arrives within twice this interval."
        >
          <input
            type="number"
            value={formData.expected_interval_seconds}
            onChange={(e) => setFormData({ ...formData, expected_interval_seconds: parseInt(e.target.value) || 60 })}
            min={10}
            step={10}
            className="input"
          />
        </FormField>

        <FormField label="Tags" description="Comma-separated, e.g. production, us-east-1">
          <input
            type="text"
            value={formData.tags}
            onChange={(e) => setFormData({ ...formData, tags: e.target.value })}
            placeholder="production, us-east-1"
            className="input"
          />
        </FormField>
      </FormSection>

      <FormSection title="Alert policies">
        {alertPolicies.length === 0 ? (
          <div className="text-sm text-slate-500">No alert policies configured.</div>
        ) : (
          <div className="space-y-2">
            {alertPolicies.map((policy) => {
              const checked = formData.alert_policy_ids.includes(policy.id);
              return (
                <label key={policy.id} className="flex items-center gap-2 text-sm text-slate-300">
                  <input
                    type="checkbox"
                    checked={checked}
                    onChange={(e) => {
                      const next = e.target.checked
                        ? [...formData.alert_policy_ids, policy.id]
                        : formData.alert_policy_ids.filter((id) => id !== policy.id);
                      setFormData({ ...formData, alert_policy_ids: next });
                    }}
                  />
                  {policy.name}
                </label>
              );
            })}
          </div>
        )}
      </FormSection>

      <FormSection title="Status">
        <div className="flex items-center justify-between rounded-lg border border-white/[0.06] bg-slate-900/40 px-4 py-3">
          <div>
            <p className="text-sm font-medium text-white">Agent enabled</p>
            <p className="text-xs text-slate-500">Accept metrics from this agent</p>
          </div>
          <button
            type="button"
            onClick={() => setFormData({ ...formData, enabled: !formData.enabled })}
            className={`relative h-5 w-9 rounded-full transition-colors ${
              formData.enabled ? 'bg-cyan-500' : 'bg-slate-700'
            }`}
            aria-pressed={formData.enabled}
            aria-label="Toggle agent enabled"
          >
            <span className={`absolute left-0.5 top-0.5 h-4 w-4 rounded-full bg-white transition-transform ${
              formData.enabled ? 'translate-x-4' : ''
            }`} />
          </button>
        </div>
      </FormSection>

      <FormActions
        cancel={onCancel ? { label: 'Cancel', onClick: onCancel, disabled: loading } : undefined}
        submit={{
          label: loading ? 'Saving…' : isEditMode ? 'Save changes' : 'Create agent monitor',
          loading,
          disabled: loading,
          type: 'submit',
        }}
      />

      {isEditMode && monitor && (
        <div className="border-t border-white/[0.06] pt-4">
          <Button
            variant="ghost"
            size="sm"
            type="button"
            icon={<Download strokeWidth={1.75} />}
            className="w-full justify-center"
            onClick={() => setShowInstallInstructions(true)}
          >
            View installation instructions
          </Button>
        </div>
      )}
    </form>
  );
}
