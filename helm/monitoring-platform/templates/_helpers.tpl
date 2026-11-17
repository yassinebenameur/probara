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
Tag for every Probara image: image.tag, or the chart's appVersion when it is
empty. The release (scripts/release/update-helm-chart.sh) sets appVersion to
the released version, and docker-build pushes that exact tag for every image,
the gated local-ai one included, while `latest` exists only for stable
releases (never on the prerelease line).
*/}}
{{- define "monitoring-platform.imageTag" -}}
{{- .Values.image.tag | default .Chart.AppVersion -}}
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
secret (per-field existingSecret), secretKey (per-field existingSecretKey),
optional (true only for a credential whose absence the consumer tolerates;
a missing key then leaves the variable unset instead of blocking the pod).
*/}}
{{- define "monitoring-platform.secretEnv" -}}
{{- $external := default .ctx.Values.secrets.existingSecret .secret -}}
- name: {{ .name }}
  valueFrom:
    secretKeyRef:
      name: {{ $external | default (include "monitoring-platform.secretName" .ctx) | quote }}
      key: {{ ternary (default .key .secretKey) .key (not (empty $external)) | quote }}
      {{- if .optional }}
      optional: true
      {{- end }}
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
ADMIN_COOKIE_SECURE for the API, or empty to leave the code default (false).
api.cookieSecure wins when set ("true"/"false"); left empty, the cookie is
Secure exactly when api.publicBaseURL is an https:// origin. A Secure cookie
is never sent over plain HTTP, so forcing it on for an HTTP-only install would
make login impossible; hence derived, not defaulted.
*/}}
{{- define "monitoring-platform.adminCookieSecure" -}}
{{- $explicit := .Values.api.cookieSecure -}}
{{- if kindIs "bool" $explicit -}}
{{- toString $explicit -}}
{{- else if $explicit -}}
{{- toString $explicit -}}
{{- else if hasPrefix "https://" (default "" .Values.api.publicBaseURL) -}}
true
{{- end -}}
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
Broker URL baked into private-location deploy snippets (PUBLIC_NATS_URL):
api.publicNatsURL, else wss://<host><path> of the NATS websocket ingress,
else empty (deploy snippets disabled).
*/}}
{{- define "monitoring-platform.publicNatsUrl" -}}
{{- if .Values.api.publicNatsURL }}
{{- .Values.api.publicNatsURL }}
{{- else if and .Values.nats.enabled .Values.nats.websocket.enabled .Values.nats.websocket.ingress.enabled }}
{{- $path := trimSuffix "/" (.Values.nats.websocket.ingress.path | default "/") }}
{{- printf "wss://%s%s" .Values.nats.websocket.ingress.host $path }}
{{- end }}
{{- end }}

{{/*
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

{{/*
URL the api and platform worker call for local AI: the bundled Service when
localAI.enabled, else localAI.externalUrl, else empty (local AI off).
*/}}
{{- define "monitoring-platform.localAIUrl" -}}
{{- if .Values.localAI.enabled -}}
{{- printf "http://%s-local-ai:8090" (include "monitoring-platform.fullname" .) -}}
{{- else -}}
{{- .Values.localAI.externalUrl -}}
{{- end -}}
{{- end }}

{{/*
Local AI client env for the api and the PLATFORM worker only (never location
workers, scheduler, alerter or status page). Without a URL it renders
LOCAL_AI_DISABLED=true and nothing else, which overrides every workspace
setting; with one, LOCAL_AI_TOKEN resolves through secretEnv like every other
credential (secrets.localAiToken / localAiTokenExistingSecret / chart-wide
secrets.existingSecret, canonical key local_ai_token). The token's presence is
enforced in secret.yaml where Helm can see it, not guarded here.

On the clients the reference is optional: an existing Secret that predates
local AI (chart-wide secrets.existingSecret without local_ai_token) must not
put the api and worker in CreateContainerConfigError on upgrade. Without the
token they start and report AI unavailable; only the local-ai pod, whose
reference stays required, waits for the key.
*/}}
{{- define "monitoring-platform.localAIEnv" -}}
{{- $url := include "monitoring-platform.localAIUrl" . -}}
- name: LOCAL_AI_DISABLED
  value: {{ empty $url | quote }}
{{- if $url }}
- name: LOCAL_AI_URL
  value: {{ $url | quote }}
- name: LOCAL_AI_TIMEOUT_SECONDS
  value: {{ .Values.localAI.timeoutSeconds | quote }}
{{ include "monitoring-platform.localAITokenEnv" (dict "ctx" . "optional" true) }}
{{- end }}
{{- end -}}

{{/*
Automatic alert triage kill switch (AI_TRIAGE_ENABLED), rendered into the
platform worker (relay + triage consumer), the alerter (suspected-cause
follow-ups) and the API (SLA reports' "Triage pending" bucket).
*/}}
{{- define "monitoring-platform.aiTriageEnv" -}}
- name: AI_TRIAGE_ENABLED
  value: {{ .Values.localAI.triageEnabled | quote }}
{{- end -}}

{{/*
The alerter never calls local AI, so it gets only the two kill switches: with
LOCAL_AI_DISABLED=true or AI_TRIAGE_ENABLED=false it sends no suspected-cause
follow-ups. LOCAL_AI_DISABLED follows the same rule as localAIEnv.
*/}}
{{- define "monitoring-platform.alerterAIEnv" -}}
- name: LOCAL_AI_DISABLED
  value: {{ empty (include "monitoring-platform.localAIUrl" .) | quote }}
{{ include "monitoring-platform.aiTriageEnv" . }}
{{- end -}}

{{/*
LOCAL_AI_TOKEN env var, shared by the clients (optional reference, see
localAIEnv) and the local AI pod (required). Args (dict): ctx, optional.
*/}}
{{- define "monitoring-platform.localAITokenEnv" -}}
{{- $s := .ctx.Values.secrets -}}
{{- include "monitoring-platform.secretEnv" (dict "ctx" .ctx "name" "LOCAL_AI_TOKEN" "key" "local_ai_token" "secret" $s.localAiTokenExistingSecret "secretKey" $s.localAiTokenExistingSecretKey "optional" .optional) }}
{{- end }}

{{/*
Value of local_ai_token in the chart-managed Secret: the literal when set,
otherwise derived from secrets.adminJwtSecret (see values.yaml for why it is
derived rather than generated). Only called when the token is chart-managed;
the fail cases for an underivable token live in secret.yaml.
*/}}
{{- define "monitoring-platform.localAIToken" -}}
{{- if .Values.secrets.localAiToken -}}
{{- .Values.secrets.localAiToken -}}
{{- else -}}
{{- sha256sum (printf "probara/local-ai-token/v1:%s" .Values.secrets.adminJwtSecret) -}}
{{- end -}}
{{- end }}

{{/*
CHECK_JOB_STREAM / CHECK_JOB_SUBJECT for every workload on the check-job queue:
the API (run-now publisher), the scheduler, the default worker fleet and each
in-cluster location worker. They must be identical everywhere — a publisher
and a consumer on different names drop every job without an error — so
scheduler.checkJobStream / checkJobSubject are the one source. The old
worker.checkJobStream / checkJobSubject keys are still accepted when they
equal the scheduler's; a different value fails the render instead of
silently splitting the queue.
*/}}
{{- define "monitoring-platform.checkJobEnv" -}}
- name: CHECK_JOB_STREAM
  value: {{ include "monitoring-platform.checkJobQueueName" (dict "ctx" . "field" "checkJobStream") | quote }}
- name: CHECK_JOB_SUBJECT
  value: {{ include "monitoring-platform.checkJobQueueName" (dict "ctx" . "field" "checkJobSubject") | quote }}
{{- end -}}

{{/*
One check-job queue name, scheduler.<field>, after checking the deprecated
worker.<field> against it. Args (dict): ctx (root context), field.
*/}}
{{- define "monitoring-platform.checkJobQueueName" -}}
{{- $name := index .ctx.Values.scheduler .field | default "" | toString -}}
{{- if not $name -}}
{{- fail (printf "scheduler.%s must be set: it names the check-job queue for the API, scheduler and workers" .field) -}}
{{- end -}}
{{- $legacy := index (.ctx.Values.worker | default dict) .field | default "" | toString -}}
{{- if and $legacy (ne $legacy $name) -}}
{{- fail (printf "worker.%s (%q) differs from scheduler.%s (%q). The API, scheduler and workers must share one check-job queue, and scheduler.%s now sets it for all of them: remove worker.%s (with --reuse-values it can be a previous chart version's default; set it to null)." .field $legacy .field $name .field .field) -}}
{{- end -}}
{{- $name -}}
{{- end -}}

{{/*
Edge env for the workloads browsers reach directly (.component: api or
status-page), from the top-level trustedProxyCIDRs and security values:
TRUSTED_PROXY_CIDRS (client-IP resolution) and SECURITY_HSTS_MAX_AGE on both;
the general rate limit and CORS allowlist on the api; APP_BASE_URL on the
status page, whose CSP frame-ancestors admits the operator UI (template
editor preview). The api gets APP_BASE_URL from smtpEnv already.
*/}}
{{- define "monitoring-platform.edgeEnv" -}}
{{- $v := .ctx.Values -}}
{{- with $v.trustedProxyCIDRs }}
- name: TRUSTED_PROXY_CIDRS
  value: {{ join "," . | quote }}
{{- end }}
- name: SECURITY_HSTS_MAX_AGE
  value: {{ include "monitoring-platform.hstsMaxAge" .ctx | quote }}
{{- if eq .component "api" }}
- name: API_RATE_LIMIT_PER_MINUTE
  value: {{ $v.security.apiRateLimitPerMinute | int64 | quote }}
- name: API_RATE_LIMIT_BURST
  value: {{ $v.security.apiRateLimitBurst | int64 | quote }}
{{- with $v.security.apiAllowedOrigins }}
- name: API_ALLOWED_ORIGINS
  value: {{ join "," . | quote }}
{{- end }}
{{- end }}
{{- if eq .component "status-page" }}
{{- with $v.appBaseURL }}
- name: APP_BASE_URL
  value: {{ . | quote }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Effective SECURITY_HSTS_MAX_AGE: security.hstsMaxAge when set, else one year
when the chart's ingress terminates TLS, else 0 (off).
*/}}
{{- define "monitoring-platform.hstsMaxAge" -}}
{{- if ne (toString .Values.security.hstsMaxAge) "" -}}
{{- .Values.security.hstsMaxAge | int64 -}}
{{- else if and .Values.ingress.enabled .Values.ingress.tls -}}
31536000
{{- else -}}
0
{{- end -}}
{{- end }}

{{/*
Worker pod securityContext: podSecurityContext plus, when pingGroupRange is
set, the net.ipv4.ping_group_range sysctl that unprivileged ICMP sockets
need (ping monitors). Takes (dict "ctx" $ "pingGroupRange" value): the
default worker fleet passes worker.pingGroupRange, a worker.locations entry
its own pingGroupRange when it has the key. A ping_group_range entry already
in podSecurityContext.sysctls wins.
*/}}
{{- define "monitoring-platform.workerPodSecurityContext" -}}
{{- $psc := deepCopy (default dict .ctx.Values.podSecurityContext) -}}
{{- with .pingGroupRange -}}
{{- $sysctls := list -}}
{{- $present := false -}}
{{- range ($psc.sysctls | default list) -}}
{{- $sysctls = append $sysctls . -}}
{{- if eq .name "net.ipv4.ping_group_range" }}{{ $present = true }}{{ end -}}
{{- end -}}
{{- if not $present -}}
{{- $sysctls = append $sysctls (dict "name" "net.ipv4.ping_group_range" "value" (toString .)) -}}
{{- end -}}
{{- $_ := set $psc "sysctls" $sysctls -}}
{{- end -}}
{{- toYaml $psc -}}
{{- end -}}

{{/*
STATUS_PAGE_SESSION_SECRET for the chart-managed Secret: secrets.statusPageSessionSecret,
else derived from a chart-managed adminJwtSecret (stable across renders; never
randAlphaNum or lookup), else empty when the chart cannot derive it (the JWT
secret comes from an existing Secret), which leaves gated pages closed.
*/}}
{{- define "monitoring-platform.statusPageSessionSecret" -}}
{{- if .Values.secrets.statusPageSessionSecret -}}
{{- .Values.secrets.statusPageSessionSecret -}}
{{- else if and .Values.secrets.adminJwtSecret (not .Values.secrets.adminJwtSecretExistingSecret) -}}
{{- sha256sum (printf "probara/status-page-session/v1:%s" .Values.secrets.adminJwtSecret) -}}
{{- end -}}
{{- end }}
