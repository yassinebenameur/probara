'use client';

import { useState, type FormEvent, type ReactNode } from 'react';
import { Mail, MessageSquare } from 'lucide-react';
import { AlertChannel, AlertChannelType, CreateAlertChannelRequest, UpdateAlertChannelRequest } from '@/lib/types';
import FormField from '@/components/ui/FormField';
import FormSection from '@/components/ui/FormSection';
import FormActions from '@/components/ui/FormActions';
import Button from '@/components/ui/Button';
import Pill from '@/components/ui/Pill';

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
    icon: <MessageSquare className="h-4 w-4" strokeWidth={1.75} />,
  },
  {
    value: 'email',
    label: 'Email',
    description: 'Send alerts via SMTP to one or more recipients',
    icon: <Mail className="h-4 w-4" strokeWidth={1.75} />,
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
          : 'border-white/[0.06] bg-slate-900/40 hover:border-white/[0.1] hover:bg-slate-900/60'
      } ${disabled ? 'opacity-50 cursor-not-allowed' : 'cursor-pointer'}`}
    >
      <div className={`rounded-lg p-2 ${selected ? 'bg-cyan-500/20 text-cyan-300' : 'bg-slate-800/60 text-slate-400'}`}>
        {icon}
      </div>
      <div>
        <p className={`text-sm font-medium ${selected ? 'text-white' : 'text-slate-300'}`}>{label}</p>
        <p className="mt-0.5 text-xs text-slate-500">{description}</p>
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
      <FormSection title="Channel type" summary="How alerts are delivered">
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
      </FormSection>

      <FormSection title="Basics">
        <FormField label="Channel name" required error={errors.name}>
          <input
            type="text"
            value={formData.name}
            onChange={(e) => setFormData({ ...formData, name: e.target.value })}
            placeholder="Primary Teams channel"
            className="input"
          />
        </FormField>
      </FormSection>

      <FormSection title="Configuration">
        {formData.type === 'teams' && (
          <FormField
            label="Webhook URL"
            required
            error={errors.webhook_url}
            infoTip="Paste the Teams incoming webhook URL for this channel. Click Show to reveal the value."
          >
            <div className="flex items-center gap-2">
              <input
                type={showWebhook ? 'text' : 'password'}
                value={formData.webhook_url}
                onChange={(e) => setFormData({ ...formData, webhook_url: e.target.value })}
                placeholder="https://outlook.office.com/webhook/..."
                className="input"
              />
              <Button
                variant="ghost"
                size="sm"
                type="button"
                onClick={() => setShowWebhook(!showWebhook)}
              >
                {showWebhook ? 'Hide' : 'Show'}
              </Button>
            </div>
          </FormField>
        )}

        {formData.type === 'email' && (
          <>
            <FormField
              label="Recipients"
              required
              error={errors.email_to}
              description="Separate emails with commas, semicolons, or new lines"
            >
              <input
                type="text"
                value={formData.email_to}
                onChange={(e) => setFormData({ ...formData, email_to: e.target.value })}
                placeholder="oncall@example.com, team@example.com"
                className="input"
              />
            </FormField>

            <div className="space-y-4 rounded-lg border border-white/[0.06] bg-slate-900/40 p-4">
              <div>
                <h4 className="text-sm font-medium text-white">Email body template</h4>
                <p className="mt-0.5 text-xs text-slate-500">
                  Optional. Leave empty to use the default.
                </p>
              </div>

              <div className="flex flex-wrap gap-1.5">
                {emailTemplateTokens.map((token) => (
                  <Pill key={token} tone="neutral" size="xs">{`{{${token}}}`}</Pill>
                ))}
              </div>

              <FormField label="Email body">
                <textarea
                  className="input min-h-[120px] resize-y"
                  rows={6}
                  value={formData.email_body}
                  onChange={(e) => setFormData({ ...formData, email_body: e.target.value })}
                  placeholder="Monitor: {{monitor_name}}"
                />
              </FormField>

              <div>
                <p className="mb-1.5 text-xs text-slate-500">Insert variable</p>
                <div className="flex flex-wrap gap-1.5">
                  {emailTemplateTokens.map((token) => (
                    <Button
                      key={token}
                      variant="ghost"
                      size="xs"
                      type="button"
                      onClick={() => appendEmailToken(token)}
                      aria-label={`Insert {{${token}}} into email body`}
                    >
                      {`{{${token}}}`}
                    </Button>
                  ))}
                </div>
              </div>

              <div className="flex flex-wrap gap-2">
                <Button
                  variant="ghost"
                  size="sm"
                  type="button"
                  onClick={() =>
                    setFormData({ ...formData, email_body: defaultEmailBodyTemplate })
                  }
                >
                  Use default template
                </Button>
                <Button
                  variant="ghost"
                  size="sm"
                  type="button"
                  onClick={() => setFormData({ ...formData, email_body: '' })}
                >
                  Clear template
                </Button>
              </div>
            </div>
          </>
        )}
      </FormSection>

      <FormSection title="Status">
        <div className="flex items-center justify-between rounded-lg border border-white/[0.06] bg-slate-900/40 px-4 py-3">
          <div>
            <p className="text-sm font-medium text-white">Channel active</p>
            <p className="text-xs text-slate-500">Deliver alerts using this channel</p>
          </div>
          <button
            type="button"
            onClick={() => setFormData({ ...formData, is_active: !formData.is_active })}
            className={`relative h-5 w-9 rounded-full transition-colors ${
              formData.is_active ? 'bg-cyan-500' : 'bg-slate-700'
            }`}
            aria-pressed={formData.is_active}
            aria-label="Toggle channel active"
          >
            <span
              className={`absolute left-0.5 top-0.5 h-4 w-4 rounded-full bg-white transition-transform ${
                formData.is_active ? 'translate-x-4' : ''
              }`}
            />
          </button>
        </div>
      </FormSection>

      <FormActions
        cancel={onCancel ? { label: 'Cancel', onClick: onCancel, disabled: loading } : undefined}
        submit={{
          label: loading ? 'Saving…' : channel ? 'Update channel' : 'Create channel',
          loading,
          disabled: loading,
          type: 'submit',
        }}
      />
    </form>
  );
}
