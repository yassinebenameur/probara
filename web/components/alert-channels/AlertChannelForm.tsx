'use client';

import { useState, type FormEvent, type ReactNode } from 'react';
import { AlertChannel, AlertChannelType, CreateAlertChannelRequest, UpdateAlertChannelRequest } from '@/lib/types';

interface AlertChannelFormProps {
  channel?: AlertChannel;
  onSubmit: (data: CreateAlertChannelRequest | UpdateAlertChannelRequest) => Promise<void>;
  onCancel?: () => void;
  loading?: boolean;
}

const channelTypes: {
  value: AlertChannelType;
  label: string;
  description: string;
  icon: ReactNode;
}[] = [
  {
    value: 'teams',
    label: 'Microsoft Teams',
    description: 'Send alerts to a Teams incoming webhook',
    icon: (
      <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M8 9h8m-8 4h5m-6 5h10a2 2 0 002-2V8a2 2 0 00-2-2H9l-4 4v8a2 2 0 002 2z" />
      </svg>
    ),
  },
  {
    value: 'email',
    label: 'Email',
    description: 'Send alerts via SMTP to one or more recipients',
    icon: (
      <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M3 8l7.89 5.26a2 2 0 002.22 0L21 8m-18 8h18a2 2 0 002-2V8a2 2 0 00-2-2H3a2 2 0 00-2 2v6a2 2 0 002 2z" />
      </svg>
    ),
  },
];

const emailTemplateTokens = [
  'monitor_name',
  'policy_name',
  'status',
  'failure_count',
  'last_error',
  'triggered_at',
  'resolved_at',
  'tenant_id',
];

const defaultEmailBodyTemplate =
  'Monitor: {{monitor_name}}\nPolicy: {{policy_name}}\nStatus: {{status}}\nTriggered At: {{triggered_at}}\nFailure Count: {{failure_count}}\nLast Error: {{last_error}}';

function extractEmailList(channel?: AlertChannel): string {
  const raw = (channel?.config as any)?.to;
  if (Array.isArray(raw)) {
    return raw.join(', ');
  }
  if (typeof raw === 'string') {
    return raw;
  }
  return '';
}

function extractEmailBody(channel?: AlertChannel): string {
  const config = channel?.config as any;
  if (!config) return '';
  return config.body || config.email_body || config.body_template || '';
}

function splitEmails(value: string): string[] {
  return value
    .split(/[,\n;]+/)
    .map((email) => email.trim())
    .filter((email) => email.length > 0);
}

function normalizeEmails(emails: string[]): string[] {
  const seen = new Set<string>();
  const normalized: string[] = [];

  emails.forEach((email) => {
    if (!email.includes('@')) {
      return;
    }
    const key = email.toLowerCase();
    if (seen.has(key)) {
      return;
    }
    seen.add(key);
    normalized.push(email);
  });

  return normalized;
}

function FormField({
  label,
  error,
  hint,
  children,
}: {
  label: string;
  error?: string;
  hint?: string;
  children: ReactNode;
}) {
  return (
    <div>
      <label className="block text-xs font-medium text-slate-400 mb-1.5">{label}</label>
      {children}
      {error && <p className="mt-1 text-xs text-rose-400">{error}</p>}
      {hint && !error && <p className="mt-1 text-xs text-slate-500">{hint}</p>}
    </div>
  );
}

function FormInput({
  label,
  type = 'text',
  value,
  onChange,
  placeholder,
  error,
  hint,
  ...props
}: {
  label: string;
  type?: string;
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  error?: string;
  hint?: string;
  [key: string]: any;
}) {
  return (
    <FormField label={label} error={error} hint={hint}>
      <input
        type={type}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        className="input"
        {...props}
      />
    </FormField>
  );
}

function FormTextArea({
  label,
  value,
  onChange,
  placeholder,
  rows = 4,
  error,
  hint,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  rows?: number;
  error?: string;
  hint?: string;
}) {
  return (
    <FormField label={label} error={error} hint={hint}>
      <textarea
        className="input min-h-[120px] resize-y"
        rows={rows}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
      />
    </FormField>
  );
}

function FormToggle({
  label,
  description,
  checked,
  onChange,
}: {
  label: string;
  description?: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
}) {
  return (
    <div className="flex items-center justify-between rounded-lg border border-white/[0.08] bg-slate-800/30 px-4 py-3">
      <div>
        <p className="text-sm font-medium text-white">{label}</p>
        {description && <p className="text-xs text-slate-500">{description}</p>}
      </div>
      <button
        type="button"
        onClick={() => onChange(!checked)}
        className={`relative h-5 w-9 rounded-full transition-colors ${
          checked ? 'bg-cyan-500' : 'bg-slate-700'
        }`}
      >
        <span
          className={`absolute top-0.5 left-0.5 h-4 w-4 rounded-full bg-white transition-transform ${
            checked ? 'translate-x-4' : ''
          }`}
        />
      </button>
    </div>
  );
}

function SectionHeader({ title, description }: { title: string; description?: string }) {
  return (
    <div className="mb-4">
      <h3 className="text-sm font-medium text-white">{title}</h3>
      {description && <p className="text-xs text-slate-500 mt-0.5">{description}</p>}
    </div>
  );
}

function TypeCard({
  icon,
  label,
  description,
  selected,
  onClick,
  disabled,
}: {
  icon: ReactNode;
  label: string;
  description: string;
  selected: boolean;
  onClick: () => void;
  disabled?: boolean;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      className={`flex items-start gap-3 rounded-lg border p-3 text-left transition-all ${
        selected
          ? 'border-cyan-500/50 bg-cyan-500/10'
          : 'border-white/[0.06] bg-slate-800/30 hover:border-white/[0.1] hover:bg-slate-800/50'
      } ${disabled ? 'opacity-50 cursor-not-allowed' : 'cursor-pointer'}`}
    >
      <div className={`rounded-lg p-2 ${selected ? 'bg-cyan-500/20 text-cyan-400' : 'bg-slate-700/50 text-slate-400'}`}>
        {icon}
      </div>
      <div>
        <p className={`text-sm font-medium ${selected ? 'text-white' : 'text-slate-300'}`}>{label}</p>
        <p className="text-xs text-slate-500 mt-0.5">{description}</p>
      </div>
    </button>
  );
}

export default function AlertChannelForm({
  channel,
  onSubmit,
  onCancel,
  loading = false,
}: AlertChannelFormProps) {
  const existingWebhook = (channel?.config as any)?.webhook_url || '';
  const existingEmails = extractEmailList(channel);
  const existingBody = extractEmailBody(channel);

  const [formData, setFormData] = useState({
    name: channel?.name || '',
    type: (channel?.type || 'teams') as AlertChannelType,
    webhook_url: existingWebhook,
    email_to: existingEmails,
    email_body: existingBody,
    is_active: channel?.is_active ?? true,
  });
  const [showWebhook, setShowWebhook] = useState(false);
  const [errors, setErrors] = useState<Record<string, string>>({});

  const appendEmailToken = (token: string) => {
    setFormData((prev) => ({
      ...prev,
      email_body: `${prev.email_body} {{${token}}}`.trim(),
    }));
  };

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setErrors({});

    const newErrors: Record<string, string> = {};
    if (!formData.name.trim()) {
      newErrors.name = 'Name is required';
    }
    if (formData.type === 'teams' && !formData.webhook_url.trim()) {
      newErrors.webhook_url = 'Webhook URL is required';
    }
    if (formData.type === 'email') {
      const recipients = normalizeEmails(splitEmails(formData.email_to));
      if (recipients.length === 0) {
        newErrors.email_to = 'At least one valid email is required';
      }
    }

    if (Object.keys(newErrors).length > 0) {
      setErrors(newErrors);
      return;
    }

    const config: Record<string, any> = {};
    if (formData.type === 'teams') {
      if (formData.webhook_url.trim()) {
        config.webhook_url = formData.webhook_url.trim();
      }
    }
    if (formData.type === 'email') {
      config.to = normalizeEmails(splitEmails(formData.email_to));
      if (formData.email_body.trim()) {
        config.body = formData.email_body.trim();
      }
    }

    let requestData: CreateAlertChannelRequest | UpdateAlertChannelRequest;
    if (channel) {
      const updateData: UpdateAlertChannelRequest = {
        name: formData.name.trim(),
        is_active: formData.is_active,
        config,
      };
      requestData = updateData;
    } else {
      requestData = {
        name: formData.name.trim(),
        type: formData.type,
        is_active: formData.is_active,
        config,
      };
    }

    await onSubmit(requestData);
  };

  return (
    <form onSubmit={handleSubmit} className="space-y-6">
      <div>
        <SectionHeader title="Channel Type" description="Choose how alerts should be delivered" />
        <div className="grid grid-cols-2 gap-3">
          {channelTypes.map((opt) => (
            <TypeCard
              key={opt.value}
              icon={opt.icon}
              label={opt.label}
              description={opt.description}
              selected={formData.type === opt.value}
              onClick={() => setFormData({ ...formData, type: opt.value })}
              disabled={!!channel}
            />
          ))}
        </div>
      </div>

      <div>
        <SectionHeader title="Basic Information" />
        <div className="space-y-4">
          <FormInput
            label="Channel Name"
            value={formData.name}
            onChange={(v) => setFormData({ ...formData, name: v })}
            placeholder="Primary Teams Channel"
            error={errors.name}
            hint="A descriptive name for this channel"
          />
        </div>
      </div>

      <div>
        <SectionHeader title="Configuration" description="Provide the details for the selected channel" />

        {formData.type === 'teams' && (
          <FormField
            label="Webhook URL"
            error={errors.webhook_url}
            hint="Paste the Teams incoming webhook URL for this channel"
          >
            <div className="flex items-center gap-2">
              <input
                type={showWebhook ? 'text' : 'password'}
                value={formData.webhook_url}
                onChange={(e) => setFormData({ ...formData, webhook_url: e.target.value })}
                placeholder="https://outlook.office.com/webhook/..."
                className="input"
              />
              <button
                type="button"
                onClick={() => setShowWebhook(!showWebhook)}
                className="btn btn-secondary btn-sm"
              >
                {showWebhook ? 'Hide' : 'Show'}
              </button>
            </div>
          </FormField>
        )}

        {formData.type === 'email' && (
          <div className="space-y-4">
            <FormInput
              label="Recipients"
              value={formData.email_to}
              onChange={(v) => setFormData({ ...formData, email_to: v })}
              placeholder="oncall@example.com, team@example.com"
              error={errors.email_to}
              hint="Separate multiple emails with commas, semicolons, or new lines"
            />
            <div className="space-y-4 rounded-xl border border-white/[0.08] bg-slate-800/30 p-4">
              <div>
                <h3 className="text-sm font-semibold text-white">Email Body Template (Optional)</h3>
                <p className="mt-1 text-xs text-slate-500">
                  Customize the email content for this channel. Leave empty to use the default template.
                </p>
              </div>

              <div className="flex flex-wrap gap-2 text-xs">
                {emailTemplateTokens.map((token) => (
                  <span
                    key={token}
                    className="badge badge-default"
                  >
                    {`{{${token}}}`}
                  </span>
                ))}
              </div>

              <FormTextArea
                label="Email Body"
                value={formData.email_body}
                onChange={(v) => setFormData({ ...formData, email_body: v })}
                placeholder="Monitor: {{monitor_name}}"
                rows={6}
                hint="Use the variables below to include alert details."
              />

              <div className="flex flex-wrap gap-2 text-xs">
                {emailTemplateTokens.map((token) => (
                  <button
                    key={token}
                    type="button"
                    onClick={() => appendEmailToken(token)}
                    className="btn btn-outline btn-xs"
                    aria-label={`Insert {{${token}}} into email body`}
                  >
                    {`{{${token}}}`}
                  </button>
                ))}
              </div>

              <div className="flex flex-wrap gap-2">
                <button
                  type="button"
                  onClick={() =>
                    setFormData({
                      ...formData,
                      email_body: defaultEmailBodyTemplate,
                    })
                  }
                  className="btn btn-secondary btn-sm"
                >
                  Use Default Template
                </button>
                <button
                  type="button"
                  onClick={() =>
                    setFormData({
                      ...formData,
                      email_body: '',
                    })
                  }
                  className="btn btn-secondary btn-sm"
                >
                  Clear Template
                </button>
              </div>
            </div>
          </div>
        )}
      </div>

      <div>
        <SectionHeader title="Status" />
        <FormToggle
          label="Channel Active"
          description="Deliver alerts using this channel"
          checked={formData.is_active}
          onChange={(v) => setFormData({ ...formData, is_active: v })}
        />
      </div>

      <div className="flex items-center justify-end gap-3 pt-4 border-t border-white/[0.06]">
        {onCancel && (
          <button
            type="button"
            onClick={onCancel}
            disabled={loading}
            className="btn btn-secondary btn-sm disabled:opacity-50"
          >
            Cancel
          </button>
        )}
        <button
          type="submit"
          disabled={loading}
          className="btn btn-primary btn-sm disabled:opacity-50"
        >
          {loading ? 'Saving...' : channel ? 'Update Channel' : 'Create Channel'}
        </button>
      </div>
    </form>
  );
}
