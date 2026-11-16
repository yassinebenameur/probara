import {
  IMPORT_SCHEMA_BETTER_STACK,
  IMPORT_SCHEMA_GATUS,
  IMPORT_SCHEMA_UPTIME_KUMA,
  IMPORT_SCHEMA_UPTIMEROBOT,
} from './types/import';

/**
 * Other monitoring tools the importer recognizes and translates on its own
 * (api/internal/services/import: one adapter per source). The upload step
 * lists them with how to get a file out of each; the mapping step explains
 * what was translated once preview reports the schema.
 */
export interface ImportSource {
  schema: string;
  label: string;
  /** One line: how to get an importable file out of the tool. */
  exportHint: string;
  /** What the translation covers and leaves out, shown once recognized. */
  previewNote?: { translated: string; notMigrated: string };
}

export const IMPORT_SOURCES: ImportSource[] = [
  {
    schema: IMPORT_SCHEMA_UPTIME_KUMA,
    label: 'Uptime Kuma',
    exportHint: 'Settings → Backup → Export on 1.x; scripts/kuma-export pulls the same file from 2.x.',
  },
  {
    schema: IMPORT_SCHEMA_BETTER_STACK,
    label: 'Better Stack',
    exportHint:
      'Save the Uptime API responses for /api/v2/monitors, /heartbeats, /monitor-groups and /heartbeat-groups, combined with jq -s.',
    previewNote: {
      translated:
        'Better Stack export detected. Monitor types, keywords, status codes, confirmation and recovery periods, SSL and domain expiration, heartbeats and groups were translated automatically, so the mappings below are already complete.',
      notMigrated:
        'Regions, notification and escalation settings, and maintenance windows are never migrated, and heartbeats get new push URLs. Check the per-monitor notes on the review step.',
    },
  },
  {
    schema: IMPORT_SCHEMA_UPTIMEROBOT,
    label: 'UptimeRobot',
    exportHint: 'Save the API v2 getMonitors response (with ssl=1 to keep SSL settings).',
    previewNote: {
      translated:
        'UptimeRobot export detected. HTTP, keyword, ping, port and heartbeat monitors, custom status codes, headers and basic auth were translated automatically, so the mappings below are already complete.',
      notMigrated:
        'Alert contacts and maintenance windows are never migrated, and heartbeats get new push URLs. Check the per-monitor notes on the review step.',
    },
  },
  {
    schema: IMPORT_SCHEMA_GATUS,
    label: 'Gatus',
    exportHint: 'Upload its config.yaml; for a configuration directory, merge the endpoint lists into one file first.',
    previewNote: {
      translated:
        'Gatus configuration detected. Endpoints, their conditions and groups were translated automatically, so the mappings below are already complete; each condition that could not be expressed is named in its monitor’s notes.',
      notMigrated:
        'Alerting providers and maintenance windows are never migrated, and environment variables are reported, not resolved: monitors that use them are created disabled.',
    },
  },
];

export function importSourceFor(schema?: string): ImportSource | undefined {
  return schema ? IMPORT_SOURCES.find((source) => source.schema === schema) : undefined;
}
