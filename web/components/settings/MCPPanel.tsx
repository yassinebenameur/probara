'use client';

import { useEffect, useState } from 'react';
import { Bot, Copy, Unplug } from 'lucide-react';
import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
import IconButton from '@/components/ui/IconButton';
import Pill from '@/components/ui/Pill';
import FilterChip from '@/components/ui/FilterChip';
import Callout from '@/components/ui/Callout';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import { useToast } from '@/components/ui/ToastProvider';
import { getMcpConnections, getMcpEndpointUrl, revokeMcpConnection } from '@/lib/api';
import { formatDateTime } from '@/lib/format';
import type { McpConnection } from '@/lib/types';

type Client = 'claude-code' | 'claude-ai' | 'cursor' | 'api-key';

const CLIENTS: Array<{ id: Client; label: string }> = [
  { id: 'claude-code', label: 'Claude Code' },
  { id: 'claude-ai', label: 'Claude.ai / Desktop' },
  { id: 'cursor', label: 'Cursor & others' },
  { id: 'api-key', label: 'With an API key' },
];

function snippet(client: Client, url: string): string {
  switch (client) {
    case 'claude-code':
      return `claude mcp add --transport http probara ${url}`;
    case 'claude-ai':
      return url;
    case 'cursor':
      return JSON.stringify({ mcpServers: { probara: { url } } }, null, 2);
    case 'api-key':
      return `claude mcp add --transport http probara ${url} \\\n  --header "Authorization: Bearer <API key>"`;
  }
}

function hint(client: Client): string {
  switch (client) {
    case 'claude-code':
      return 'Run it, then /mcp in Claude Code and choose Authenticate: your browser opens this workspace to approve the connection.';
    case 'claude-ai':
      return 'Settings → Connectors → Add custom connector, paste the URL, then Connect and approve in the browser.';
    case 'cursor':
      return 'Add to .cursor/mcp.json (or your client\'s MCP config). Clients that support MCP OAuth open the browser to approve.';
    case 'api-key':
      return 'For scripts and headless agents: a workspace API key (Access tab). Keys act in one workspace and never reach tenant-admin or superadmin tools.';
  }
}

/**
 * MCP: how to connect an AI agent to Probara, and the signed-in user's
 * own OAuth connections. Connections are per user, so every role can see
 * and revoke theirs; what an agent may do is the user's role narrowed by
 * the access chosen at consent.
 */
export function MCPPanel() {
  const { showToast } = useToast();
  const [client, setClient] = useState<Client>('claude-code');
  const [url, setUrl] = useState('');
  const [connections, setConnections] = useState<McpConnection[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [pending, setPending] = useState<McpConnection | null>(null);
  const [revoking, setRevoking] = useState(false);

  useEffect(() => {
    setUrl(getMcpEndpointUrl());
  }, []);

  const load = async () => {
    try {
      setLoading(true);
      setError('');
      const res = await getMcpConnections();
      setConnections(res.items || []);
    } catch (err: any) {
      setError(err.message || 'Failed to load connections');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    load();
  }, []);

  const copy = async (text: string) => {
    try {
      await navigator.clipboard.writeText(text);
      showToast('Copied', 'success');
    } catch {
      showToast('Copy failed', 'error');
    }
  };

  const revoke = async () => {
    if (!pending) return;
    setRevoking(true);
    try {
      await revokeMcpConnection(pending.id);
      showToast(`Disconnected ${pending.client_name}`, 'success');
      setPending(null);
      await load();
    } catch (err: any) {
      showToast(err.message || 'Failed to disconnect', 'error');
    } finally {
      setRevoking(false);
    }
  };

  const code = snippet(client, url);

  return (
    <>
      <Panel
        title="Connect an AI agent"
        subtitle="Probara speaks the Model Context Protocol: agents can read and operate everything you can, with your role and audit trail."
      >
        <div className="space-y-4">
          <div className="flex flex-wrap gap-1.5" role="group" aria-label="MCP client">
            {CLIENTS.map((c) => (
              <FilterChip key={c.id} selected={client === c.id} onClick={() => setClient(c.id)}>
                {c.label}
              </FilterChip>
            ))}
          </div>
          <div className="flex gap-2">
            <pre className="glass-well min-w-0 flex-1 overflow-x-auto px-3 py-2 font-mono text-xs text-slate-200">
              {code}
            </pre>
            <IconButton icon={<Copy strokeWidth={1.75} />} label="Copy" onClick={() => copy(code)} />
          </div>
          <p className="max-w-prose text-xs text-slate-400">{hint(client)}</p>
        </div>
      </Panel>

      <Panel title="Your connections" subtitle="Agents you authorized. Disconnecting revokes their tokens immediately.">
        {loading ? (
          <p className="text-sm text-slate-500">Loading connections…</p>
        ) : error ? (
          <Callout
            tone="danger"
            action={<Button variant="ghost" size="xs" onClick={load}>Retry</Button>}
          >
            {error}
          </Callout>
        ) : connections.length === 0 ? (
          <div className="flex flex-col items-center py-6 text-center">
            <Bot className="h-5 w-5 text-slate-500" strokeWidth={1.5} aria-hidden="true" />
            <p className="mt-3 text-sm font-medium text-white">No agents connected</p>
            <p className="mt-1 text-ui text-slate-400">Add Probara to an MCP client above to see it here.</p>
          </div>
        ) : (
          <ul className="-mx-5 -mb-5 divide-y divide-divider">
            {connections.map((c) => (
              <li
                key={c.id}
                className="flex flex-col gap-3 px-5 py-3.5 transition-colors hover:bg-well sm:flex-row sm:items-center sm:justify-between"
              >
                <div className="min-w-0">
                  <p className="text-sm font-medium text-white">{c.client_name}</p>
                  <p className="mt-0.5 flex flex-wrap gap-x-3 text-xs text-slate-500">
                    <span>{c.tenant_name}</span>
                    <span>Connected {formatDateTime(c.created_at)}</span>
                    {c.last_used_at && <span>Last used {formatDateTime(c.last_used_at)}</span>}
                  </p>
                </div>
                <div className="flex flex-shrink-0 items-center gap-2">
                  <Pill tone={c.scope === 'read' ? 'info' : 'neutral'} size="xs">
                    {c.scope === 'read' ? 'Read-only' : 'Read & write'}
                  </Pill>
                  <IconButton
                    icon={<Unplug strokeWidth={1.75} />}
                    label="Disconnect"
                    onClick={() => setPending(c)}
                    danger
                  />
                </div>
              </li>
            ))}
          </ul>
        )}
      </Panel>

      <ConfirmDialog
        open={pending !== null}
        title="Disconnect agent?"
        description={`${pending?.client_name ?? 'This agent'} loses access immediately. You can connect it again later.`}
        confirmLabel="Disconnect"
        tone="danger"
        loading={revoking}
        onConfirm={revoke}
        onCancel={() => setPending(null)}
      />
    </>
  );
}
