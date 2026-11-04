{{- define "ecg-hub.labels" -}}
app.kubernetes.io/name: ecg-hub
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
{{- end }}

{{- define "ecg-hub.config" -}}
{{- .Values.config | default (.Files.Get "files/config.yaml") -}}
{{- end }}

{{/* Name of the PVC behind a volume ("ecg" or "quarantine"). */}}
{{- define "ecg-hub.claim" -}}
{{- $v := index .root.Values.persistence .name -}}
{{- if eq .root.Values.persistence.mode "existingClaim" -}}
{{- required (printf "persistence.%s.existingClaim is required" .name) $v.existingClaim -}}
{{- else -}}
{{- printf "ecg-hub-%s" .name -}}
{{- end -}}
{{- end }}
