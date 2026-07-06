{{/*
Copyright 2025 Tencent
SPDX-License-Identifier: MIT

WeKnora Helm Chart Template Helpers

Best Practices References:
- https://helm.sh/docs/chart_best_practices/templates/
- https://github.com/argoproj/argo-helm/blob/main/charts/argo-cd/templates/_helpers.tpl
*/}}

{{/*
Expand the name of the chart.
*/}}
{{- define "weknora.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "weknora.fullname" -}}
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
Ref: https://helm.sh/docs/chart_best_practices/labels/
*/}}
{{- define "weknora.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels following Kubernetes recommended labels.
Ref: https://kubernetes.io/docs/concepts/overview/working-with-objects/common-labels/
*/}}
{{- define "weknora.labels" -}}
helm.sh/chart: {{ include "weknora.chart" . }}
{{ include "weknora.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: weknora
{{- end }}

{{/*
Selector labels
*/}}
{{- define "weknora.selectorLabels" -}}
app.kubernetes.io/name: {{ include "weknora.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Component labels - use for individual components
Usage: {{ include "weknora.componentLabels" (dict "component" "app" "context" .) }}
*/}}
{{- define "weknora.componentLabels" -}}
{{ include "weknora.labels" .context }}
app.kubernetes.io/component: {{ .component }}
{{- end }}

{{/*
Component selector labels
Usage: {{ include "weknora.componentSelectorLabels" (dict "component" "app" "context" .) }}
*/}}
{{- define "weknora.componentSelectorLabels" -}}
{{ include "weknora.selectorLabels" .context }}
app.kubernetes.io/component: {{ .component }}
{{- end }}

{{/*
Create the name of the service account to use.
Ref: https://helm.sh/docs/chart_best_practices/rbac/
*/}}
{{- define "weknora.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "weknora.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Secret name - supports existing secret
*/}}
{{- define "weknora.secretName" -}}
{{- if .Values.secrets.existingSecret }}
{{- .Values.secrets.existingSecret }}
{{- else }}
{{- include "weknora.fullname" . }}-secrets
{{- end }}
{{- end }}

{{/*
主业务数据库驱动。
*/}}
{{- define "weknora.database.driver" -}}
{{- default "postgres" .Values.database.driver }}
{{- end }}

{{/*
主业务数据库主机。
*/}}
{{- define "weknora.database.host" -}}
{{- $driver := include "weknora.database.driver" . }}
{{- if .Values.database.external.host }}
{{- .Values.database.external.host }}
{{- else if eq $driver "mysql" }}
{{- "mysql" }}
{{- else }}
{{- "postgres" }}
{{- end }}
{{- end }}

{{/*
主业务数据库端口。
*/}}
{{- define "weknora.database.port" -}}
{{- $driver := include "weknora.database.driver" . }}
{{- if .Values.database.external.port }}
{{- .Values.database.external.port }}
{{- else if eq $driver "mysql" }}
{{- "3306" }}
{{- else }}
{{- "5432" }}
{{- end }}
{{- end }}

{{/*
Effective database user.
*/}}
{{- define "weknora.database.user" -}}
{{- $driver := include "weknora.database.driver" . }}
{{- $user := default "postgres" .Values.secrets.dbUser }}
{{- if and (eq $driver "mysql") (or (eq $user "") (eq $user "postgres")) }}
{{- "root" }}
{{- else }}
{{- $user }}
{{- end }}
{{- end }}

{{/*
Default retrieval driver.
*/}}
{{- define "weknora.retrieve.driver" -}}
{{- $dbDriver := include "weknora.database.driver" . }}
{{- $retrieveDriver := default "" .Values.app.env.RETRIEVE_DRIVER }}
{{- if and (eq $dbDriver "mysql") (or (eq $retrieveDriver "") (eq $retrieveDriver "postgres")) }}
{{- "mysql" }}
{{- else }}
{{- default "postgres" $retrieveDriver }}
{{- end }}
{{- end }}

{{/*
Return the app image with tag.
Defaults to Chart.appVersion if tag is not specified.
*/}}
{{- define "weknora.app.image" -}}
{{- $tag := default .Chart.AppVersion .Values.app.image.tag }}
{{- printf "%s:%s" .Values.app.image.repository $tag }}
{{- end }}

{{/*
Return the frontend image with tag.
*/}}
{{- define "weknora.frontend.image" -}}
{{- printf "%s:%s" .Values.frontend.image.repository .Values.frontend.image.tag }}
{{- end }}

{{/*
Return the docreader image with tag.
*/}}
{{- define "weknora.docreader.image" -}}
{{- printf "%s:%s" .Values.docreader.image.repository .Values.docreader.image.tag }}
{{- end }}

{{/*
Return the PostgreSQL image with tag.
*/}}
{{- define "weknora.postgresql.image" -}}
{{- printf "%s:%s" .Values.postgresql.image.repository .Values.postgresql.image.tag }}
{{- end }}

{{/*
返回带标签的 MySQL 镜像。
*/}}
{{- define "weknora.mysql.image" -}}
{{- printf "%s:%s" .Values.mysql.image.repository .Values.mysql.image.tag }}
{{- end }}

{{/*
Return the Redis image with tag.
*/}}
{{- define "weknora.redis.image" -}}
{{- printf "%s:%s" .Values.redis.image.repository .Values.redis.image.tag }}
{{- end }}

{{/*
Return the Neo4j image with tag.
*/}}
{{- define "weknora.neo4j.image" -}}
{{- printf "%s:%s" .Values.neo4j.image.repository .Values.neo4j.image.tag }}
{{- end }}

{{/*
Create image pull secrets list.
*/}}
{{- define "weknora.imagePullSecrets" -}}
{{- with .Values.global.imagePullSecrets }}
imagePullSecrets:
{{- toYaml . | nindent 2 }}
{{- end }}
{{- end }}

{{/*
Return the storage class name.
*/}}
{{- define "weknora.storageClass" -}}
{{- if .Values.global.storageClass }}
{{- if eq .Values.global.storageClass "-" }}
storageClassName: ""
{{- else }}
storageClassName: {{ .Values.global.storageClass | quote }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Pod security context.
Merges global defaults with component-specific overrides.
*/}}
{{- define "weknora.podSecurityContext" -}}
{{- $global := .Values.global.podSecurityContext | default dict }}
{{- $component := .componentSecurityContext | default dict }}
{{- $merged := merge $component $global }}
{{- if $merged }}
securityContext:
{{- toYaml $merged | nindent 2 }}
{{- end }}
{{- end }}

{{/*
Container security context.
*/}}
{{- define "weknora.containerSecurityContext" -}}
{{- if . }}
securityContext:
{{- toYaml . | nindent 2 }}
{{- end }}
{{- end }}
