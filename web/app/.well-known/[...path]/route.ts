import { NextRequest } from 'next/server';

// MCP clients discover the OAuth authorization server at root well-known
// paths (RFC 9728 / RFC 8414). Ingress sends everything outside /api to this
// app, so forward them to the API, which serves them. Forwarded headers keep
// the advertised URLs on this origin.
const API_PROXY_TARGET = process.env.API_PROXY_TARGET || 'http://localhost:8080';

export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';

type RouteContext = {
  params: Promise<{ path: string[] }>;
};

async function forward(req: NextRequest, ctx: RouteContext): Promise<Response> {
  const { path } = await ctx.params;
  const incoming = new URL(req.url);
  const target = `${API_PROXY_TARGET}/.well-known/${path.map(encodeURIComponent).join('/')}${incoming.search}`;
  try {
    const upstream = await fetch(target, {
      method: req.method,
      headers: {
        accept: 'application/json',
        'x-forwarded-host': req.headers.get('x-forwarded-host') || incoming.host,
        'x-forwarded-proto': req.headers.get('x-forwarded-proto') || incoming.protocol.replace(':', ''),
      },
      cache: 'no-store',
    });
    return new Response(upstream.body, {
      status: upstream.status,
      headers: {
        'content-type': upstream.headers.get('content-type') || 'application/json',
        'cache-control': 'no-store',
        'access-control-allow-origin': '*',
      },
    });
  } catch {
    return Response.json({ error: 'bad_gateway' }, { status: 502 });
  }
}

export function GET(req: NextRequest, ctx: RouteContext): Promise<Response> {
  return forward(req, ctx);
}
