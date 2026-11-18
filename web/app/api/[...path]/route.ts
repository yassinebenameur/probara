import { NextRequest } from 'next/server';
import {
  badGateway,
  buildTargetUrl,
  copyHeaders,
  copyResponseHeaders,
  type ProxyRouteContext as RouteContext,
  vouchForSameOriginWrite,
} from '@/lib/api-proxy';

export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';

async function proxyRequest(req: NextRequest, ctx: RouteContext): Promise<Response> {
  const { path } = await ctx.params;
  const targetUrl = buildTargetUrl(req.url, 'api', path);
  const headers = copyHeaders(req.headers);
  // A write from the UI's own pages: let the API know (lib/api-proxy.ts).
  vouchForSameOriginWrite(req.method, req.headers, headers, req.url);
  // The API builds absolute URLs (MCP OAuth metadata and redirects) from
  // the origin the client used; the Host header is dropped above, so pass it
  // on as the forwarded host when no outer proxy already did.
  const incoming = new URL(req.url);
  if (!headers.has('x-forwarded-host')) headers.set('x-forwarded-host', incoming.host);
  if (!headers.has('x-forwarded-proto')) headers.set('x-forwarded-proto', incoming.protocol.replace(':', ''));

  const init: RequestInit & { duplex?: 'half' } = {
    method: req.method,
    headers,
    redirect: 'manual',
    cache: 'no-store',
  };

  if (req.method !== 'GET' && req.method !== 'HEAD') {
    const body = await req.arrayBuffer();
    if (body.byteLength > 0) {
      init.body = body;
    }
  }

  try {
    const upstream = await fetch(targetUrl, init);
    const responseHeaders = copyResponseHeaders(upstream);
    return new Response(upstream.body, {
      status: upstream.status,
      statusText: upstream.statusText,
      headers: responseHeaders,
    });
  } catch {
    return badGateway();
  }
}

export function GET(req: NextRequest, ctx: RouteContext): Promise<Response> {
  return proxyRequest(req, ctx);
}

export function POST(req: NextRequest, ctx: RouteContext): Promise<Response> {
  return proxyRequest(req, ctx);
}

export function PUT(req: NextRequest, ctx: RouteContext): Promise<Response> {
  return proxyRequest(req, ctx);
}

export function PATCH(req: NextRequest, ctx: RouteContext): Promise<Response> {
  return proxyRequest(req, ctx);
}

export function DELETE(req: NextRequest, ctx: RouteContext): Promise<Response> {
  return proxyRequest(req, ctx);
}

export function OPTIONS(req: NextRequest, ctx: RouteContext): Promise<Response> {
  return proxyRequest(req, ctx);
}
