import { parseExportCount } from '../ai-labels';
import type {
  AILabelExportFilter,
  AILabelExportResult,
  AISettings,
  AISettingsUpdate,
  AITestRequest,
  AITestResult,
} from '../types';
import { apiDownload, apiRequest } from './core';

// Downloads the workspace's labels as JSONL training rows (tenant admins only).
// rows/skipped come from X-Probara-Export-Rows/-Skipped (null when absent):
// an empty export is a 200 with a 0-byte body, so the counts are the only
// way the UI can tell "nothing to export" from a real file.
export async function exportAILabels(filter: AILabelExportFilter = {}): Promise<AILabelExportResult> {
  const { blob, filename, headers } = await apiDownload('/v1/ai-labels/export', 'probara-ai-labels.jsonl', { method: 'POST', body: filter });
  return {
    blob,
    filename,
    rows: parseExportCount(headers.get('x-probara-export-rows')),
    skipped: parseExportCount(headers.get('x-probara-export-skipped')),
  };
}

// --- AI settings (workspace local-AI switch + automatic triage) ---

export async function getAISettings(): Promise<AISettings> {
  return apiRequest<AISettings>('GET', '/v1/ai-settings');
}

export async function updateAISettings(data: AISettingsUpdate): Promise<AISettings> {
  return apiRequest<AISettings>('PUT', '/v1/ai-settings', data);
}

export async function testAISettings(data: AITestRequest = {}): Promise<AITestResult> {
  return apiRequest<AITestResult>('POST', '/v1/ai-settings/test', data);
}
