import type { Sla, SlaExportFormat, SlaInput, SlaReport, SlaReportQuery, SlaReportRef } from '../sla';
import { slaReportQueryString } from '../sla';
import { apiDownload, apiRequest, withQuery } from './core';

// SLAs and their reports (lib/sla.ts holds the types)
export async function getSlas(params?: { monitor_id?: string }): Promise<Sla[]> {
  const res = await apiRequest<{ items: Sla[] }>('GET', withQuery('/v1/slas', { monitor_id: params?.monitor_id }));
  return res?.items ?? [];
}

export function getSla(id: string) {
  return apiRequest<Sla>('GET', `/v1/slas/${id}`);
}

export function createSla(data: SlaInput) {
  return apiRequest<Sla>('POST', '/v1/slas', data);
}

export function updateSla(id: string, data: Partial<SlaInput>) {
  return apiRequest<Sla>('PATCH', `/v1/slas/${id}`, data);
}

export function deleteSla(id: string) {
  return apiRequest<void>('DELETE', `/v1/slas/${id}`);
}

export function getSlaReport(id: string, query: SlaReportQuery = {}) {
  return apiRequest<SlaReport>('GET', `/v1/slas/${id}/report${slaReportQueryString(query)}`);
}

export function downloadSlaReport(id: string, query: SlaReportQuery, format: SlaExportFormat) {
  return apiDownload(`/v1/slas/${id}/report${slaReportQueryString(query, format)}`, `sla-report.${format}`);
}

export async function getIssuedSlaReports(id: string): Promise<SlaReportRef[]> {
  const res = await apiRequest<{ items: SlaReportRef[] }>('GET', `/v1/slas/${id}/reports`);
  return res?.items ?? [];
}

export function issueSlaReport(id: string, period?: string) {
  return apiRequest<SlaReport>('POST', `/v1/slas/${id}/reports`, period ? { period } : {});
}

export function downloadIssuedSlaReport(reportId: string, format: SlaExportFormat) {
  return apiDownload(withQuery(`/v1/sla-reports/${reportId}`, { format }), `sla-report.${format}`);
}
