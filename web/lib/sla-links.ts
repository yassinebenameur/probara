// The deep link an emailed SLA report carries: `/slas?sla=<id>&tenant=<id>`
// (reportschedules.SLAReportPath in the API), the shape alert links use
// (lib/alert-links.ts). It points at the list route because a workspace
// switch on a `/slas/<id>` page falls back to the list (TenantScope): the
// list switches to the link's workspace first, then opens the SLA's issued
// reports. Parsed here and nowhere else.

export interface SlaLink {
  slaId: string;
  /** The SLA's workspace, when the link names one. */
  tenantId: string | null;
}

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/** Reads the link params; null unless `sla` is a UUID. A malformed `tenant` is ignored. */
export function parseSlaLink(params: { get(name: string): string | null }): SlaLink | null {
  const slaId = params.get('sla')?.trim() ?? '';
  if (!UUID.test(slaId)) return null;
  const tenant = params.get('tenant')?.trim() ?? '';
  return { slaId, tenantId: UUID.test(tenant) ? tenant : null };
}

/** Where the link lands once in the right workspace: the SLA's issued reports. */
export function slaIssuedReportsHref(slaId: string): string {
  return `/slas/${slaId}?tab=issued`;
}
