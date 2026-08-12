{{/*
The chart name.
*/}}
{{- define "kem-agent.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
The fully qualified release name, used to name every object.
*/}}
{{- define "kem-agent.fullname" -}}
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
The labels set on every object.
*/}}
{{- define "kem-agent.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{ include "kem-agent.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
The labels selecting the agent pod.
*/}}
{{- define "kem-agent.selectorLabels" -}}
app.kubernetes.io/name: {{ include "kem-agent.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
The agent configuration as the mounted file holds it. The values are copied
and filled in place, and never passed through tpl, so a {{ }} in the config,
such as a webhook template, reaches the agent as written.
*/}}
{{- define "kem-agent.config" -}}
{{- $cfg := deepCopy (required "config is required" .Values.config) }}
{{- if not $cfg.pipelines }}
{{- fail "config.pipelines is empty: declare at least one pipeline, as the example in values.yaml shows" }}
{{- end }}
{{- $source := default dict $cfg.source }}
{{- if not $source.watches }}
{{- $_ := set $source "watches" (list (dict "namespace" .Release.Namespace)) }}
{{- end }}
{{- $enrichment := default dict $source.enrichment }}
{{- if $enrichment.resources }}
{{- $names := list }}
{{- range $i, $r := $enrichment.resources }}
{{- if not (and (kindIs "map" $r) $r.name) }}
{{- fail (printf "config.source.enrichment.resources[%d] must be an object with a name" $i) }}
{{- end }}
{{- $names = append $names $r.name }}
{{- end }}
{{- $_ := set $enrichment "resources" $names }}
{{- end }}
{{- $_ := set $cfg "source" $source }}
{{- $store := dig "checkpoint" "store" dict $cfg }}
{{- if and (eq (toString $store.type) "configmap") (not $store.name) }}
{{- $_ := set $store "name" (printf "%s-checkpoint" (include "kem-agent.fullname" .)) }}
{{- end }}
{{- toYaml $cfg }}
{{- end }}

{{/*
The agent image, tagged with the chart appVersion unless a tag is set, and
pinned to a digest when one is.
*/}}
{{- define "kem-agent.image" -}}
{{- $ref := printf "%s:%s" .Values.image.repository (default .Chart.AppVersion .Values.image.tag) }}
{{- if .Values.image.digest }}
{{- $ref = printf "%s@%s" $ref .Values.image.digest }}
{{- end }}
{{- $ref }}
{{- end }}

{{/*
The port of a listen address such as :8080 or 0.0.0.0:8080. The render fails
when the address has no port a container can expose.
*/}}
{{- define "kem-agent.port" -}}
{{- $port := regexFind "[0-9]+$" . | default "0" | int }}
{{- if not (and (regexMatch ":[0-9]+$" .) (le 1 $port) (le $port 65535)) }}
{{- fail (printf "listen address %q needs a port between 1 and 65535" .) }}
{{- end }}
{{- $port }}
{{- end }}

{{/*
The port the agent serves its probes and metrics on, read from its HTTP listen
address.
*/}}
{{- define "kem-agent.httpPort" -}}
{{- include "kem-agent.port" (required "config.service.http_server.addr is required" (dig "service" "http_server" "addr" "" .Values.config)) }}
{{- end }}

{{/*
The ServiceAccount the pod runs as. It uses the name set in the values, otherwise
either the fullname when the chart creates it, or the namespace's default one when
it does not.
*/}}
{{- define "kem-agent.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "kem-agent.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
The RBAC inputs derived from the rendered configuration, as JSON: watch scope
and namespaces, enrichment resources by scope, and the ConfigMap checkpoint
store.
*/}}
{{- define "kem-agent.rbac" -}}
{{- $cfg := include "kem-agent.config" . | fromYaml }}
{{- $scope := "multi" }}
{{- $namespaces := list }}
{{- range $i, $w := $cfg.source.watches }}
{{- if not (kindIs "map" $w) }}
{{- fail (printf "config.source.watches[%d] must be an object" $i) }}
{{- end }}
{{- if $w.namespace }}
{{- $namespaces = append $namespaces (toString $w.namespace) }}
{{- else }}
{{- $scope = "cluster" }}
{{- end }}
{{- end }}
{{- $namespaces = $namespaces | uniq | sortAlpha }}
{{- if eq $scope "cluster" }}
{{- $namespaces = list }}
{{- else if eq (len $namespaces) 1 }}
{{- $scope = "single" }}
{{- end }}
{{- $namespaced := list }}
{{- $clusterScoped := list }}
{{- range dig "source" "enrichment" "resources" (list) .Values.config }}
{{- $parts := splitn "." 2 (toString .name) }}
{{- $resource := dict "group" (default "" $parts._1) "resource" $parts._0 }}
{{- if .clusterScoped }}
{{- $clusterScoped = append $clusterScoped $resource }}
{{- else }}
{{- $namespaced = append $namespaced $resource }}
{{- end }}
{{- end }}
{{- $checkpoint := dict }}
{{- $store := dig "checkpoint" "store" (dict) $cfg }}
{{- if eq (toString $store.type) "configmap" }}
{{- $checkpoint = dict "name" $store.name "namespace" (default .Release.Namespace $store.namespace) }}
{{- end }}
{{- toJson (dict "scope" $scope "namespaces" $namespaces "namespaced" $namespaced "clusterScoped" $clusterScoped "checkpoint" $checkpoint) }}
{{- end }}
