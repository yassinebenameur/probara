import { NextRequest } from 'next/server';

const API_PROXY_TARGET = process.env.API_PROXY_TARGET || 'http://localhost:8080';
const HOP_BY_HOP_HEADERS = new Set([
  'connection',
  'keep-alive',
  'proxy-authenticate',
  'proxy-authorization',
  'te',
  'trailer',
  'transfer-encoding',
  'upgrade',
  'host',
  'content-length',
]);

export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';

type RouteContext = {
  params: Promise<{ path: string[] }>;
};

function buildTargetUrl(requestUrl: string, path: string[]): string {
  const incoming = new URL(requestUrl);
  const targetPath = path.join('/');
  const query = incoming.search || '';
  return `${API_PROXY_TARGET}/api/${targetPath}${query}`;
}

function copyHeaders(requestHeaders: Headers): Headers {
  const headers = new Headers();
  requestHeaders.forEach((value, key) => {
    if (!HOP_BY_HOP_HEADERS.has(key.toLowerCase())) {
      headers.set(key, value);
    }
  });
  return headers;
}

function copyResponseHeaders(upstream: Response): Headers {
  const headers = new Headers();
  upstream.headers.forEach((value, key) => {
    const lower = key.toLowerCase();
    if (lower === 'content-length' || lower === 'set-cookie') {
      return;
    }
    headers.append(key, value);
  });

  // Preserve each cookie header independently. Collapsing them into one
  // header can break parsing and cause immediate auth logout.
  const getSetCookie = (upstream.headers as unknown as { getSetCookie?: () => string[] }).getSetCookie;
  const setCookies = typeof getSetCookie === 'function' ? getSetCookie.call(upstream.headers) : [];
  for (const cookie of setCookies) {
    headers.append('set-cookie', cookie);
  }

  return headers;
}

async function proxyRequest(req: NextRequest, ctx: RouteContext): Promise<Response> {
  const { path } = await ctx.params;
  const targetUrl = buildTargetUrl(req.url, path);
  const headers = copyHeaders(req.headers);

  const init: RequestInit & { duplex?: 'half' } = {
    method: req.method,
    headers,
    redirect: 'manual',
    cache: 'no-store',
  };

  if (req.method !== 'GET' && req.method !== 'HEAD') {
    init.body = req.body;
    init.duplex = 'half';
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
    return Response.json(
      { error: 'bad_gateway', message: 'Failed to reach API upstream' },
      { status: 502 },
    );
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
