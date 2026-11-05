import {
  Activity,
  Boxes,
  Braces,
  Cable,
  CalendarClock,
  CircleDot,
  Database,
  Globe2,
  Leaf,
  MessageSquare,
  MonitorSmartphone,
  Network,
  Radio,
  Search,
  Send,
  Server,
  Webhook,
  Workflow,
  Zap,
} from 'lucide-react';

// The landing page's monitor type catalogue. MONITOR_TYPE_COUNT is derived
// from it, so a new type is one entry here and every "N monitor types" on
// the site follows.
export const monitorGroups = [
  {
    label: 'Web & API',
    icon: Globe2,
    monitors: [
      { name: 'HTTP', icon: Globe2 },
      { name: 'WebSocket', icon: Cable },
      { name: 'Synthetic API', icon: Braces },
      { name: 'Synthetic Browser', icon: MonitorSmartphone },
    ],
  },
  {
    label: 'Network',
    icon: Network,
    monitors: [
      { name: 'Ping', icon: Radio },
      { name: 'DNS', icon: Search },
      { name: 'gRPC', icon: Network },
      { name: 'TCP', icon: CircleDot },
      { name: 'SIP', icon: Send },
      { name: 'Domain expiry', icon: CalendarClock },
    ],
  },
  {
    label: 'Databases & brokers',
    icon: Database,
    monitors: [
      { name: 'PostgreSQL', icon: Database },
      { name: 'MySQL', icon: Database },
      { name: 'Redis', icon: Zap },
      { name: 'MongoDB', icon: Leaf },
      { name: 'RabbitMQ', icon: MessageSquare },
    ],
  },
  {
    label: 'Infrastructure & organization',
    icon: Boxes,
    monitors: [
      { name: 'Host agent', icon: Server },
      { name: 'Push heartbeat', icon: Webhook },
      { name: 'Prometheus query', icon: Activity },
      { name: 'Group roll-up', icon: Workflow },
    ],
  },
];

export const MONITOR_TYPE_COUNT = monitorGroups.flatMap((group) => group.monitors).length;
