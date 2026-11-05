'use client';

import { useEffect, useState, type ReactNode } from 'react';
import Link from 'next/link';
import { convertRoutesToEscalationPolicy, getEscalationPolicies } from '@/lib/api';
import type { EscalationPolicy } from '@/lib/escalation';
import Button from '@/components/ui/Button';
import { useToast } from '@/components/ui/ToastProvider';
import { PolicySteps } from './PolicySteps';

interface RowProps {
  title: string;
  description?: ReactNode;
  control?: (ids: { titleId: string; descriptionId?: string }) => ReactNode;
  children?: ReactNode;
}

interface Props {
  /** The selected default policy id ('' = none: the default channels apply). */
  value: string;
  onChange: (id: string) => void;
  readOnly: boolean;
  hasDefaultChannels: boolean;
  /** The settings panel's row, so this row matches its neighbours. */
  renderRow: (props: RowProps) => ReactNode;
}

/**
 * The workspace default escalation policy: when set, default-mode monitors
 * (and alerts without a monitor) route through it instead of the default
 * channels. "Convert" builds a policy from the default channels and selects
 * it; the panel's save bar then makes it the default.
 */
export function DefaultPolicyRow({ value, onChange, readOnly, hasDefaultChannels, renderRow }: Props) {
  const { showToast } = useToast();
  const [policies, setPolicies] = useState<EscalationPolicy[]>([]);
  const [converting, setConverting] = useState(false);

  useEffect(() => {
    getEscalationPolicies().then(setPolicies).catch(() => setPolicies([]));
  }, []);

  const selected = policies.find((p) => p.id === value);

  const convert = async () => {
    setConverting(true);
    try {
      const p = await convertRoutesToEscalationPolicy({ assign: false });
      setPolicies((prev) => [...prev, p]);
      onChange(p.id);
      showToast(`Created “${p.name}”. Save to make it the default.`, 'success');
    } catch (err: any) {
      showToast(err.message || 'Conversion failed', 'error');
    } finally {
      setConverting(false);
    }
  };

  return renderRow({
    title: 'Default escalation policy',
    description: (
      <>
        Optional. Default-mode monitors follow this policy, paging people and channels step by step, instead of the
        default channels below.{' '}
        <Link href="/escalation-policies" className="font-medium text-cyan-300 underline decoration-cyan-300/40 underline-offset-2 hover:text-cyan-200">
          Manage policies
        </Link>
      </>
    ),
    control: ({ titleId, descriptionId }) => (
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        disabled={readOnly}
        aria-labelledby={titleId}
        aria-describedby={descriptionId}
        className="input input-sm !w-44 cursor-pointer"
      >
        <option value="">None (channels)</option>
        {policies.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
        {value && !selected && <option value={value}>Current policy</option>}
      </select>
    ),
    children: (
      <>
        {selected && (
          <div className="glass-well mt-3 px-4 py-3">
            <PolicySteps steps={selected.steps} />
          </div>
        )}
        {!readOnly && !value && hasDefaultChannels && (
          <Button size="xs" className="mt-3" onClick={convert} loading={converting}>
            Convert default channels to a policy
          </Button>
        )}
      </>
    ),
  });
}
