'use client';

import { useState, useEffect } from 'react';
import { Download } from 'lucide-react';
import { Monitor, CreateMonitorRequest, UpdateMonitorRequest, AgentMonitorConfig, AgentInstallCommand, ApiKey, DependencySuppression, NotificationMode, ChannelAssignment } from '@/lib/types';
import { getAgentInstallCommand, getApiKeys } from '@/lib/api';
import { getApiKey } from '@/lib/auth';
import { loadStoredApiKeys } from '@/lib/api-keys';
import FormField from '@/components/ui/FormField';
import FormSection from '@/components/ui/FormSection';
import FormActions from '@/components/ui/FormActions';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import { AlertingSection } from './AlertingSection';
import MetricRuleBuilder, {
  MetricRuleDraft,
  draftsFromRules,
  rulesFromDrafts,
} from './MetricRuleBuilder';
import Switch from '@/components/ui/Switch';

type ServerType =
  | 'linux-amd64'
  | 'linux-arm64'
  | 'macos-amd64'
  | 'macos-arm64'
  | 'windows-amd64';

type ServerTypeOption = {
  value: ServerType;
  label: string;
  family: 'unix' | 'windows';
  platform: 'linux' | 'darwin' | 'windows';
};

const SERVER_TYPE_OPTIONS: ServerTypeOption[] = [
  { value: 'linux-amd64', label: 'Linux (x86_64)', family: 'unix', platform: 'linux' },
  { value: 'linux-arm64', label: 'Linux (ARM64)', family: 'unix', platform: 'linux' },
  { value: 'macos-amd64', label: 'macOS (Intel)', family: 'unix', platform: 'darwin' },
  { value: 'macos-arm64', label: 'macOS (Apple Silicon)', family: 'unix', platform: 'darwin' },
  { value: 'windows-amd64', label: 'Windows (x86_64)', family: 'windows', platform: 'windows' },
];

const DEFAULT_SERVER_TYPE: ServerType = 'linux-amd64';

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
  const initialRules =
    (monitor && monitor.type === 'agent'
      ? (monitor.config as AgentMonitorConfig).metric_rules
      : initialAgentConfig?.metric_rules) || [];

  const [formData, setFormData] = useState({
    name: monitor?.name || initialData?.name || '',
    expected_interval_seconds: monitor && monitor.type === 'agent'
      ? (monitor.config as AgentMonitorConfig).expected_interval_seconds
      : initialAgentConfig?.expected_interval_seconds || 60,
    enabled: monitor?.enabled ?? initialData?.enabled ?? true,
    tags: monitor?.tags?.join(', ') || (initialData?.tags || []).join(', '),
    consecutive_failures_threshold: monitor?.consecutive_failures_threshold ?? 2,
    notification_mode: (monitor?.notification_mode ?? 'default') as NotificationMode,
    dependency_suppression: (monitor?.dependency_suppression ?? 'inherit') as DependencySuppression,
    notification_channels: monitor?.notification_channels ?? [] as ChannelAssignment[],
  });
  const [ruleDrafts, setRuleDrafts] = useState<MetricRuleDraft[]>(() => draftsFromRules(initialRules));

  const [errors, setErrors] = useState<Record<string, string>>({});
  const [ruleErrors, setRuleErrors] = useState<Record<number, string>>({});

  useEffect(() => {
    const key = getApiKey();
    if (key) setApiKeyState(key);
  }, []);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setErrors({});
    setRuleErrors({});

    const newErrors: Record<string, string> = {};
    if (!formData.name.trim()) newErrors.name = 'Name is required';
    if (formData.expected_interval_seconds < 10) newErrors.expected_interval_seconds = 'Minimum 10 seconds';

    const { rules, errors: newRuleErrors } = rulesFromDrafts(ruleDrafts);

    if (Object.keys(newErrors).length > 0 || Object.keys(newRuleErrors).length > 0) {
      setErrors(newErrors);
      setRuleErrors(newRuleErrors);
      return;
    }

    const config: AgentMonitorConfig = {
      agent_id: isEditMode ? monitor?.agent_id || '' : '',
      expected_interval_seconds: formData.expected_interval_seconds,
    };
    if (rules.length > 0) {
      config.metric_rules = rules;
    }

    const requestData: CreateMonitorRequest | UpdateMonitorRequest = {
      name: formData.name.trim(),
      type: 'agent',
      config,
      interval_seconds: formData.expected_interval_seconds,
      timeout_seconds: formData.expected_interval_seconds,
      enabled: formData.enabled,
    };

    requestData.consecutive_failures_threshold = formData.consecutive_failures_threshold;
    requestData.notification_mode = formData.notification_mode;
    requestData.dependency_suppression = formData.dependency_suppression;
    requestData.notification_channels = formData.notification_mode === 'custom' ? formData.notification_channels : [];
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
        setApiKeyError('No stored API key secrets found. Create a key in Settings → Access to use here.');
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
    const installScriptUrl = installCommand
      ? `${installCommand.backend_url}/api/v1/monitors/${monitor.id}/agent/install/script.sh`
      : '';
    const windowsInstallScriptUrl = installCommand
      ? `${installCommand.backend_url}/api/v1/monitors/${monitor.id}/agent/install/script.ps1`
      : '';
    const uninstallScriptUrl = installCommand
      ? `${installCommand.backend_url}/api/v1/monitors/${monitor.id}/agent/uninstall/script.sh`
      : '';
    const windowsUninstallScriptUrl = installCommand
      ? `${installCommand.backend_url}/api/v1/monitors/${monitor.id}/agent/uninstall/script.ps1`
      : '';
    const collectorConfigUrl = installCommand
      ? `${installCommand.backend_url}/api/v1/monitors/${monitor.id}/agent/config.yaml?platform=${selectedServer.platform}`
      : '';
    const linuxQuickInstallCommand = installCommand
      ? `curl -fsSL -H "Authorization: Bearer ${resolvedApiKey}" \
  "${installScriptUrl}" | sudo bash`
      : '';
    const linuxQuickInstallCopy = installCommand
      ? `curl -fsSL -H "Authorization: Bearer ${resolvedApiKey}" "${installScriptUrl}" | sudo bash`
      : '';
    const windowsQuickInstallCommand = installCommand
      ? `powershell -NoProfile -ExecutionPolicy Bypass -Command "Invoke-Expression (Invoke-WebRequest -UseBasicParsing -Headers @{Authorization='Bearer ${resolvedApiKey}'} -Uri '${windowsInstallScriptUrl}').Content"`
      : '';
    const linuxQuickUninstallCommand = installCommand
      ? `curl -fsSL -H "Authorization: Bearer ${resolvedApiKey}" \
  "${uninstallScriptUrl}" | sudo bash`
      : '';
    const linuxQuickUninstallCopy = installCommand
      ? `curl -fsSL -H "Authorization: Bearer ${resolvedApiKey}" "${uninstallScriptUrl}" | sudo bash`
      : '';
    const windowsQuickUninstallCommand = installCommand
      ? `powershell -NoProfile -ExecutionPolicy Bypass -Command "Invoke-Expression (Invoke-WebRequest -UseBasicParsing -Headers @{Authorization='Bearer ${resolvedApiKey}'} -Uri '${windowsUninstallScriptUrl}').Content"`
      : '';
    const quickInstallCommand = installCommand
      ? (isWindows ? windowsQuickInstallCommand : linuxQuickInstallCommand)
      : '';
    const quickInstallCopy = installCommand
      ? (isWindows ? windowsQuickInstallCommand : linuxQuickInstallCopy)
      : '';
    const quickUninstallCommand = installCommand
      ? (isWindows ? windowsQuickUninstallCommand : linuxQuickUninstallCommand)
      : '';
    const quickUninstallCopy = installCommand
      ? (isWindows ? windowsQuickUninstallCommand : linuxQuickUninstallCopy)
      : '';

    return (
      <div className="space-y-5">
        <FormSection title="Collector installation">
          {loadingInstallCmd ? (
            <p className="text-sm text-slate-500">Loading installation details…</p>
          ) : installCommand ? (
            <>
              <p className="text-[13px] text-slate-400">
                The install script sets up <span className="font-mono text-slate-300">probara-collector</span>
                {' '}(an OpenTelemetry Collector distribution
                {installCommand.collector_version ? `, v${installCommand.collector_version}` : ''})
                as a system service that pushes OTLP host metrics to this monitor.
              </p>

              <div className="grid gap-4 sm:grid-cols-2">
                <FormField label="Agent ID">
                  <div className="flex gap-2">
                    <code className="glass-well flex-1 overflow-x-auto px-3 py-2 font-mono text-xs text-slate-200">
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
                        className="input"
                      >
                        {apiKeyOptions.map((option) => (
                          <option key={option.id} value={option.id}>{option.label}</option>
                        ))}
                      </select>
                    ) : null}
                    <div className="flex gap-2">
                      <code className="glass-well flex-1 overflow-x-auto px-3 py-2 font-mono text-xs text-slate-200">
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
                      <p className="text-xs text-slate-500">No stored API keys. Create one in Settings → Access to auto-fill.</p>
                    )}
                  </div>
                </FormField>
              </div>

              <FormField label="Server type">
                <select
                  value={serverType}
                  onChange={(e) => setServerType(e.target.value as ServerType)}
                  className="input"
                >
                  {SERVER_TYPE_OPTIONS.map((option) => (
                    <option key={option.value} value={option.value}>{option.label}</option>
                  ))}
                </select>
                <p className="mt-1 text-xs text-slate-500">Where the collector will run</p>
              </FormField>

              <FormField label={isWindows ? 'Install service (Windows)' : 'Install service (Linux/macOS)'}>
                <div className="flex gap-2">
                  <pre className="glass-well flex-1 overflow-x-auto whitespace-pre-wrap px-3 py-2 font-mono text-xs text-slate-300">{quickInstallCommand}</pre>
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

              <FormField label={isWindows ? 'Uninstall service (Windows)' : 'Uninstall service (Linux/macOS)'}>
                <div className="flex gap-2">
                  <pre className="glass-well flex-1 overflow-x-auto whitespace-pre-wrap px-3 py-2 font-mono text-xs text-slate-300">{quickUninstallCommand}</pre>
                  <Button
                    variant="ghost"
                    size="xs"
                    type="button"
                    className="self-start"
                    onClick={() => copyToClipboard(quickUninstallCopy, 'uninstall')}
                  >
                    {copiedField === 'uninstall' ? 'Copied' : 'Copy'}
                  </Button>
                </div>
              </FormField>

              <FormField label="Collector config">
                <div className="flex gap-2">
                  <pre className="glass-well max-h-64 flex-1 overflow-auto whitespace-pre px-3 py-2 font-mono text-xs text-slate-300">{installCommand.collector_config}</pre>
                  <Button
                    variant="ghost"
                    size="xs"
                    type="button"
                    className="self-start"
                    onClick={() => copyToClipboard(installCommand.collector_config, 'collector_config')}
                  >
                    {copiedField === 'collector_config' ? 'Copied' : 'Copy'}
                  </Button>
                </div>
                <p className="mt-1 text-xs text-slate-500">Generated OpenTelemetry Collector configuration (Linux variant) — the install script writes it for you</p>
                <p className="mt-2 text-xs text-slate-500">
                  Managing hosts with Ansible/Chef/Puppet? Fetch the raw per-platform YAML from{' '}
                  <button
                    type="button"
                    className="font-mono text-cyan-300 hover:text-cyan-200"
                    onClick={() => copyToClipboard(collectorConfigUrl, 'config_url')}
                    title="Copy URL"
                  >
                    {copiedField === 'config_url' ? 'Copied' : `…/agent/config.yaml?platform=${selectedServer.platform}`}
                  </button>{' '}
                  and ship the <span className="font-mono">probara-collector</span> binary from{' '}
                  <span className="font-mono break-all">{installCommand.download_url}</span>.
                </p>
              </FormField>

              <div className="glass-well p-4">
                <p className="text-[13px] font-medium text-white">Collected metrics</p>
                <p className="mt-1 text-xs text-slate-400">
                  OpenTelemetry hostmetrics: CPU and memory utilization, swap, per-mount filesystem
                  usage, disk and network I/O, load averages, process count, and uptime — stored as
                  queryable metric series.
                </p>
              </div>
            </>
          ) : (
            <Callout tone="danger">Failed to load installation details</Callout>
          )}
        </FormSection>

        <div className="flex items-center justify-end gap-2">
          <Button variant="ghost" type="button" onClick={() => setShowInstallInstructions(false)}>
            Back to settings
          </Button>
          {onCancel && (
            <Button variant="accent" type="button" onClick={onCancel}>
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
        >
          <input
            type="text"
            value={formData.name}
            onChange={(e) => setFormData({ ...formData, name: e.target.value })}
            placeholder="Production server"
            className="input"
          />
          <p className="mt-1 text-xs text-slate-500">Identifier for this monitored server</p>
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

        <FormField label="Tags">
          <input
            type="text"
            value={formData.tags}
            onChange={(e) => setFormData({ ...formData, tags: e.target.value })}
            placeholder="production, us-east-1"
            className="input"
          />
          <p className="mt-1 text-xs text-slate-500">Comma-separated tags for filtering</p>
        </FormField>

        <div className="flex items-center justify-between gap-4">
          <div className="min-w-0">
            <p className="text-sm font-medium text-slate-200">Agent enabled</p>
            <p className="text-xs text-slate-500">Accept metrics from this agent</p>
          </div>
          <Switch checked={formData.enabled} onChange={() => setFormData({ ...formData, enabled: !formData.enabled })} label="Toggle agent enabled" />
        </div>

        {isEditMode && monitor && (
          <div>
            <Button
              variant="ghost"
              type="button"
              icon={<Download strokeWidth={1.75} />}
              onClick={() => setShowInstallInstructions(true)}
            >
              View installation instructions
            </Button>
          </div>
        )}
      </FormSection>

      <FormSection title="Alerting">
        <AlertingSection
          isGroup={false}
          intervalSeconds={formData.expected_interval_seconds}
          threshold={formData.consecutive_failures_threshold}
          onThresholdChange={(n) => setFormData({ ...formData, consecutive_failures_threshold: n })}
          mode={formData.notification_mode}
          onModeChange={(m) => setFormData({ ...formData, notification_mode: m })}
          dependencySuppression={formData.dependency_suppression}
          hasDependencies={(monitor?.depends_on_ids?.length ?? 0) > 0}
          onDependencySuppressionChange={(d) => setFormData({ ...formData, dependency_suppression: d })}
          customChannels={formData.notification_channels}
          onCustomChannelsChange={(next) => setFormData({ ...formData, notification_channels: next })}
        />
      </FormSection>

      <FormSection title="Metric alert rules">
        <p className="text-[13px] text-slate-400">
          Open an alert when a reported metric breaches its threshold, optionally only after it is
          sustained for a duration. Utilization thresholds are entered as percentages. Evaluated
          independently of up/down status.
        </p>
        <MetricRuleBuilder
          drafts={ruleDrafts}
          onChange={setRuleDrafts}
          errors={ruleErrors}
          monitorId={isEditMode ? monitor?.id : undefined}
        />
      </FormSection>

      <FormActions
        middle={`Expect an agent report every ${formData.expected_interval_seconds}s`}
        cancel={onCancel ? { label: 'Cancel', onClick: onCancel, disabled: loading } : undefined}
        submit={{
          label: loading ? 'Saving…' : isEditMode ? 'Save changes' : 'Create agent monitor',
          loading,
          disabled: loading,
          type: 'submit',
        }}
      />
    </form>
  );
}
