import { redirect } from 'next/navigation';

// Alert channels are listed on the Channels tab of Settings; the
// new/edit/catalog routes under /alert-channels stay where they are.
export default function AlertChannelsPage() {
  redirect('/settings?tab=channels');
}
