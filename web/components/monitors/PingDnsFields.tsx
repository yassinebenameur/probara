'use client';

import FormSection from '@/components/ui/FormSection';
import type { FormErrors } from '@/lib/monitor-form/common';
import type { DNSFormData } from '@/lib/monitor-form/dns';
import type { PingFormData } from '@/lib/monitor-form/ping';
import { FormInput, FormSelect } from './MonitorFormControls';

const DNS_RECORD_TYPES = [
  { value: 'A', label: 'A (IPv4)' },
  { value: 'AAAA', label: 'AAAA (IPv6)' },
  { value: 'CNAME', label: 'CNAME' },
  { value: 'TXT', label: 'TXT' },
  { value: 'MX', label: 'MX' },
  { value: 'NS', label: 'NS' },
];

export function PingFields({
  value,
  onChange,
  errors,
}: {
  value: PingFormData;
  onChange: (patch: Partial<PingFormData>) => void;
  errors: FormErrors;
}) {
  return (
    <FormSection title="Ping configuration">
      <FormInput
        label="Host"
        value={value.host}
        onChange={(host) => onChange({ host })}
        placeholder="example.com or 8.8.8.8"
        error={errors.host}
        hint="Hostname or IP address to ping"
      />
    </FormSection>
  );
}

export function DnsFields({
  value,
  onChange,
  errors,
}: {
  value: DNSFormData;
  onChange: (patch: Partial<DNSFormData>) => void;
  errors: FormErrors;
}) {
  return (
    <FormSection title="DNS configuration">
      <FormInput
        label="Host"
        value={value.host}
        onChange={(host) => onChange({ host })}
        placeholder="example.com"
        error={errors.host}
      />
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <FormSelect
          label="Record Type"
          value={value.record_type}
          onChange={(record_type) => onChange({ record_type })}
          options={DNS_RECORD_TYPES}
          hint="Defaults to A if not set."
        />
        <FormInput
          label="Expected Answers (optional)"
          value={value.expected_answers}
          onChange={(expected_answers) => onChange({ expected_answers })}
          placeholder="1.2.3.4, 5.6.7.8"
          hint="Comma or newline separated. Leave empty to accept any answer."
        />
      </div>
      <FormInput
        label="Nameserver (optional)"
        value={value.nameserver}
        onChange={(nameserver) => onChange({ nameserver })}
        placeholder="10.0.0.2 or resolver.internal:53"
        hint="Query this DNS server instead of the worker's system resolver — for private zones and VPC resolvers."
      />
    </FormSection>
  );
}
