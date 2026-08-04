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
reverse. Emits nothing when smtp.host is unset.
*/}}
{{- define "monitoring-platform.smtpEnv" -}}
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
