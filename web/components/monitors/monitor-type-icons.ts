import {
  Cable, Code, Cylinder, Database, Flame, Folder, Globe, Leaf, MessageSquare, MousePointer2,
  Network, Phone, PlugZap, Radio, Search, Server, Webhook, Zap,
  type LucideIcon,
} from 'lucide-react';
import type { MonitorType } from '@/lib/types';

// The one icon per monitor type, for the type picker (MONITOR_TYPE_META) and
// the dependency map. Typed against MonitorType, so adding a type without an
// icon does not compile.
export const MONITOR_TYPE_ICONS: Record<MonitorType, LucideIcon> = {
  http: Globe,
  websocket: Cable,
  synthetic_api: Code,
  synthetic_browser: MousePointer2,
  ping: Radio,
  dns: Search,
  grpc: Network,
  tcp: PlugZap,
  sip: Phone,
  postgres: Database,
  mysql: Cylinder,
  redis: Zap,
  mongodb: Leaf,
  rabbitmq: MessageSquare,
  prometheus: Flame,
  agent: Server,
  push: Webhook,
  group: Folder,
};
