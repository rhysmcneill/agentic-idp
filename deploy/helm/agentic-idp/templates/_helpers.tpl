{{- define "idp.fullname" -}}
{{- if contains .Chart.Name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "idp.labels" -}}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
{{- end -}}

{{- define "idp.selector" -}}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: {{ .component }}
{{- end -}}

{{- define "idp.image" -}}
{{- printf "%s/%s:%s" .root.Values.image.registry .cfg.image.repository (default .root.Chart.AppVersion .cfg.image.tag) -}}
{{- end -}}

{{- define "idp.bootstrapSecretName" -}}
{{- default (printf "%s-bootstrap" (include "idp.fullname" .)) .Values.bootstrapToken.existingSecret -}}
{{- end -}}

{{- define "idp.databaseSecretName" -}}
{{- default (printf "%s-database" (include "idp.fullname" .)) .Values.database.existingSecret -}}
{{- end -}}

{{- define "idp.worker.serviceAccountName" -}}
{{- if .Values.worker.serviceAccount.create -}}
{{- default (printf "%s-worker" (include "idp.fullname" .)) .Values.worker.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.worker.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "idp.securityContext" -}}
runAsNonRoot: true
runAsUser: 65532
runAsGroup: 65532
allowPrivilegeEscalation: false
readOnlyRootFilesystem: true
capabilities:
  drop: ["ALL"]
seccompProfile:
  type: RuntimeDefault
{{- end -}}
