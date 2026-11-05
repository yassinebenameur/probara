'use client';

import { useEffect, useState } from 'react';
import { Slack } from 'lucide-react';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import { useSelectedTenantId } from '@/components/providers/TenantProvider';
import { useIsTenantAdmin } from '@/components/providers/CurrentUserProvider';

// Why "Add to Slack" sent the browser back (slack_error, set by the API's
// /integrations/slack/install and /callback).
const SLACK_ERRORS: Record<string, string> = {
  not_configured: 'Add to Slack is not set up on this installation: set SLACK_CLIENT_ID, SLACK_CLIENT_SECRET and PUBLIC_BASE_URL on the API, or fill in the fields below by hand.',
  denied: 'The Slack install was cancelled.',
  invalid_state: 'The Slack install expired or was started in another browser. Start it again.',
  forbidden: 'Only a workspace admin can add the Slack app.',
  exchange_failed: 'Slack did not complete the install. Try again.',
  no_channel: 'Slack did not say which channel to post to. Pick a channel when Slack asks.',
  internal: 'The Slack install failed. Try again.',
};

/**
 * "Add to Slack" for the slack_app channel type: the API's install route
 * runs Slack's OAuth consent and creates the channel. A browser navigation,
 * not a fetch: it ends on Slack's site and comes back through the callback.
 */
export default function SlackInstall() {
  const tenantId = useSelectedTenantId();
  const isAdmin = useIsTenantAdmin();
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    // Read once on mount; useSearchParams would need a Suspense boundary
    // wherever the channel form is embedded.
    const code = new URLSearchParams(window.location.search).get('slack_error');
    if (code) setError(SLACK_ERRORS[code] ?? SLACK_ERRORS.internal);
  }, []);

  const href = tenantId
    ? `/api/v1/integrations/slack/install?tenant_id=${encodeURIComponent(tenantId)}`
    : null;

  return (
    <div className="space-y-3">
      {error && <Callout tone="warning">{error}</Callout>}
      <Callout
        tone="info"
        title="Add to Slack"
        action={
          isAdmin && href ? (
            <Button asChild variant="accent" size="xs" icon={<Slack />}>
              <a href={href}>Add to Slack</a>
            </Button>
          ) : undefined
        }
      >
        {isAdmin
          ? 'Install the Slack app in your workspace and pick a channel; the channel is created for you. Or fill in a bot token, workspace ID and channel ID below.'
          : 'A workspace admin can install the Slack app with Add to Slack. You can still fill in a bot token, workspace ID and channel ID below.'}
      </Callout>
    </div>
  );
}
