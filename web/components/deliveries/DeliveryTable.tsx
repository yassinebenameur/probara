import Link from 'next/link';
import type { NotificationDelivery } from '@/lib/types';
import { formatDateTime, formatDateTimeFull, formatLatency } from '@/lib/format';
import { deliveryOutcomeTone } from '@/lib/status-tones';
import {
  deliveryAlertSubject,
  deliveryDetail,
  deliveryEventLabel,
  deliveryOutcomeLabel,
  deliveryPathLabel,
  deliveryReasonHint,
  deliveryReasonLabel,
} from '@/lib/deliveries';
import Pill from '@/components/ui/Pill';

interface DeliveryTableProps {
  items: NotificationDelivery[];
  /** alert: one alert's attempts, by channel; channel: one channel's attempts, by alert. */
  scope: 'alert' | 'channel';
}

// The rows of the notification delivery history. No surface of its own: the
// alert view sits inside a feed row, the channel view inside a Panel.
export default function DeliveryTable({ items, scope }: DeliveryTableProps) {
  return (
    <div className="overflow-x-auto">
      <table className="data-table text-sm">
        <thead>
          <tr>
            <th>Time</th>
            <th>{scope === 'alert' ? 'Channel' : 'Alert'}</th>
            <th className="hidden sm:table-cell">Event</th>
            <th>Outcome</th>
            <th className="hidden md:table-cell">Detail</th>
          </tr>
        </thead>
        <tbody>
          {items.map((d) => (
            <DeliveryRow key={d.id} delivery={d} scope={scope} />
          ))}
        </tbody>
      </table>
    </div>
  );
}

function DeliveryRow({ delivery: d, scope }: { delivery: NotificationDelivery; scope: 'alert' | 'channel' }) {
  const event = `${deliveryEventLabel(d)} · attempt ${d.attempt}`;
  const reason = deliveryReasonLabel(d.reason);
  const detail = deliveryDetail(d);
  const timing = d.duration_ms !== null ? ` · ${formatLatency(d.duration_ms)}` : '';
  return (
    <tr>
      <td className="whitespace-nowrap text-ui text-slate-300" title={formatDateTimeFull(d.created_at)}>
        {formatDateTime(d.created_at)}
        <div className="mt-0.5 text-xs text-slate-500 sm:hidden">{event}</div>
      </td>
      <td className="max-w-[16rem]">
        {scope === 'alert' ? (
          <Link
            href={`/alert-channels/${d.channel_id}`}
            className="block truncate text-white transition-colors hover:text-cyan-300"
            title={d.channel_name}
          >
            {d.channel_name}
          </Link>
        ) : d.alert_id ? (
          <Link
            href={`/alerts?alert=${d.alert_id}`}
            className="block truncate text-white transition-colors hover:text-cyan-300"
            title={deliveryAlertSubject(d)}
          >
            {deliveryAlertSubject(d)}
          </Link>
        ) : (
          <span className="block truncate text-slate-300">{deliveryAlertSubject(d)}</span>
        )}
        <div className="mt-0.5 text-xs text-slate-500">
          {deliveryPathLabel(d)}
          {timing}
        </div>
      </td>
      <td className="hidden whitespace-nowrap text-ui text-slate-400 sm:table-cell">{event}</td>
      <td>
        <Pill tone={deliveryOutcomeTone(d.outcome, d.reason)} size="xs" dot>
          {deliveryOutcomeLabel(d)}
        </Pill>
        {reason && (
          <div className="mt-1 text-xs text-slate-400" title={deliveryReasonHint(d.reason)}>
            {reason}
          </div>
        )}
        {detail && (
          <div className="mt-1 max-w-[16rem] truncate text-xs text-slate-500 md:hidden" title={detail}>
            {detail}
          </div>
        )}
      </td>
      <td className="hidden max-w-md md:table-cell">
        {detail ? (
          <span className="line-clamp-2 break-words font-mono text-xs text-slate-400" title={detail}>
            {detail}
          </span>
        ) : (
          <span className="text-xs text-slate-600">—</span>
        )}
      </td>
    </tr>
  );
}
