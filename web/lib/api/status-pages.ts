import type {
  CreateStatusPageRequest,
  StatusPage,
  StatusPageLibraryTemplate,
  StatusPageLibraryTemplateListResponse,
  StatusPageListResponse,
  StatusPageTemplateState,
  UpdateStatusPageRequest,
} from '../types';
import { apiRequest, apiRequestText, withQuery } from './core';

// Status Page API functions
export async function getStatusPages(params?: {
  page?: number;
  page_size?: number;
}): Promise<StatusPageListResponse> {
  return apiRequest<StatusPageListResponse>(
    'GET',
    withQuery('/v1/status-pages', {
      page: params?.page,
      page_size: params?.page_size,
    })
  );
}

export async function getStatusPage(id: string): Promise<StatusPage> {
  return apiRequest<StatusPage>('GET', `/v1/status-pages/${id}`);
}

export async function createStatusPage(
  data: CreateStatusPageRequest
): Promise<StatusPage> {
  return apiRequest<StatusPage>('POST', '/v1/status-pages', data);
}

export async function updateStatusPage(
  id: string,
  data: UpdateStatusPageRequest
): Promise<StatusPage> {
  return apiRequest<StatusPage>('PATCH', `/v1/status-pages/${id}`, data);
}

export async function deleteStatusPage(id: string): Promise<void> {
  return apiRequest<void>('DELETE', `/v1/status-pages/${id}`);
}

// Status page custom template API functions

export async function getStatusPageTemplate(id: string): Promise<StatusPageTemplateState> {
  return apiRequest<StatusPageTemplateState>('GET', `/v1/status-pages/${id}/template`);
}

export async function saveStatusPageTemplateDraft(
  id: string,
  source: string
): Promise<StatusPageTemplateState> {
  return apiRequest<StatusPageTemplateState>('PUT', `/v1/status-pages/${id}/template/draft`, {
    source,
  });
}

export async function discardStatusPageTemplateDraft(
  id: string
): Promise<StatusPageTemplateState> {
  return apiRequest<StatusPageTemplateState>('DELETE', `/v1/status-pages/${id}/template/draft`);
}

export async function publishStatusPageTemplate(id: string): Promise<StatusPageTemplateState> {
  return apiRequest<StatusPageTemplateState>('POST', `/v1/status-pages/${id}/template/publish`);
}

export async function revertStatusPageTemplate(
  id: string,
  version: number
): Promise<StatusPageTemplateState> {
  return apiRequest<StatusPageTemplateState>('POST', `/v1/status-pages/${id}/template/revert`, {
    version,
  });
}

export async function resetStatusPageTemplate(id: string): Promise<StatusPageTemplateState> {
  return apiRequest<StatusPageTemplateState>('DELETE', `/v1/status-pages/${id}/template`);
}

export async function getStatusPageDefaultTemplateSource(id: string): Promise<string> {
  return apiRequestText(`/v1/status-pages/${id}/template/default`);
}

export async function getStatusPageTemplateVersionSource(
  id: string,
  version: number
): Promise<string> {
  return apiRequestText(`/v1/status-pages/${id}/template/versions/${version}/source`);
}

// Status page template library (tenant-level reusable templates)

export async function listLibraryTemplates(): Promise<StatusPageLibraryTemplateListResponse> {
  return apiRequest<StatusPageLibraryTemplateListResponse>('GET', '/v1/status-page-templates');
}

export async function createLibraryTemplate(data: {
  name: string;
  description?: string;
  source: string;
}): Promise<StatusPageLibraryTemplate> {
  return apiRequest<StatusPageLibraryTemplate>('POST', '/v1/status-page-templates', data);
}

export async function updateLibraryTemplate(
  id: string,
  data: { name?: string; description?: string; source?: string }
): Promise<StatusPageLibraryTemplate> {
  return apiRequest<StatusPageLibraryTemplate>('PATCH', `/v1/status-page-templates/${id}`, data);
}

export async function deleteLibraryTemplate(id: string): Promise<void> {
  return apiRequest<void>('DELETE', `/v1/status-page-templates/${id}`);
}

export async function getLibraryTemplateSource(id: string): Promise<string> {
  return apiRequestText(`/v1/status-page-templates/${id}/source`);
}
