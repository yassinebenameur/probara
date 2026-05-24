import {
  AlertCircle,
  Bell,
  Mail,
  MessageCircle,
  MessageSquare,
  Slack,
  Webhook,
  type LucideIcon,
} from 'lucide-react';

// Plugin manifests reference icons by a string key (icon_key) so the backend
// doesn't depend on the frontend's icon library. The map below resolves that
// key to an actual lucide component on the client.
const iconMap: Record<string, LucideIcon> = {
  teams: MessageSquare,
  mail: Mail,
  email: Mail,
  slack: Slack,
  discord: MessageCircle,
  webhook: Webhook,
  bell: Bell,
};

export function iconFor(key: string | undefined): LucideIcon {
  if (!key) return AlertCircle;
  return iconMap[key] ?? AlertCircle;
}
