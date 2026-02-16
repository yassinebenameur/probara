'use client';

import { useState, useEffect } from 'react';
import { StatusPage, CreateStatusPageRequest, UpdateStatusPageRequest, Monitor } from '@/lib/types';
import { getMonitors } from '@/lib/api';

interface StatusPageFormProps {
  statusPage?: StatusPage;
  onSubmit: (data: CreateStatusPageRequest | UpdateStatusPageRequest) => Promise<void>;
  onCancel?: () => void;
  loading?: boolean;
}

export default function StatusPageForm({
  statusPage,
  onSubmit,
  onCancel,
  loading = false,
}: StatusPageFormProps) {
  const [monitors, setMonitors] = useState<Monitor[]>([]);
  const [formData, setFormData] = useState({
    slug: statusPage?.slug || '',
    title: statusPage?.title || '',
    description: statusPage?.description || '',
    logo_url: statusPage?.logo_url || '',
    primary_color: statusPage?.primary_color || '#22d3ee',
    secondary_color: statusPage?.secondary_color || '#64748b',
    monitor_ids: statusPage?.monitor_ids || [],
  });

  const [errors, setErrors] = useState<Record<string, string>>({});

  useEffect(() => {
    loadMonitors();
  }, []);

  const loadMonitors = async () => {
    try {
      const response = await getMonitors({ page_size: 100 });
      setMonitors(response.items || []);
    } catch (error) {
      console.error('Failed to load monitors:', error);
    }
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setErrors({});

    const newErrors: Record<string, string> = {};
    if (!formData.slug.trim()) {
      newErrors.slug = 'Slug is required';
    } else if (!/^[a-z0-9-]+$/.test(formData.slug)) {
      newErrors.slug = 'Only lowercase letters, numbers, and hyphens allowed';
    }
    if (!formData.title.trim()) {
      newErrors.title = 'Title is required';
    }

    if (Object.keys(newErrors).length > 0) {
      setErrors(newErrors);
      return;
    }

    const requestData: CreateStatusPageRequest | UpdateStatusPageRequest = {
      slug: formData.slug.trim(),
      title: formData.title.trim(),
    };

    // Always include these fields - empty string clears the value
    requestData.description = formData.description.trim();
    requestData.logo_url = formData.logo_url.trim();
    requestData.primary_color = formData.primary_color.trim() || '#22d3ee';
    requestData.secondary_color = formData.secondary_color.trim() || '#64748b';
    requestData.monitor_ids = formData.monitor_ids;

    await onSubmit(requestData);
  };

  const toggleMonitor = (monitorId: string) => {
    setFormData({
      ...formData,
      monitor_ids: formData.monitor_ids.includes(monitorId)
        ? formData.monitor_ids.filter((id) => id !== monitorId)
        : [...formData.monitor_ids, monitorId],
    });
  };

  return (
    <form onSubmit={handleSubmit} className="space-y-6">
      {/* Basic Info */}
      <div>
        <h3 className="text-sm font-medium text-white mb-4">Basic Information</h3>
        <div className="space-y-4">
          <div>
            <label className="block text-xs font-medium text-slate-400 mb-1.5">URL Slug</label>
            <input
              type="text"
              value={formData.slug}
              onChange={(e) => setFormData({ ...formData, slug: e.target.value.toLowerCase() })}
              placeholder="my-status-page"
              className="input"
            />
            {errors.slug && <p className="mt-1 text-xs text-rose-400">{errors.slug}</p>}
            <p className="mt-1 text-xs text-slate-500">Lowercase letters, numbers, and hyphens only</p>
          </div>

          <div>
            <label className="block text-xs font-medium text-slate-400 mb-1.5">Title</label>
            <input
              type="text"
              value={formData.title}
              onChange={(e) => setFormData({ ...formData, title: e.target.value })}
              placeholder="My Service Status"
              className="input"
            />
            {errors.title && <p className="mt-1 text-xs text-rose-400">{errors.title}</p>}
          </div>

          <div>
            <label className="block text-xs font-medium text-slate-400 mb-1.5">Description (optional)</label>
            <textarea
              value={formData.description}
              onChange={(e) => setFormData({ ...formData, description: e.target.value })}
              placeholder="Describe your service status page..."
              rows={3}
              className="input resize-none min-h-[96px]"
            />
          </div>
        </div>
      </div>

      {/* Branding */}
      <div>
        <h3 className="text-sm font-medium text-white mb-4">Branding</h3>
        <div className="space-y-4">
          <div>
            <label className="block text-xs font-medium text-slate-400 mb-1.5">Logo URL (optional)</label>
            <input
              type="url"
              value={formData.logo_url}
              onChange={(e) => setFormData({ ...formData, logo_url: e.target.value })}
              placeholder="https://example.com/logo.png"
              className="input"
            />
          </div>

          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="block text-xs font-medium text-slate-400 mb-1.5">Primary Color</label>
              <div className="flex gap-2">
                <input
                  type="text"
                  value={formData.primary_color}
                  onChange={(e) => setFormData({ ...formData, primary_color: e.target.value })}
                  className="input font-mono"
                />
                <input
                  type="color"
                  value={formData.primary_color}
                  onChange={(e) => setFormData({ ...formData, primary_color: e.target.value })}
                  className="h-9 w-12 rounded-lg border border-white/[0.08] bg-transparent cursor-pointer"
                />
              </div>
            </div>
            <div>
              <label className="block text-xs font-medium text-slate-400 mb-1.5">Secondary Color</label>
              <div className="flex gap-2">
                <input
                  type="text"
                  value={formData.secondary_color}
                  onChange={(e) => setFormData({ ...formData, secondary_color: e.target.value })}
                  className="input font-mono"
                />
                <input
                  type="color"
                  value={formData.secondary_color}
                  onChange={(e) => setFormData({ ...formData, secondary_color: e.target.value })}
                  className="h-9 w-12 rounded-lg border border-white/[0.08] bg-transparent cursor-pointer"
                />
              </div>
            </div>
          </div>

          {/* Preview */}
          <div className="rounded-lg border border-white/[0.06] bg-[#0a0a0f] p-4">
            <p className="text-xs text-slate-500 mb-3">Header Preview</p>
            <div className="flex items-center gap-3">
              {formData.logo_url ? (
                <img 
                  src={formData.logo_url} 
                  alt="Logo" 
                  className="h-10 w-10 rounded-xl object-cover"
                  onError={(e) => {
                    (e.target as HTMLImageElement).style.display = 'none';
                  }}
                />
              ) : (
                <div 
                  className="h-10 w-10 rounded-xl flex items-center justify-center flex-shrink-0"
                  style={{ 
                    background: `linear-gradient(135deg, ${formData.primary_color || '#6366f1'} 0%, #06b6d4 100%)`,
                    boxShadow: '0 0 20px rgba(99, 102, 241, 0.3)'
                  }}
                >
                  <div className="w-5 h-5 rounded-full bg-[#0a0a0f] border-2 border-white/20" />
                </div>
              )}
              <div className="min-w-0">
                <div className="text-sm font-semibold text-white truncate">
                  {formData.title || 'Status Page Title'}
                </div>
                <div className="text-xs text-slate-500 truncate">
                  {formData.description || 'System Status'}
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>

      {/* Monitor Selection */}
      <div>
        <div className="flex items-center justify-between mb-4">
          <h3 className="text-sm font-medium text-white">Monitors</h3>
          <div className="flex items-center gap-3">
            <span className="text-xs text-cyan-400">{formData.monitor_ids.length} selected</span>
            {monitors.length > 0 && (
              <div className="flex items-center gap-2">
                <button
                  type="button"
                  onClick={() => setFormData({ ...formData, monitor_ids: monitors.map((m) => m.id) })}
                  className="btn btn-outline btn-xs"
                >
                  Select All
                </button>
                <span className="text-slate-600">|</span>
                <button
                  type="button"
                  onClick={() => setFormData({ ...formData, monitor_ids: [] })}
                  className="btn btn-outline btn-xs"
                >
                  Deselect All
                </button>
              </div>
            )}
          </div>
        </div>

        <div className="rounded-lg border border-white/[0.08] bg-slate-800/30 max-h-64 overflow-y-auto">
          {monitors.length === 0 ? (
            <p className="p-6 text-center text-sm text-slate-500">No monitors available</p>
          ) : (
            <div className="p-2 space-y-1">
              {monitors.map((monitor) => (
                <label
                  key={monitor.id}
                  className={`flex cursor-pointer items-center gap-3 rounded-lg px-3 py-2.5 transition-colors ${
                    formData.monitor_ids.includes(monitor.id)
                      ? 'bg-cyan-500/10 border border-cyan-500/30'
                      : 'hover:bg-white/[0.03] border border-transparent'
                  }`}
                  onMouseDown={() => {
                    // Prevent browser auto-scroll when focusing hidden checkbox
                    const scrollBefore = window.scrollY;
                    const scrollHandler = () => {
                      window.scrollTo({ top: scrollBefore, behavior: 'instant' });
                    };
                    window.addEventListener('scroll', scrollHandler, { once: true });
                    setTimeout(() => window.removeEventListener('scroll', scrollHandler), 200);
                  }}
                >
                  <input
                    type="checkbox"
                    checked={formData.monitor_ids.includes(monitor.id)}
                    onChange={() => toggleMonitor(monitor.id)}
                    className="sr-only"
                    tabIndex={-1}
                  />
                  <div className={`flex h-4 w-4 items-center justify-center rounded border transition-colors ${
                    formData.monitor_ids.includes(monitor.id)
                      ? 'border-cyan-500 bg-cyan-500'
                      : 'border-slate-600'
                  }`}>
                    {formData.monitor_ids.includes(monitor.id) && (
                      <svg className="h-3 w-3 text-white" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={3} d="M5 13l4 4L19 7" />
                      </svg>
                    )}
                  </div>
                  <div className="flex-1 min-w-0">
                    <p className="text-sm font-medium text-white truncate">{monitor.name}</p>
                    <p className="text-xs text-slate-500 truncate">
                      {monitor.type.toUpperCase()}
                      {monitor.url && ` · ${monitor.url}`}
                    </p>
                  </div>
                  <span className={`text-xs ${monitor.enabled ? 'text-emerald-400' : 'text-slate-500'}`}>
                    {monitor.enabled ? 'Active' : 'Paused'}
                  </span>
                </label>
              ))}
            </div>
          )}
        </div>
      </div>

      {/* Actions */}
      <div className="flex items-center justify-end gap-3 pt-4 border-t border-white/[0.06]">
        {onCancel && (
          <button
            type="button"
            onClick={onCancel}
            disabled={loading}
            className="btn btn-secondary btn-sm disabled:opacity-50"
          >
            Cancel
          </button>
        )}
        <button
          type="submit"
          disabled={loading}
          className="btn btn-primary btn-sm disabled:opacity-50"
        >
          {loading ? 'Saving...' : statusPage ? 'Save Changes' : 'Create Status Page'}
        </button>
      </div>
    </form>
  );
}
