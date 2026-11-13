// MCP OAuth connection (an agent the signed-in user authorized).
export interface McpConnection {
  id: string;
  client_id: string;
  client_name: string;
  tenant_id: string;
  tenant_name: string;
  scope: 'read' | 'write';
  created_at: string;
  last_used_at?: string | null;
}

export interface McpConnectionListResponse {
  items: McpConnection[];
}
