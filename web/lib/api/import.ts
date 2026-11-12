import { apiErrorFromBody } from '../errors';
import type { ImportExecuteRequest, ImportExecuteResponse, ImportPreviewResponse } from '../types';
import { apiFetch, apiRequest } from './core';

// Import API functions
export async function previewImport(file: File): Promise<ImportPreviewResponse> {
  const formData = new FormData();
  formData.append('file', file);

  const response = await apiFetch('/v1/monitors/import/preview', { method: 'POST', body: formData });

  // Try to parse as JSON, handle non-JSON responses gracefully
  let data;
  const contentType = response.headers.get('content-type');
  if (contentType && contentType.includes('application/json')) {
    try {
      data = await response.json();
    } catch {
      throw new Error('Invalid response from server');
    }
  } else {
    const text = await response.text();
    throw new Error(text || `Server error: ${response.status}`);
  }

  if (!response.ok) {
    throw apiErrorFromBody(data);
  }

  return data;
}

export async function executeImport(data: ImportExecuteRequest): Promise<ImportExecuteResponse> {
  return apiRequest<ImportExecuteResponse>('POST', '/v1/monitors/import', data);
}
