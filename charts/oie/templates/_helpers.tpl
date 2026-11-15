{{- define "oie.fullname" -}}
{{- $release := replace "." "-" .Release.Name -}}
{{- if or (eq $release .Chart.Name) (hasPrefix (printf "%s-" .Chart.Name) $release) -}}
{{- $release -}}
{{- else -}}
{{- printf "%s-%s" .Chart.Name $release -}}
{{- end -}}
{{- end -}}

{{- define "oie.selectorLabels" -}}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/instance: {{ .Release.Name | quote }}
{{- end -}}

{{- define "oie.labels" -}}
{{ include "oie.selectorLabels" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | quote }}
{{- end -}}

{{- define "oie.image" -}}
{{- if .Values.image.digest -}}
{{- printf "%s@%s" .Values.image.repository .Values.image.digest -}}
{{- else -}}
{{- printf "%s:%s" .Values.image.repository (default .Chart.AppVersion .Values.image.tag) -}}
{{- end -}}
{{- end -}}
