{{/* Labels Helm adds on top of the ones the kustomize base sets. Never part
of a selector, so the chart and the base select the same pods. */}}
{{- define "pssst.helmLabels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/part-of: pssst
{{- end -}}

{{- define "pssst.inventory" -}}
{{- if .Values.inventory -}}
{{ .Values.inventory }}
{{- else -}}
{{ .Files.Get "files/pssst.psp.yml" }}
{{- end -}}
{{- end -}}
