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
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/volcano-sh/agentcube/pkg/api"
	runtimev1alpha1 "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
	"github.com/volcano-sh/agentcube/pkg/common/types"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"
	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
	extensionsv1alpha1 "sigs.k8s.io/agent-sandbox/extensions/api/v1alpha1"
)

// Constants for Router's identity resources
// WorkloadManager uses these to inject the public key into PicoD containers
const (
	// IdentitySecretName is the name of the Secret storing Router's keys
	IdentitySecretName = "picod-router-identity" //nolint:gosec // This is a name reference, not a credential
	// PublicKeyDataKey is the key in the Secret data map for the public key
	PublicKeyDataKey = "public.pem"
)

// IdentitySecretNamespace is the namespace where the identity secret is stored
// This is read from AGENTCUBE_NAMESPACE env var
var IdentitySecretNamespace = "default"

func init() {
	if ns := os.Getenv("AGENTCUBE_NAMESPACE"); ns != "" {
		IdentitySecretNamespace = ns
	}
}

// cachedPublicKey stores the public key loaded from Router's Secret
// This allows PicoD pods to be created in any namespace without cross-namespace Secret references
var (
	cachedPublicKey     string
	publicKeyCacheMutex sync.RWMutex
)

// GetCachedPublicKey returns the cached public key, or empty string if not loaded
func GetCachedPublicKey() string {
	publicKeyCacheMutex.RLock()
	defer publicKeyCacheMutex.RUnlock()
	return cachedPublicKey
}

// IsPublicKeyCached returns true if the public key has been successfully loaded
func IsPublicKeyCached() bool {
	publicKeyCacheMutex.RLock()
	defer publicKeyCacheMutex.RUnlock()
	return cachedPublicKey != ""
}

// InitPublicKeyCache starts a background goroutine that continuously tries to load
// the public key from Router's Secret until successful. This handles the case where
// Router hasn't started yet when WorkloadManager starts.
func InitPublicKeyCache(clientset kubernetes.Interface) {
	go func() {
		retryInterval := 5 * time.Second
		maxRetryInterval := 60 * time.Second

		for {
			if IsPublicKeyCached() {
				return
			}

			err := loadPublicKeyFromSecret(clientset)
			if err == nil {
				klog.Infof("Public key cached from Secret %s/%s", IdentitySecretNamespace, IdentitySecretName)
				return
			}

			klog.Warningf("Failed to load public key from Router Secret: %v. Retrying in %v...", err, retryInterval)
			time.Sleep(retryInterval)

			// Exponential backoff with max limit
			retryInterval = retryInterval * 2
			if retryInterval > maxRetryInterval {
				retryInterval = maxRetryInterval
			}
		}
	}()
}

// loadPublicKeyFromSecret reads the public key from Router's Secret
func loadPublicKeyFromSecret(clientset kubernetes.Interface) error {
	secret, err := clientset.CoreV1().Secrets(IdentitySecretNamespace).Get(
		context.Background(),
		IdentitySecretName,
		metav1.GetOptions{},
	)
	if err != nil {
		return fmt.Errorf("failed to get Router identity secret %s/%s: %w",
			IdentitySecretNamespace, IdentitySecretName, err)
	}

	publicKeyData, ok := secret.Data[PublicKeyDataKey]
	if !ok {
		return fmt.Errorf("public key not found in secret %s/%s (key: %s)",
			IdentitySecretNamespace, IdentitySecretName, PublicKeyDataKey)
	}

	publicKeyCacheMutex.Lock()
	cachedPublicKey = string(publicKeyData)
	publicKeyCacheMutex.Unlock()
	return nil
}

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

func buildSandboxByAgentRuntime(namespace string, name string, ifm *Informers) (*sandboxv1alpha1.Sandbox, *sandboxEntry, error) {
	agentRuntimeKey := namespace + "/" + name
	runtimeObj, exists, err := ifm.AgentRuntimeInformer.GetStore().GetByKey(agentRuntimeKey)
	if err != nil {
		return nil, nil, fmt.Errorf("get agent runtime %s from informer failed: %v", agentRuntimeKey, err)
	}
	if !exists {
		return nil, nil, fmt.Errorf("%w: %s", api.ErrAgentRuntimeNotFound, agentRuntimeKey)
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
	if agentRuntimeObj.Spec.SessionTimeout != nil {
		buildParams.idleTimeout = agentRuntimeObj.Spec.SessionTimeout.Duration
	}
	sandbox := buildSandboxObject(buildParams)
	entry := &sandboxEntry{
		Kind:      types.SandboxKind,
		Ports:     agentRuntimeObj.Spec.Ports,
		SessionID: sessionID,
	}
	return sandbox, entry, nil
}

func buildSandboxByCodeInterpreter(namespace string, codeInterpreterName string, informer *Informers) (*sandboxv1alpha1.Sandbox, *extensionsv1alpha1.SandboxClaim, *sandboxEntry, error) {
	codeInterpreterKey := namespace + "/" + codeInterpreterName
	runtimeObj, exists, err := informer.CodeInterpreterInformer.GetStore().GetByKey(codeInterpreterKey)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("get code interpreter %s from informer failed: %v", codeInterpreterKey, err)
	}

	if !exists {
		return nil, nil, nil, fmt.Errorf("%w: %s", api.ErrCodeInterpreterNotFound, codeInterpreterKey)
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

	// Check if public key is cached before creating pods that require it
	// Skip this check if authMode is "none" (custom images that don't use PicoD auth)
	if codeInterpreterObj.Spec.AuthMode != runtimev1alpha1.AuthModeNone && !IsPublicKeyCached() {
		return nil, nil, nil, fmt.Errorf("%w: cannot create PicoD pod", api.ErrPublicKeyMissing)
	}

	sessionID := uuid.New().String()
	sandboxName := fmt.Sprintf("%s-%s", codeInterpreterName, RandString(8))
	sandboxEntry := &sandboxEntry{
		Kind:      types.SandboxKind,
		Ports:     codeInterpreterObj.Spec.Ports,
		SessionID: sessionID,
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
		sandboxEntry.Kind = types.SandboxClaimsKind
		return simpleSandbox, sandboxClaim, sandboxEntry, nil
	}

	// Normalize RuntimeClassName: if it's an empty string, set it to nil
	runtimeClassName := codeInterpreterObj.Spec.Template.RuntimeClassName
	if runtimeClassName != nil && *runtimeClassName == "" {
		runtimeClassName = nil
	}

	// Build environment variables - create a copy to avoid mutating the informer cached object
	envVars := make([]corev1.EnvVar, len(codeInterpreterObj.Spec.Template.Environment))
	copy(envVars, codeInterpreterObj.Spec.Template.Environment)
	// Only inject public key for picod auth mode (default behavior)
	if codeInterpreterObj.Spec.AuthMode != runtimev1alpha1.AuthModeNone {
		envVars = append(envVars, corev1.EnvVar{
			Name:  "PICOD_AUTH_PUBLIC_KEY",
			Value: GetCachedPublicKey(),
		})
	}

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
		sessionID:      sessionID,
		podSpec:        podSpec,
		podLabels:      codeInterpreterObj.Spec.Template.Labels,
		podAnnotations: codeInterpreterObj.Spec.Template.Annotations,
	}
	if codeInterpreterObj.Spec.MaxSessionDuration != nil {
		buildParams.ttl = codeInterpreterObj.Spec.MaxSessionDuration.Duration
	}
	sandbox := buildSandboxObject(buildParams)
	return sandbox, nil, sandboxEntry, nil
}
