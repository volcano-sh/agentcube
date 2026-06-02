{{/*
Redis Secret name: use existingSecret when set, otherwise chart-managed Secret.
*/}}
{{- define "agentcube.redis.secretName" -}}
{{- if .Values.redis.existingSecret }}
{{- .Values.redis.existingSecret }}
{{- else }}
{{- printf "%s-redis" .Release.Name }}
{{- end }}
{{- end }}

{{/*
Key within the Redis Secret that holds the password.
*/}}
{{- define "agentcube.redis.passwordKey" -}}
{{- .Values.redis.existingSecretPasswordKey | default "password" }}
{{- end }}
