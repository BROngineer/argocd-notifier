{{- define "argocd-notifier.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "argocd-notifier.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "argocd-notifier.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "argocd-notifier.imageTag" -}}
{{- if .Values.image.tag -}}
{{- .Values.image.tag -}}
{{- else -}}
v{{ .Chart.AppVersion }}
{{- end -}}
{{- end -}}

{{- define "argocd-notifier.labels" -}}
helm.sh/chart: {{ include "argocd-notifier.chart" . }}
{{ include "argocd-notifier.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "argocd-notifier.selectorLabels" -}}
app.kubernetes.io/name: {{ include "argocd-notifier.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "argocd-notifier.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "argocd-notifier.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "argocd-notifier.leaderElectionNamespace" -}}
{{- default .Release.Namespace .Values.leaderElection.namespace -}}
{{- end -}}

{{- /*
The headless Service backing per-pod DNS (<pod-name>.<this>.<namespace>.svc.cluster.local)
that lets a non-leader resolve and forward requests to the current leader —
must live in the pods' own namespace (Release.Namespace), which is not
necessarily the same as leaderElectionNamespace (that's just where the Lease
lives, and can be a different namespace by design).
*/ -}}
{{- define "argocd-notifier.headlessServiceName" -}}
{{- printf "%s-headless" (include "argocd-notifier.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "argocd-notifier.leaderProxyDNSSuffix" -}}
{{ include "argocd-notifier.headlessServiceName" . }}.{{ .Release.Namespace }}.svc.cluster.local
{{- end -}}

{{- define "argocd-notifier.slackBackendFullname" -}}
{{- printf "%s-slack-backend" (include "argocd-notifier.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "argocd-notifier.slackBackendImageTag" -}}
{{- if .Values.slackBackend.image.tag -}}
{{- .Values.slackBackend.image.tag -}}
{{- else -}}
v{{ .Chart.AppVersion }}
{{- end -}}
{{- end -}}

{{- /*
Distinct app.kubernetes.io/name from the core's, so this workload's Service
selector never matches the core's pods (and vice versa) — Deployment
selectors are immutable, so the core's existing selectorLabels must never
change; this component gets its own instead of a shared "component" label.
*/ -}}
{{- define "argocd-notifier.slackBackendSelectorLabels" -}}
app.kubernetes.io/name: {{ include "argocd-notifier.name" . }}-slack-backend
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "argocd-notifier.slackBackendLabels" -}}
helm.sh/chart: {{ include "argocd-notifier.chart" . }}
{{ include "argocd-notifier.slackBackendSelectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "argocd-notifier.slackBackendSecretName" -}}
{{- default (include "argocd-notifier.slackBackendFullname" .) .Values.slackBackend.slack.existingSecret -}}
{{- end -}}

{{- define "argocd-notifier.slackBackendSecretKey" -}}
{{- if .Values.slackBackend.slack.existingSecret -}}
{{- default "SLACK_BOT_TOKEN" .Values.slackBackend.slack.existingSecretKey -}}
{{- else -}}
SLACK_BOT_TOKEN
{{- end -}}
{{- end -}}

{{- /* This release's own core Service address — overridable for a
slack-backend pointed at a core outside this release. */ -}}
{{- define "argocd-notifier.coreURL" -}}
{{- if .Values.slackBackend.coreURL -}}
{{- .Values.slackBackend.coreURL -}}
{{- else -}}
http://{{ include "argocd-notifier.fullname" . }}.{{ .Release.Namespace }}.svc.cluster.local:{{ .Values.service.port }}
{{- end -}}
{{- end -}}

{{- define "argocd-notifier.slackBackendPublicBaseURL" -}}
{{- if .Values.slackBackend.publicBaseURL -}}
{{- .Values.slackBackend.publicBaseURL -}}
{{- else -}}
http://{{ include "argocd-notifier.slackBackendFullname" . }}.{{ .Release.Namespace }}.svc.cluster.local:{{ .Values.slackBackend.service.port }}
{{- end -}}
{{- end -}}
