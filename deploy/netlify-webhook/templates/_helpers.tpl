{{/* vim: set filetype=mustache: */}}
{{/*
Expand the name of the chart.
*/}}
{{- define "webhook-netlify-dns.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "webhook-netlify-dns.fullname" -}}
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

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "webhook-netlify-dns.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "webhook-netlify-dns.selfSignedIssuer" -}}
{{ printf "%s-selfsign" (include "webhook-netlify-dns.fullname" .) }}
{{- end -}}

{{- define "webhook-netlify-dns.rootCAIssuer" -}}
{{ printf "%s-ca" (include "webhook-netlify-dns.fullname" .) }}
{{- end -}}

{{- define "webhook-netlify-dns.rootCACertificate" -}}
{{ printf "%s-ca" (include "webhook-netlify-dns.fullname" .) }}
{{- end -}}

{{- define "webhook-netlify-dns.servingCertificate" -}}
{{ printf "%s-webhook-tls" (include "webhook-netlify-dns.fullname" .) }}
{{- end -}}
