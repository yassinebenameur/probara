'use client';

import { useMemo, useState } from 'react';
import { Copy } from 'lucide-react';
import type { Monitor, StatusPage, StatusPageVisibility } from '@/lib/types';
import { isGatedVisibility } from '@/lib/status-page-access';
import {
  BADGE_UPTIME_RANGES,
  badgeHtml,
  badgeMarkdown,
  monitorBadgeUrl,
  statusPageShareLinks,
  type BadgeMetric,
  type BadgeUptimeRange,
} from '@/lib/status-page-share';
import { useClipboard } from '@/lib/useClipboard';
import FormField from '@/components/ui/FormField';
import Checkbox from '@/components/ui/Checkbox';
import CopyableTarget from '@/components/ui/CopyableTarget';
import IconButton from '@/components/ui/IconButton';
import Select from '@/components/ui/Select';
import SegmentedControl from '@/components/ui/SegmentedControl';
import { useToast } from '@/components/ui/ToastProvider';
import Callout from '@/components/ui/Callout';
import { SEO_DESCRIPTION_MAX, SEO_TITLE_MAX, type SeoSettings } from './useStatusPageFormState';

interface SeoSectionProps {
  pageTitle: string;
  value: SeoSettings;
  /** The visibility being edited: only a public page can be indexed. */
  visibility: StatusPageVisibility;
  onChange: (value: Partial<SeoSettings>) => void;
}

/** Title, description and indexing for search results and link previews. */
export function SeoSection({ pageTitle, value, visibility, onChange }: SeoSectionProps) {
  const name = pageTitle.trim() || 'My service';
  // Unlisted, password and private pages are always noindex (the status
  // service's pageIndexable); the stored choice is kept for a return to
  // public.
  const indexingForcedOff = visibility !== 'public';
  return (
    <section className="glass p-6" aria-labelledby="sp-seo-heading">
      <h3 id="sp-seo-heading" className="mb-1 text-title font-semibold text-white">SEO</h3>
      <p className="mb-5 text-xs text-slate-400">
        How the page appears in search results and when its link is shared.
      </p>
      <div className="space-y-4">
        <FormField label="Search title" description="Leave empty to use the page title.">
          <input
            type="text"
            value={value.title}
            onChange={(event) => onChange({ title: event.target.value })}
            maxLength={SEO_TITLE_MAX}
            placeholder={`${name} · Status`}
            className="input"
          />
        </FormField>
        <FormField
          label="Search description"
          description="Leave empty to use the page description, or a fixed sentence naming the page. It never states the current status: search engines keep it for days."
        >
          <textarea
            value={value.description}
            onChange={(event) => onChange({ description: event.target.value })}
            maxLength={SEO_DESCRIPTION_MAX}
            rows={2}
            placeholder={`Live status, incident history and scheduled maintenance for ${name}.`}
            className="input min-h-[72px] resize-none"
          />
        </FormField>
        <div className="glass-well p-4">
          <Checkbox
            checked={value.allowIndexing && !indexingForcedOff}
            disabled={indexingForcedOff}
            onChange={(checked) => onChange({ allowIndexing: checked })}
            className="!py-0"
            label={<span className="font-medium text-white">Allow search engines to index this page</span>}
            description={
              indexingForcedOff
                ? 'Off while the page is not public: unlisted, password and private pages are never indexed.'
                : 'Off asks crawlers not to list the page (robots noindex). The page stays public to anyone with the link.'
            }
          />
        </div>
      </div>
    </section>
  );
}

interface ShareSectionProps {
  statusPage: StatusPage;
  publicUrl: string;
  monitorsById: Map<string, Monitor>;
}

type BadgeTarget = 'page' | string;

/**
 * The page's public feed, summary.json and badge snippets. Built from the
 * saved page: a badge exists only for a component the published page shows.
 */
export function ShareSection({ statusPage, publicUrl, monitorsById }: ShareSectionProps) {
  const links = statusPageShareLinks(publicUrl);
  const { showToast } = useToast();
  const clipboard = useClipboard();
  const [target, setTarget] = useState<BadgeTarget>('page');
  const [metric, setMetric] = useState<BadgeMetric>('status');
  const [range, setRange] = useState<BadgeUptimeRange>('30d');
  const uptimeShown = statusPage.settings?.show_monitor_uptime !== false;
  const gated = isGatedVisibility(statusPage.visibility);

  const components = useMemo(() => {
    const seen = new Set<string>();
    const out: { id: string; name: string }[] = [];
    (statusPage.sections || []).forEach((section) =>
      (section.monitors || []).forEach((entry) => {
        if (seen.has(entry.monitor_id)) return;
        seen.add(entry.monitor_id);
        const name = entry.display_name || monitorsById.get(entry.monitor_id)?.name || entry.monitor_id;
        out.push({ id: entry.monitor_id, name });
      }),
    );
    return out;
  }, [statusPage.sections, monitorsById]);

  const component = components.find((c) => c.id === target);
  const effectiveMetric: BadgeMetric = component && uptimeShown ? metric : 'status';
  const imageUrl = component ? monitorBadgeUrl(links.page, component.id, effectiveMetric, range) : links.badge;
  const alt = component
    ? `${component.name} ${effectiveMetric === 'uptime' ? `uptime ${range}` : 'status'}`
    : `${statusPage.title} status`;
  const snippets = [
    { key: 'markdown', label: 'Markdown', code: badgeMarkdown(imageUrl, links.page, alt) },
    { key: 'html', label: 'HTML', code: badgeHtml(imageUrl, links.page, alt) },
  ];

  const copy = async (code: string, key: string) => {
    const ok = await clipboard.copy(code, key);
    showToast(ok ? 'Copied' : 'Copy failed', ok ? 'success' : 'error');
  };

  return (
    <section className="glass p-6" aria-labelledby="sp-share-heading">
      <h3 id="sp-share-heading" className="mb-1 text-title font-semibold text-white">Share</h3>
      {gated ? (
        <Callout tone="warning" className="mb-5">
          This page is password-protected or private, so its feed, summary and badges work only for visitors who
          have unlocked it in the same browser. Embedded on another site or polled by a feed reader they are
          refused.
        </Callout>
      ) : (
        <p className="mb-5 text-xs text-slate-400">
          {statusPage.visibility === 'unlisted'
            ? 'Readable by anyone with these links, like the page itself; never indexed.'
            : 'Public and readable from any site, like the page itself.'}{' '}
          Uptime appears only where the page shows it.
        </p>
      )}
      <div className="space-y-5">
        <dl className="grid gap-3 sm:grid-cols-[8rem_minmax(0,1fr)] sm:items-center">
          <dt className="text-ui font-medium text-slate-300">Atom feed</dt>
          <dd className="min-w-0">
            <CopyableTarget value={links.feed} label="feed URL" />
          </dd>
          <dt className="text-ui font-medium text-slate-300">Summary JSON</dt>
          <dd className="min-w-0">
            <CopyableTarget value={links.summary} label="summary URL" />
          </dd>
        </dl>

        <div className="space-y-3">
          <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-end">
            <Select label="Badge" value={target} onChange={(event) => setTarget(event.target.value)}>
              <option value="page">Overall page status</option>
              {components.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                </option>
              ))}
            </Select>
            {component && uptimeShown && (
              <SegmentedControl<BadgeMetric>
                label="Badge metric"
                value={metric}
                onChange={setMetric}
                options={[
                  { value: 'status', label: 'Status' },
                  { value: 'uptime', label: 'Uptime' },
                ]}
              />
            )}
          </div>
          {component && uptimeShown && metric === 'uptime' && (
            <SegmentedControl<BadgeUptimeRange>
              label="Uptime range"
              value={range}
              onChange={setRange}
              options={BADGE_UPTIME_RANGES.map((r) => ({ value: r, label: r }))}
            />
          )}
          {snippets.map((snippet) => (
            <div key={snippet.key}>
              <p className="mb-1.5 text-xs text-slate-500">{snippet.label}</p>
              <div className="flex gap-2">
                <pre className="glass-well min-w-0 flex-1 overflow-x-auto px-3 py-2 font-mono text-xs text-slate-200">
                  {snippet.code}
                </pre>
                <IconButton
                  icon={<Copy strokeWidth={1.75} />}
                  label={`Copy ${snippet.label} snippet`}
                  onClick={() => copy(snippet.code, snippet.key)}
                />
              </div>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}
