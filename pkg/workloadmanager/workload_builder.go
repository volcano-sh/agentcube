package workloadmanager

import (
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	runtimev1alpha1 "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/pointer"
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
			Kind:       "Sandbox",
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
			Replicas:     pointer.Int32(1),
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
			APIVersion: "agents.x-k8s.io/v1alpha1",
			Kind:       "Sandbox",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        params.name,
			Namespace:   params.namespace,
			Labels:      map[string]string{},
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
		log.Printf("agent runtime %s type asserting unstructured.Unstructured failed", agentRuntimeKey)
		return nil, nil, fmt.Errorf("agent runtime type asserting failed")
	}

	var agentRuntimeObj runtimev1alpha1.AgentRuntime
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredObj.Object, &agentRuntimeObj); err != nil {
		return nil, nil, fmt.Errorf("failed to convert unstructured to AgentRuntime: %w", err)
	}

	if agentRuntimeObj.Spec.Template == nil {
		return nil, nil, fmt.Errorf("agent runtime %s has no template", agentRuntimeKey)
	}

	sessionId := uuid.New().String()
	sandboxName := "agent-runtime-" + uuid.New().String()
	buildParams := &buildSandboxParams{
		namespace:    namespace,
		workloadName: name,
		sandboxName:  sandboxName,
		sessionID:    sessionId,
		podSpec:      agentRuntimeObj.Spec.Template.Spec,
	}
	if agentRuntimeObj.Spec.MaxSessionDuration != nil {
		buildParams.ttl = agentRuntimeObj.Spec.MaxSessionDuration.Duration
	}
	if agentRuntimeObj.Spec.SessionTimeout != nil {
		buildParams.idleTimeout = agentRuntimeObj.Spec.SessionTimeout.Duration
	}
	sandbox := buildSandboxObject(buildParams)
	externalInfo := &sandboxExternalInfo{
		Ports:     agentRuntimeObj.Spec.Ports,
		SessionID: sessionId,
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
		log.Printf("code interpreter %s type asserting unstructured.Unstructured failed", codeInterpreterKey)
		return nil, nil, nil, fmt.Errorf("code interpreter type asserting failed")
	}

	var codeInterpreterObj runtimev1alpha1.CodeInterpreter
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredObj.Object, &codeInterpreterObj); err != nil {
		return nil, nil, nil, fmt.Errorf("failed to convert unstructured to CodeInterpreter: %w", err)
	}

	sessionId := uuid.New().String()
	sandboxName := "code-interpreter-" + uuid.New().String()
	externalInfo := &sandboxExternalInfo{
		Ports:     codeInterpreterObj.Spec.Ports,
		SessionID: sessionId,
	}

	if codeInterpreterObj.Spec.WarmPoolSize != nil && *codeInterpreterObj.Spec.WarmPoolSize > 0 {
		sandboxClaimName := sandboxName
		sandboxClaim := buildSandboxClaimObject(&buildSandboxClaimParams{
			namespace:           namespace,
			name:                sandboxClaimName,
			sandboxTemplateName: codeInterpreterName,
		})
		simpleSandbox := &sandboxv1alpha1.Sandbox{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      sandboxName,
			},
		}
		externalInfo.SandboxClaimName = sandboxClaimName
		return simpleSandbox, sandboxClaim, externalInfo, nil
	}

	if codeInterpreterObj.Spec.Template == nil {
		return nil, nil, nil, fmt.Errorf("code interpreter %s has no template", codeInterpreterKey)
	}

	podSpec := corev1.PodSpec{
		ImagePullSecrets: codeInterpreterObj.Spec.Template.ImagePullSecrets,
		Containers: []corev1.Container{
			{
				Name:            "code-interpreter",
				Image:           codeInterpreterObj.Spec.Template.Image,
				ImagePullPolicy: codeInterpreterObj.Spec.Template.ImagePullPolicy,
				Env:             codeInterpreterObj.Spec.Template.Environment,
				Command:         codeInterpreterObj.Spec.Template.Command,
				Args:            codeInterpreterObj.Spec.Template.Args,
				Resources:       codeInterpreterObj.Spec.Template.Resources,
			},
		},
		RuntimeClassName: codeInterpreterObj.Spec.Template.RuntimeClassName,
	}

	// Inject bootstrap key volume and mount
	bootstrapVolume := corev1.Volume{
		Name: "jwt-public-key",
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: "agentcube-jwt-public-key",
			},
		},
	}
	podSpec.Volumes = append(podSpec.Volumes, bootstrapVolume)

	if len(podSpec.Containers) > 0 {
		podSpec.Containers[0].VolumeMounts = append(podSpec.Containers[0].VolumeMounts, corev1.VolumeMount{
			Name:      "jwt-public-key",
			MountPath: "/etc/picod",
			ReadOnly:  true,
		})
	}

	buildParams := &buildSandboxParams{
		sandboxName:    sandboxName,
		namespace:      namespace,
		sessionID:      sessionId,
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
