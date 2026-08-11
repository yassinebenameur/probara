{{/*
Expand the name of the chart.
*/}}
{{- define "monitoring-platform.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "monitoring-platform.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "monitoring-platform.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "monitoring-platform.labels" -}}
helm.sh/chart: {{ include "monitoring-platform.chart" . }}
{{ include "monitoring-platform.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "monitoring-platform.selectorLabels" -}}
app.kubernetes.io/name: {{ include "monitoring-platform.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "monitoring-platform.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "monitoring-platform.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Name of the Secret the chart renders itself.
*/}}
{{- define "monitoring-platform.secretName" -}}
{{- printf "%s-secret" (include "monitoring-platform.fullname" .) }}
{{- end }}

{{/*
Render one env var backed by a Kubernetes Secret, resolving the source in a
fixed order so that any credential can come from an operator-supplied Secret
(External Secrets Operator, Vault, sealed-secrets, SOPS — they all land a plain
Secret in the namespace):

  1. the field's own existingSecret        -> that Secret, its existingSecretKey
  2. chart-wide secrets.existingSecret     -> that Secret, the canonical key
  3. neither                               -> the chart-managed <fullname>-secret

The key defaults to the canonical name in every mode, so a single chart-wide
Secret carrying postgres_url / nats_url / admin_jwt_secret / probara_secrets_key
/ oidc_client_secret / nats_platform_password /
nats_location_auth_issuer_seed satisfies the whole install.

Args (dict): ctx (root context), name (env var), key (canonical key),
secret (per-field existingSecret), secretKey (per-field existingSecretKey).
*/}}
{{- define "monitoring-platform.secretEnv" -}}
{{- $external := default .ctx.Values.secrets.existingSecret .secret -}}
- name: {{ .name }}
  valueFrom:
    secretKeyRef:
      name: {{ $external | default (include "monitoring-platform.secretName" .ctx) | quote }}
      key: {{ ternary (default .key .secretKey) .key (not (empty $external)) | quote }}
{{- end }}

{{/*
True when a secret field is satisfied by EITHER a literal value or any existing
Secret reference. Guards must test this, never the literal alone: an
external-secret install leaves the literal empty, and a guard keyed on the
literal would silently drop the env var (for PROBARA_SECRETS_KEY that means
running with at-rest encryption disabled).

Args (dict): ctx, value (the literal), secret (per-field existingSecret).
*/}}
{{- define "monitoring-platform.hasSecret" -}}
{{- if or .value .secret .ctx.Values.secrets.existingSecret -}}true{{- end -}}
{{- end }}

{{/*
True when a secret field is still chart-managed, i.e. its value belongs in the
Secret this chart renders. Inverse of "some existing Secret supplies it".

Args (dict): ctx, secret (per-field existingSecret).
*/}}
{{- define "monitoring-platform.chartManagedSecret" -}}
{{- if and (not .secret) (not .ctx.Values.secrets.existingSecret) -}}true{{- end -}}
{{- end }}

{{/*
PostgreSQL connection URL
*/}}
{{- define "monitoring-platform.postgresUrl" -}}
{{- if .Values.postgresql.enabled }}
{{- printf "postgres://%s:%s@%s-postgresql:5432/%s?sslmode=disable" .Values.postgresql.auth.username .Values.postgresql.auth.password (include "monitoring-platform.fullname" .) .Values.postgresql.auth.database }}
{{- else }}
{{- .Values.postgresql.externalUrl }}
{{- end }}
{{- end }}{{/*
NATS connection URL
*/}}
{{- define "monitoring-platform.natsUrl" -}}
{{- if .Values.nats.enabled }}
{{- if .Values.nats.auth.enabled }}
{{- printf "nats://%s:%s@%s-nats:4222" (.Values.nats.auth.platformUser | urlquery) (.Values.nats.auth.platformPassword | urlquery) (include "monitoring-platform.fullname" .) }}
{{- else }}
{{- printf "nats://%s-nats:4222" (include "monitoring-platform.fullname" .) }}
{{- end }}
{{- else }}
{{- .Values.nats.externalUrl }}
{{- end }}
{{- end }}{{/*
PostgreSQL host
*/}}
{{- define "monitoring-platform.postgresHost" -}}
{{- printf "%s-postgresql" (include "monitoring-platform.fullname" .) }}
{{- end }}{{/*
NATS host
*/}}
{{- define "monitoring-platform.natsHost" -}}
{{- printf "%s-nats" (include "monitoring-platform.fullname" .) }}
{{- end }}{{/*
SMTP env block for the builtin email notification plugin. Rendered into every
workload that can invoke email Send: alerter (alert delivery), worker
(async notifications consumer), and api (alert-channel test endpoint). Wiring
only some of them produces channels that deliver but cannot be tested, or the
reverse. APP_BASE_URL rides along because the same three workloads render the
alert email and its "open the monitor" link; it is emitted independently of
smtp.host so it survives an SMTP-less install. Emits nothing when neither
smtp.host nor appBaseURL is set.
*/}}
{{- define "monitoring-platform.smtpEnv" -}}
{{- with .Values.appBaseURL }}
- name: APP_BASE_URL
  value: {{ . | quote }}
{{- end }}
{{- with .Values.smtp }}
{{- if .host }}
- name: SMTP_HOST
  value: {{ .host | quote }}
- name: SMTP_PORT
  value: {{ .port | quote }}
- name: SMTP_USE_TLS
  value: {{ .useTLS | quote }}
{{- with .from }}
- name: SMTP_FROM
  value: {{ . | quote }}
{{- end }}
{{- with .fromName }}
- name: SMTP_FROM_NAME
  value: {{ . | quote }}
{{- end }}
{{- with .username }}
- name: SMTP_USERNAME
  value: {{ . | quote }}
{{- end }}
{{- if .existingSecret }}
- name: SMTP_PASSWORD
  valueFrom:
    secretKeyRef:
      name: {{ .existingSecret | quote }}
      key: {{ .existingSecretKey | default "password" | quote }}
{{- else if .password }}
- name: SMTP_PASSWORD
  value: {{ .password | quote }}
{{- end }}
{{- end }}
{{- end }}
{{- end }}
