'use client';

import FormSection from '@/components/ui/FormSection';
import { MonitorFormState } from '@/lib/monitor-form/inline';
import { FormInput, FormSelect } from './form-kit';

const RECORD_TYPES = [
  { value: 'A', label: 'A (IPv4)' },
  { value: 'AAAA', label: 'AAAA (IPv6)' },
  { value: 'CNAME', label: 'CNAME' },
  { value: 'TXT', label: 'TXT' },
  { value: 'MX', label: 'MX' },
  { value: 'NS', label: 'NS' },
];

type DnsFields = Pick<MonitorFormState, 'dns_record_type' | 'dns_expected_answers' | 'dns_nameserver'>;

/** The DNS monitor's Check section. The host sits in MonitorForm's Target section. */
export default function DnsEditor({
  data,
  onChange,
}: {
  data: DnsFields;
  onChange: (patch: Partial<DnsFields>) => void;
}) {
  return (
    <FormSection title="Check">
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <FormSelect
          label="Record type"
          value={data.dns_record_type}
          onChange={(v) => onChange({ dns_record_type: v })}
          options={RECORD_TYPES}
          hint="Defaults to A if not set."
        />
        <FormInput
          label="Expected answers"
          value={data.dns_expected_answers}
          onChange={(v) => onChange({ dns_expected_answers: v })}
          placeholder="1.2.3.4, 5.6.7.8"
          hint="Optional. Comma or newline separated; empty accepts any answer."
          className="input font-mono"
        />
      </div>
      <FormInput
        label="Nameserver"
        value={data.dns_nameserver}
        onChange={(v) => onChange({ dns_nameserver: v })}
        placeholder="10.0.0.2 or resolver.internal:53"
        hint="Optional. Query this DNS server instead of the worker's system resolver, for private zones and VPC resolvers."
        className="input font-mono"
      />
    </FormSection>
  );
}
