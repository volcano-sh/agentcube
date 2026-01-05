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
	"time"

	"github.com/google/uuid"
	runtimev1alpha1 "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
	"github.com/volcano-sh/agentcube/pkg/common/types"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
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
					Labels:      params.podLabels,
					Annotations: params.podAnnotations,
				},
			},
			ShutdownTime: &shutdownTime,
			Replicas:     ptr.To[int32](1),
		},
	}
	if len(sandbox.Spec.PodTemplate.ObjectMeta.Labels) == 0 {
		sandbox.Spec.PodTemplate.ObjectMeta.Labels = make(map[string]string, 2)
	}
	sandbox.Spec.PodTemplate.ObjectMeta.Labels[SessionIdLabelKey] = params.sessionID
	sandbox.Spec.PodTemplate.ObjectMeta.Labels["sandbox-name"] = params.sandboxName
	return sandbox
}

func buildSandboxClaimObject(params *buildSandboxClaimParams) *extensionsv1alpha1.SandboxClaim {
	sandboxClaim := &extensionsv1alpha1.SandboxClaim{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "extensions.agents.x-k8s.io/v1alpha1",
			Kind:       types.SandboxClaimsKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      params.name,
			Namespace: params.namespace,
			Labels: map[string]string{
				SessionIdLabelKey: params.sessionID,
				"sandbox-name":    params.name,
			},
			Annotations: map[string]string{},
		},
		Spec: extensionsv1alpha1.SandboxClaimSpec{
			TemplateRef: extensionsv1alpha1.SandboxTemplateRef{
				Name: params.sandboxTemplateName,
			},
		},
	}
	return sandboxClaim
}

func buildSandboxByAgentRuntime(namespace string, name string, ifm *Informers) (*sandboxv1alpha1.Sandbox, *sandboxExternalInfo, error) {
	agentRuntimeKey := namespace + "/" + name
	runtimeObj, exists, err := ifm.AgentRuntimeInformer.GetStore().GetByKey(agentRuntimeKey)
	if err != nil {
		return nil, nil, fmt.Errorf("get agent runtime %s from informer failed: %v", agentRuntimeKey, err)
	}
	if !exists {
		return nil, nil, fmt.Errorf("agent runtime %s not found", agentRuntimeKey)
	}

	unstructuredObj, ok := runtimeObj.(*unstructured.Unstructured)
	if !ok {
		klog.Errorf("agent runtime %s type asserting unstructured.Unstructured failed", agentRuntimeKey)
		return nil, nil, fmt.Errorf("agent runtime type asserting failed")
	}

	var agentRuntimeObj runtimev1alpha1.AgentRuntime
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredObj.Object, &agentRuntimeObj); err != nil {
		return nil, nil, fmt.Errorf("failed to convert unstructured to AgentRuntime: %w", err)
	}

	if agentRuntimeObj.Spec.Template == nil {
		return nil, nil, fmt.Errorf("agent runtime %s has no template", agentRuntimeKey)
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
	if agentRuntimeObj.Spec.MaxSessionDuration != nil {
		buildParams.ttl = agentRuntimeObj.Spec.MaxSessionDuration.Duration
	}
	if agentRuntimeObj.Spec.SessionTimeout != nil {
		buildParams.idleTimeout = agentRuntimeObj.Spec.SessionTimeout.Duration
	}
	sandbox := buildSandboxObject(buildParams)
	externalInfo := &sandboxExternalInfo{
		Kind:               types.SandboxKind,
		Ports:              agentRuntimeObj.Spec.Ports,
		SessionID:          sessionID,
		NeedInitialization: false,
	}
	return sandbox, externalInfo, nil
}

func buildSandboxByCodeInterpreter(namespace string, codeInterpreterName string, ifm *Informers) (*sandboxv1alpha1.Sandbox, *extensionsv1alpha1.SandboxClaim, *sandboxExternalInfo, error) {
	codeInterpreterKey := namespace + "/" + codeInterpreterName
	runtimeObj, exists, err := ifm.CodeInterpreterInformer.GetStore().GetByKey(codeInterpreterKey)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("get code interpreter %s from informer failed: %v", codeInterpreterKey, err)
	}

	if !exists {
		return nil, nil, nil, fmt.Errorf("code interpreter %s not found", codeInterpreterKey)
	}

	unstructuredObj, ok := runtimeObj.(*unstructured.Unstructured)
	if !ok {
		klog.Errorf("code interpreter %s type asserting unstructured.Unstructured failed", codeInterpreterKey)
		return nil, nil, nil, fmt.Errorf("code interpreter type asserting failed")
	}

	var codeInterpreterObj runtimev1alpha1.CodeInterpreter
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredObj.Object, &codeInterpreterObj); err != nil {
		return nil, nil, nil, fmt.Errorf("failed to convert unstructured to CodeInterpreter: %w", err)
	}

	sessionID := uuid.New().String()
	sandboxName := fmt.Sprintf("%s-%s", codeInterpreterName, RandString(8))
	externalInfo := &sandboxExternalInfo{
		Kind:               types.SandboxKind,
		Ports:              codeInterpreterObj.Spec.Ports,
		SessionID:          sessionID,
		NeedInitialization: true,
	}

	if codeInterpreterObj.Spec.NeedInitialization != nil {
		externalInfo.NeedInitialization = *codeInterpreterObj.Spec.NeedInitialization
	}

	if codeInterpreterObj.Spec.WarmPoolSize != nil && *codeInterpreterObj.Spec.WarmPoolSize > 0 {
		sandboxClaim := buildSandboxClaimObject(&buildSandboxClaimParams{
			namespace:           namespace,
			name:                sandboxName,
			sandboxTemplateName: codeInterpreterName,
			sessionID:           sessionID,
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
		externalInfo.Kind = types.SandboxClaimsKind
		return simpleSandbox, sandboxClaim, externalInfo, nil
	}

	if codeInterpreterObj.Spec.Template == nil {
		return nil, nil, nil, fmt.Errorf("code interpreter %s has no template", codeInterpreterKey)
	}

	// Normalize RuntimeClassName: if it's an empty string, set it to nil
	runtimeClassName := codeInterpreterObj.Spec.Template.RuntimeClassName
	if runtimeClassName != nil && *runtimeClassName == "" {
		runtimeClassName = nil
	}

	podSpec := corev1.PodSpec{
		ImagePullSecrets: codeInterpreterObj.Spec.Template.ImagePullSecrets,
		RuntimeClassName: runtimeClassName,
		Containers: []corev1.Container{
			{
				Name:            "code-interpreter",
				Image:           codeInterpreterObj.Spec.Template.Image,
				ImagePullPolicy: codeInterpreterObj.Spec.Template.ImagePullPolicy,
				Env:             codeInterpreterObj.Spec.Template.Environment,
				Command:         codeInterpreterObj.Spec.Template.Command,
				Args:            codeInterpreterObj.Spec.Template.Args,
				Resources:       codeInterpreterObj.Spec.Template.Resources,
				VolumeMounts: []corev1.VolumeMount{
					{
						Name:      JWTKeyVolumeName,
						MountPath: "/etc/picod",
						ReadOnly:  true,
					},
				},
			},
		},
		Volumes: []corev1.Volume{
			{
				Name: JWTKeyVolumeName,
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: JWTKeySecretName,
					},
				},
			},
		},
	}

	buildParams := &buildSandboxParams{
		sandboxName:    sandboxName,
		namespace:      namespace,
		sessionID:      sessionID,
		podSpec:        podSpec,
		podLabels:      codeInterpreterObj.Spec.Template.Labels,
		podAnnotations: codeInterpreterObj.Spec.Template.Annotations,
	}
	if codeInterpreterObj.Spec.MaxSessionDuration != nil {
		buildParams.ttl = codeInterpreterObj.Spec.MaxSessionDuration.Duration
	}
	sandbox := buildSandboxObject(buildParams)
	return sandbox, nil, externalInfo, nil
}
