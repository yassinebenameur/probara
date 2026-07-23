import type { DocNavGroup } from './types';

export const DOC_NAVIGATION: DocNavGroup[] = [
  {
    title: 'Start here',
    items: [
      {
        slug: 'getting-started',
        title: 'Getting started',
        description: 'Run Probara locally and create your first monitor.',
      },
      {
        slug: 'architecture',
        title: 'Architecture & concepts',
        description: 'Services, data flow, tenancy, and the monitor lifecycle.',
      },
    ],
  },
  {
    title: 'Use Probara',
    items: [
      {
        slug: 'monitors',
        title: 'Monitor reference',
        description: 'Every monitor type, shared field, and check behavior.',
      },
      {
        slug: 'locations',
        title: 'Locations & mesh',
        description: 'Private workers, quorum, ingest, and connectivity mesh.',
      },
      {
        slug: 'alerting',
        title: 'Alerting & incidents',
        description: 'Sensitivity, routing, lifecycle, maintenance, and AI RCA.',
      },
      {
        slug: 'status-pages',
        title: 'Status pages',
        description: 'Sections, incidents, themes, templates, live updates.',
      },
      {
        slug: 'dependencies',
        title: 'Dependencies & analytics',
        description: 'Service graph, correlations, dashboard, and rollups.',
      },
      {
        slug: 'agents',
        title: 'Host agents & push',
        description: 'Host telemetry, installers, heartbeats, and custom metrics.',
      },
      {
        slug: 'administration',
        title: 'Administration',
        description: 'Tenants, users, RBAC, API keys, SSO, audit, and imports.',
      },
    ],
  },
  {
    title: 'Build & operate',
    items: [
      {
        slug: 'api',
        title: 'API reference',
        description: 'Authentication, conventions, endpoint families, and examples.',
      },
      {
        slug: 'configuration',
        title: 'Configuration variables',
        description: 'Every service environment variable, default, and constraint.',
      },
      {
        slug: 'deployment',
        title: 'Deployment',
        description: 'Docker Compose, Helm, external services, and scaling.',
      },
      {
        slug: 'operations',
        title: 'Operations',
        description: 'Health, metrics, retention, backup, recovery, and testing.',
      },
      {
        slug: 'security',
        title: 'Security',
        description: 'Network policy, secrets, credentials, tenancy, and hardening.',
      },
    ],
  },
];

export const FLAT_DOC_NAVIGATION = DOC_NAVIGATION.flatMap((group) =>
  group.items.map((item) => ({ ...item, group: group.title })),
);
