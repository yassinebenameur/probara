import {
  badGateway,
  buildTargetUrl,
  copyHeaders,
  copyResponseHeaders,
  type ProxyRouteContext as RouteContext,
} from '@/lib/api-proxy';

export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';

async function proxyCollectorStatic(req: Request, ctx: RouteContext): Promise<Response> {
  const { path } = await ctx.params;
  const targetUrl = buildTargetUrl(req.url, 'static/collector', path);

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
    return badGateway();
  }
}

export function GET(req: Request, ctx: RouteContext): Promise<Response> {
  return proxyCollectorStatic(req, ctx);
}

export function HEAD(req: Request, ctx: RouteContext): Promise<Response> {
  return proxyCollectorStatic(req, ctx);
}
