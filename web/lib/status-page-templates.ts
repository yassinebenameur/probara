import type { StatusPageTemplateName } from './types';

/**
 * The built-in public page templates a status page can render with (the
 * `template` setting). Mirrors shared/statustemplate.Builtins: the API
 * refuses any other name. A published custom template still wins.
 */
export const PAGE_TEMPLATE_OPTIONS: readonly {
  value: StatusPageTemplateName;
  label: string;
  description: string;
}[] = [
  {
    value: 'default',
    label: 'Default',
    description: 'Status summary with page-wide uptime, search and filters, and a list of current incidents.',
  },
  {
    value: 'classic',
    label: 'Classic',
    description: 'The familiar layout: overall banner, services with history bars, and incidents by day.',
  },
];

/** The page's built-in template; anything unknown or unset is the default. */
export function pageTemplate(value: string | undefined): StatusPageTemplateName {
  return PAGE_TEMPLATE_OPTIONS.some((o) => o.value === value) ? (value as StatusPageTemplateName) : 'default';
}

/** The operator-facing name of a built-in template. */
export function pageTemplateLabel(value: string | undefined): string {
  const name = pageTemplate(value);
  return PAGE_TEMPLATE_OPTIONS.find((o) => o.value === name)?.label ?? 'Default';
}
