{{/*
Release-scoped name. Two installs in one namespace must not collide, so the
release name leads; the chart name is dropped when the release already carries
it, which keeps `helm install kbengine …` from producing "kbengine-kbengine".
*/}}
{{- define "kbengine.fullname" -}}
{{- if contains .Chart.Name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "kbengine.name" -}}
{{- .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Labels every object carries. Selector labels are a strict subset and are kept
separate: a Deployment's selector is immutable after creation, so anything that
changes between releases (the version) must stay out of it.
*/}}
{{- define "kbengine.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{ include "kbengine.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: kb-engine
{{- end -}}

{{- define "kbengine.selectorLabels" -}}
app.kubernetes.io/name: {{ include "kbengine.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/*
Which ConfigMap the catalog comes from: the one the user brought, or the one
this chart renders.
*/}}
{{- define "kbengine.catalogConfigMap" -}}
{{- if .Values.catalog.existingConfigMap -}}
{{- .Values.catalog.existingConfigMap -}}
{{- else -}}
{{- printf "%s-catalog" (include "kbengine.fullname" .) -}}
{{- end -}}
{{- end -}}
