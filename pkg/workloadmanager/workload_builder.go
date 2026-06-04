/*
Copyright The Volcano Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package workloadmanager

import (
	"fmt"
	"maps"
	"time"

	"github.com/google/uuid"
	"github.com/volcano-sh/agentcube/pkg/api"
	runtimev1alpha1 "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
	"github.com/volcano-sh/agentcube/pkg/common/types"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/utils/ptr"
	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
	extensionsv1alpha1 "sigs.k8s.io/agent-sandbox/extensions/api/v1alpha1"
)

type buildSandboxParams struct {
	namespace      string
	workloadName   string
	sandboxName    string
	sessionID      string
	ttl            time.Duration
	idleTimeout    time.Duration
	podSpec        corev1.PodSpec
	podLabels      map[string]string
	podAnnotations map[string]string
}

type buildSandboxClaimParams struct {
	namespace           string
	name                string
	sandboxTemplateName string
	sessionID           string
	idleTimeout         time.Duration
	// ownerReference is the reference to the CodeInterpreter that creates this SandboxClaim
	ownerReference *metav1.OwnerReference
}

// buildSandboxObject builds a Sandbox object from parameters
func buildSandboxObject(params *buildSandboxParams) *sandboxv1alpha1.Sandbox {
	if params.ttl == 0 {
		params.ttl = DefaultSandboxTTL
	}
	if params.idleTimeout == 0 {
		params.idleTimeout = DefaultSandboxIdleTimeout
	}

	shutdownTime := metav1.NewTime(time.Now().Add(params.ttl))

	// Allocate fresh maps for copied metadata so we never mutate informer-cached input.
	// Annotations are only copied when params.podAnnotations is non-nil.
	podLabels := make(map[string]string, len(params.podLabels)+2)
	maps.Copy(podLabels, params.podLabels)
	podLabels[SessionIdLabelKey] = params.sessionID
	podLabels[SandboxNameLabelKey] = params.sandboxName

	podAnnotations := make(map[string]string, len(params.podAnnotations))
	maps.Copy(podAnnotations, params.podAnnotations)

	// Create Sandbox object using agent-sandbox types
	sandbox := &sandboxv1alpha1.Sandbox{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "agents.x-k8s.io/v1alpha1",
			Kind:       types.SandboxKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      params.sandboxName,
			Namespace: params.namespace,
			Labels: map[string]string{
				SessionIdLabelKey:    params.sessionID,
				WorkloadNameLabelKey: params.workloadName,
				"managed-by":         "agentcube-workload-manager",
			},
			Annotations: map[string]string{
				IdleTimeoutAnnotationKey: params.idleTimeout.String(),
			},
		},
		Spec: sandboxv1alpha1.SandboxSpec{
			PodTemplate: sandboxv1alpha1.PodTemplate{
				Spec: params.podSpec,
				ObjectMeta: sandboxv1alpha1.PodMetadata{
					Labels:      podLabels,
					Annotations: podAnnotations,
				},
			},
			Lifecycle: sandboxv1alpha1.Lifecycle{
				ShutdownTime: &shutdownTime,
			},
			Replicas: ptr.To[int32](1),
		},
	}
	return sandbox
}

func buildSandboxClaimObject(params *buildSandboxClaimParams) *extensionsv1alpha1.SandboxClaim {
	idleTimeout := params.idleTimeout
	if idleTimeout == 0 {
		idleTimeout = DefaultSandboxIdleTimeout
	}
	sandboxClaim := &extensionsv1alpha1.SandboxClaim{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "extensions.agents.x-k8s.io/v1alpha1",
			Kind:       types.SandboxClaimsKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      params.name,
			Namespace: params.namespace,
			Labels: map[string]string{
				SessionIdLabelKey:   params.sessionID,
				SandboxNameLabelKey: params.name,
			},
			Annotations: map[string]string{
				IdleTimeoutAnnotationKey: idleTimeout.String(),
			},
		},
		Spec: extensionsv1alpha1.SandboxClaimSpec{
			TemplateRef: extensionsv1alpha1.SandboxTemplateRef{
				Name: params.sandboxTemplateName,
			},
		},
	}
	// Set owner reference to the CodeInterpreter that creates this SandboxClaim
	if params.ownerReference != nil {
		sandboxClaim.ObjectMeta.OwnerReferences = []metav1.OwnerReference{*params.ownerReference}
	}
	return sandboxClaim
}

func buildSandboxByAgentRuntime(namespace string, name string, ifm *Informers) (*sandboxv1alpha1.Sandbox, *sandboxEntry, error) {
	agentRuntimeObj, err := ifm.AgentRuntimeLister.AgentRuntimes(namespace).Get(name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil, api.ErrAgentRuntimeNotFound
		}
		return nil, nil, fmt.Errorf("failed to get agent runtime %s/%s: %w", namespace, name, err)
	}

	sessionID := uuid.New().String()
	sandboxName := fmt.Sprintf("%s-%s", name, RandString(8))

	// Normalize RuntimeClassName: if it's an empty string, set it to nil
	podSpec := agentRuntimeObj.Spec.Template.Spec.DeepCopy()
	if podSpec.RuntimeClassName != nil && *podSpec.RuntimeClassName == "" {
		podSpec.RuntimeClassName = nil
	}

	buildParams := &buildSandboxParams{
		namespace:    namespace,
		workloadName: name,
		sandboxName:  sandboxName,
		sessionID:    sessionID,
		podSpec:      *podSpec,
	}
	// Apply labels and annotations from AgentRuntime template
	if agentRuntimeObj.Spec.Template.Labels != nil {
		buildParams.podLabels = agentRuntimeObj.Spec.Template.Labels
	}
	if agentRuntimeObj.Spec.Template.Annotations != nil {
		buildParams.podAnnotations = agentRuntimeObj.Spec.Template.Annotations
	}
	if agentRuntimeObj.Spec.MaxSessionDuration != nil {
		buildParams.ttl = agentRuntimeObj.Spec.MaxSessionDuration.Duration
	}
	idleTimeout := DefaultSandboxIdleTimeout
	if agentRuntimeObj.Spec.SessionTimeout != nil {
		idleTimeout = agentRuntimeObj.Spec.SessionTimeout.Duration
	}
	buildParams.idleTimeout = idleTimeout

	sandbox := buildSandboxObject(buildParams)
	entry := &sandboxEntry{
		Kind:        types.SandboxKind,
		Ports:       agentRuntimeObj.Spec.Ports,
		SessionID:   sessionID,
		IdleTimeout: idleTimeout,
		AuthMode:    runtimev1alpha1.AuthModeNone, // AgentRuntime doesn't explicitly have AuthMode defined in the CRD, but we default to None.
	}
	return sandbox, entry, nil
}

// buildCodeInterpreterEnvVars copies the template env vars and injects the
// public key when authMode is picod.
func buildCodeInterpreterEnvVars(templateEnv []corev1.EnvVar, authMode runtimev1alpha1.AuthModeType, bootstrapPubKey string, sessionID string) []corev1.EnvVar {
	envVars := make([]corev1.EnvVar, len(templateEnv))
	copy(envVars, templateEnv)
	if authMode == runtimev1alpha1.AuthModePicoD {
		envVars = append(envVars, corev1.EnvVar{
			Name:  "PICOD_BOOTSTRAP_PUBLIC_KEY",
			Value: bootstrapPubKey,
		})
		if sessionID != "" {
			envVars = append(envVars, corev1.EnvVar{
				Name:  "PICOD_SESSION_ID",
				Value: sessionID,
			})
		}
	}
	return envVars
}

func buildSandboxByCodeInterpreter(namespace string, codeInterpreterName string, informer *Informers, bootstrapPubKey string) (*sandboxv1alpha1.Sandbox, *extensionsv1alpha1.SandboxClaim, *sandboxEntry, error) {
	codeInterpreterObj, err := informer.CodeInterpreterLister.CodeInterpreters(namespace).Get(codeInterpreterName)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil, nil, api.ErrCodeInterpreterNotFound
		}
		return nil, nil, nil, fmt.Errorf("failed to get code interpreter %s/%s: %w", namespace, codeInterpreterName, err)
	}

	// For PicoD auth mode, a bootstrap public key must be provided so it can
	// be injected into the container environment and used for /init verification.
	if codeInterpreterObj.Spec.AuthMode == runtimev1alpha1.AuthModePicoD && bootstrapPubKey == "" {
		return nil, nil, nil, api.ErrPublicKeyMissing
	}

	sessionID := uuid.New().String()
	sandboxName := fmt.Sprintf("%s-%s", codeInterpreterName, RandString(8))

	idleTimeout := DefaultSandboxIdleTimeout
	if codeInterpreterObj.Spec.SessionTimeout != nil {
		idleTimeout = codeInterpreterObj.Spec.SessionTimeout.Duration
	}

	sandboxEntry := &sandboxEntry{
		Kind:        types.SandboxKind,
		Ports:       codeInterpreterObj.Spec.Ports,
		SessionID:   sessionID,
		IdleTimeout: idleTimeout,
		AuthMode:    codeInterpreterObj.Spec.AuthMode,
	}

	// Set default port for code interpreter if not configured
	if len(sandboxEntry.Ports) == 0 {
		sandboxEntry.Ports = []runtimev1alpha1.TargetPort{
			{
				Port:       8080,
				Protocol:   runtimev1alpha1.ProtocolTypeHTTP,
				PathPrefix: "/",
			},
		}
	}

	if codeInterpreterObj.Spec.WarmPoolSize != nil && *codeInterpreterObj.Spec.WarmPoolSize > 0 {
		sandboxClaim := buildSandboxClaimObject(&buildSandboxClaimParams{
			namespace:           namespace,
			name:                sandboxName,
			sandboxTemplateName: codeInterpreterName,
			sessionID:           sessionID,
			idleTimeout:         idleTimeout,
			ownerReference: &metav1.OwnerReference{
				APIVersion: runtimev1alpha1.CodeInterpreterGroupVersionKind.GroupVersion().String(),
				Kind:       runtimev1alpha1.CodeInterpreterKind,
				Name:       codeInterpreterObj.Name,
				UID:        codeInterpreterObj.UID,
			},
		})
		simpleSandbox := &sandboxv1alpha1.Sandbox{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      sandboxName,
				Labels: map[string]string{
					SessionIdLabelKey: sessionID,
				},
			},
		}
		if codeInterpreterObj.Spec.MaxSessionDuration != nil {
			shutdownTime := metav1.NewTime(time.Now().Add(codeInterpreterObj.Spec.MaxSessionDuration.Duration))
			simpleSandbox.Spec.Lifecycle.ShutdownTime = &shutdownTime
		}
		sandboxEntry.Kind = types.SandboxClaimsKind
		return simpleSandbox, sandboxClaim, sandboxEntry, nil
	}

	// Normalize RuntimeClassName: if it's an empty string, set it to nil
	runtimeClassName := codeInterpreterObj.Spec.Template.RuntimeClassName
	if runtimeClassName != nil && *runtimeClassName == "" {
		runtimeClassName = nil
	}

	envVars := buildCodeInterpreterEnvVars(codeInterpreterObj.Spec.Template.Environment, codeInterpreterObj.Spec.AuthMode, bootstrapPubKey, sessionID)

	podSpec := corev1.PodSpec{
		ImagePullSecrets: codeInterpreterObj.Spec.Template.ImagePullSecrets,
		RuntimeClassName: runtimeClassName,
		Containers: []corev1.Container{
			{
				Name:            "code-interpreter",
				Image:           codeInterpreterObj.Spec.Template.Image,
				ImagePullPolicy: codeInterpreterObj.Spec.Template.ImagePullPolicy,
				Env:             envVars,
				Command:         codeInterpreterObj.Spec.Template.Command,
				Args:            codeInterpreterObj.Spec.Template.Args,
				Resources:       codeInterpreterObj.Spec.Template.Resources,
			},
		},
	}

	buildParams := &buildSandboxParams{
		sandboxName:    sandboxName,
		namespace:      namespace,
		workloadName:   codeInterpreterName,
		sessionID:      sessionID,
		podSpec:        podSpec,
		podLabels:      codeInterpreterObj.Spec.Template.Labels,
		podAnnotations: codeInterpreterObj.Spec.Template.Annotations,
		idleTimeout:    idleTimeout,
	}

	if codeInterpreterObj.Spec.MaxSessionDuration != nil {
		buildParams.ttl = codeInterpreterObj.Spec.MaxSessionDuration.Duration
	}
	sandbox := buildSandboxObject(buildParams)
	return sandbox, nil, sandboxEntry, nil
}
