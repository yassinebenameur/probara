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
  return `${API_PROXY_TARGET}/static/agent/${targetPath}${incoming.search || ''}`;
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
    if (key.toLowerCase() !== 'content-length') {
      headers.append(key, value);
    }
  });
  return headers;
}

async function proxyAgentStatic(req: Request, ctx: RouteContext): Promise<Response> {
  const { path } = await ctx.params;
  const targetUrl = buildTargetUrl(req.url, path);

  try {
    const upstream = await fetch(targetUrl, {
      method: req.method,
      headers: copyHeaders(req.headers),
      redirect: 'manual',
      cache: 'no-store',
    });

    return new Response(req.method === 'HEAD' ? null : upstream.body, {
      status: upstream.status,
      statusText: upstream.statusText,
      headers: copyResponseHeaders(upstream),
    });
  } catch {
    return Response.json(
      { error: 'bad_gateway', message: 'Failed to reach API upstream' },
      { status: 502 },
    );
  }
}

export function GET(req: Request, ctx: RouteContext): Promise<Response> {
  return proxyAgentStatic(req, ctx);
}

export function HEAD(req: Request, ctx: RouteContext): Promise<Response> {
  return proxyAgentStatic(req, ctx);
}
