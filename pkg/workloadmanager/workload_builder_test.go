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
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/volcano-sh/agentcube/pkg/api"
	runtimev1alpha1 "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
	"github.com/volcano-sh/agentcube/pkg/common/types"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/ptr"
)

// TestBuildSandboxObject_DoesNotMutateCallerLabels verifies that buildSandboxObject
// does not write session-specific labels back into the caller's map, which would
// corrupt the informer-cached CRD object shared across goroutines.
func TestBuildSandboxObject_DoesNotMutateCallerLabels(t *testing.T) {
	original := map[string]string{
		"app": "my-app",
		"env": "test",
	}
	// Take a snapshot before the call.
	before := make(map[string]string, len(original))
	for k, v := range original {
		before[k] = v
	}

	params := &buildSandboxParams{
		namespace:   "default",
		sandboxName: "sandbox-abc",
		sessionID:   "session-123",
		ttl:         DefaultSandboxTTL,
		idleTimeout: DefaultSandboxIdleTimeout,
		podLabels:   original,
	}

	sandbox := buildSandboxObject(params)

	// The caller's map must be unchanged.
	if len(original) != len(before) {
		t.Fatalf("caller labels map was mutated: before=%v after=%v", before, original)
	}
	for k, v := range before {
		if original[k] != v {
			t.Fatalf("caller labels map was mutated: key %q changed from %q to %q", k, v, original[k])
		}
	}

	// The sandbox pod-template labels must contain both original and injected keys.
	podLabels := sandbox.Spec.PodTemplate.ObjectMeta.Labels
	if podLabels["app"] != "my-app" {
		t.Errorf("expected pod label app=my-app, got %q", podLabels["app"])
	}
	if podLabels[SessionIdLabelKey] != "session-123" {
		t.Errorf("expected pod label %s=session-123, got %q", SessionIdLabelKey, podLabels[SessionIdLabelKey])
	}
	if podLabels[SandboxNameLabelKey] != "sandbox-abc" {
		t.Errorf("expected pod label %s=sandbox-abc, got %q", SandboxNameLabelKey, podLabels[SandboxNameLabelKey])
	}

	// Mutating the returned sandbox labels must not affect the caller's map.
	podLabels["app"] = "mutated-app"
	delete(podLabels, "env")

	if original["app"] != before["app"] {
		t.Fatalf("caller labels map was aliased through sandbox labels: key %q changed from %q to %q", "app", before["app"], original["app"])
	}
	if original["env"] != before["env"] {
		t.Fatalf("caller labels map was aliased through sandbox labels: key %q changed from %q to %q", "env", before["env"], original["env"])
	}
}

// TestBuildSandboxObject_NilLabels verifies that a nil podLabels input still
// produces a sandbox with the injected session labels.
func TestBuildSandboxObject_NilLabels(t *testing.T) {
	params := &buildSandboxParams{
		namespace:   "default",
		sandboxName: "sandbox-xyz",
		sessionID:   "session-456",
		ttl:         time.Hour,
		idleTimeout: 15 * time.Minute,
		podLabels:   nil,
	}

	sandbox := buildSandboxObject(params)

	podLabels := sandbox.Spec.PodTemplate.ObjectMeta.Labels
	if podLabels[SessionIdLabelKey] != "session-456" {
		t.Errorf("expected %s=session-456, got %q", SessionIdLabelKey, podLabels[SessionIdLabelKey])
	}
	if podLabels[SandboxNameLabelKey] != "sandbox-xyz" {
		t.Errorf("expected %s=sandbox-xyz, got %q", SandboxNameLabelKey, podLabels[SandboxNameLabelKey])
	}
}

// TestBuildSandboxObject_WorkloadNameLabel verifies that the WorkloadNameLabelKey
// label is correctly set on the Sandbox object metadata from the workloadName param.
// This covers the bug where CodeInterpreter sandboxes were missing this label.
func TestBuildSandboxObject_WorkloadNameLabel(t *testing.T) {
	tests := []struct {
		name         string
		workloadName string
		wantLabel    string
	}{
		{
			name:         "workloadName is set",
			workloadName: "my-code-interpreter",
			wantLabel:    "my-code-interpreter",
		},
		{
			name:         "workloadName is empty",
			workloadName: "",
			wantLabel:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := &buildSandboxParams{
				namespace:    "default",
				workloadName: tt.workloadName,
				sandboxName:  "sandbox-wl-test",
				sessionID:    "session-wl-test",
				ttl:          time.Hour,
				idleTimeout:  15 * time.Minute,
			}
			sandbox := buildSandboxObject(params)

			got := sandbox.ObjectMeta.Labels[WorkloadNameLabelKey]
			if got != tt.wantLabel {
				t.Errorf("expected %s=%q, got %q", WorkloadNameLabelKey, tt.wantLabel, got)
			}
		})
	}
}

// TestBuildSandboxClaimObject verifies that buildSandboxClaimObject correctly
// populates all fields including labels, annotations, and owner references.
func TestBuildSandboxClaimObject(t *testing.T) {
	t.Run("with owner reference", func(t *testing.T) {
		ownerRef := &metav1.OwnerReference{
			APIVersion: "runtime.agentcube.volcano.sh/v1alpha1",
			Kind:       "CodeInterpreter",
			Name:       "my-ci",
			UID:        "test-uid-123",
		}
		params := &buildSandboxClaimParams{
			namespace:           "test-ns",
			name:                "claim-abc",
			sandboxTemplateName: "my-ci",
			sessionID:           "session-claim-test",
			idleTimeout:         10 * time.Minute,
			ownerReference:      ownerRef,
		}
		claim := buildSandboxClaimObject(params)

		if claim.Namespace != "test-ns" {
			t.Errorf("expected namespace test-ns, got %q", claim.Namespace)
		}
		if claim.Name != "claim-abc" {
			t.Errorf("expected name claim-abc, got %q", claim.Name)
		}
		if claim.Spec.TemplateRef.Name != "my-ci" {
			t.Errorf("expected templateRef name my-ci, got %q", claim.Spec.TemplateRef.Name)
		}
		if claim.Labels[SessionIdLabelKey] != "session-claim-test" {
			t.Errorf("expected label %s=session-claim-test, got %q", SessionIdLabelKey, claim.Labels[SessionIdLabelKey])
		}
		if claim.Annotations[IdleTimeoutAnnotationKey] != "10m0s" {
			t.Errorf("expected annotation %s=10m0s, got %q", IdleTimeoutAnnotationKey, claim.Annotations[IdleTimeoutAnnotationKey])
		}
		if len(claim.OwnerReferences) != 1 {
			t.Fatalf("expected 1 owner reference, got %d", len(claim.OwnerReferences))
		}
		if claim.OwnerReferences[0].Name != "my-ci" {
			t.Errorf("expected owner ref name my-ci, got %q", claim.OwnerReferences[0].Name)
		}
	})

	t.Run("without owner reference", func(t *testing.T) {
		params := &buildSandboxClaimParams{
			namespace:           "default",
			name:                "claim-no-owner",
			sandboxTemplateName: "template-1",
			sessionID:           "session-no-owner",
			idleTimeout:         0,
		}
		claim := buildSandboxClaimObject(params)

		if len(claim.OwnerReferences) != 0 {
			t.Errorf("expected 0 owner references, got %d", len(claim.OwnerReferences))
		}
		if claim.Annotations[IdleTimeoutAnnotationKey] != DefaultSandboxIdleTimeout.String() {
			t.Errorf("expected default idle timeout annotation %s, got %q",
				DefaultSandboxIdleTimeout.String(), claim.Annotations[IdleTimeoutAnnotationKey])
		}
	})
}

// fakeInformer wraps a cache.Store to satisfy cache.SharedIndexInformer (partially) for testing.
type fakeInformer struct {
	cache.SharedIndexInformer
	store cache.Store
}

const (
	testNamespace               = "default"
	testAgentRuntimeName        = "test-runtime"
	testCodeInterpreterWarmPool = "ci-with-wp"
	testCodeInterpreterUID      = "ci-uid-123"
)

func (f *fakeInformer) GetStore() cache.Store {
	return f.store
}

func toUnstructured(t *testing.T, obj interface{}, kind string) *unstructured.Unstructured {
	t.Helper()
	data, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		t.Fatalf("failed to convert to unstructured: %v", err)
	}
	u := &unstructured.Unstructured{Object: data}
	u.SetAPIVersion("runtime.agentcube.volcano.sh/v1alpha1")
	u.SetKind(kind)
	return u
}

func setCachedPublicKeyForTest(t *testing.T, value string) {
	t.Helper()
	publicKeyCacheMutex.Lock()
	previous := cachedPublicKey
	cachedPublicKey = value
	publicKeyCacheMutex.Unlock()

	t.Cleanup(func() {
		publicKeyCacheMutex.Lock()
		cachedPublicKey = previous
		publicKeyCacheMutex.Unlock()
	})
}

func addRuntimeObjectToStore(t *testing.T, store cache.Store, obj interface{}, kind string) {
	t.Helper()
	u := toUnstructured(t, obj, kind)
	if err := store.Add(u); err != nil {
		t.Fatalf("failed to add to store: %v", err)
	}
}

func assertSandboxMetadata(t *testing.T, sandboxLabels map[string]string, sandboxName, namespace, namePrefix, workloadName, sessionID string) {
	t.Helper()
	if !strings.HasPrefix(sandboxName, namePrefix) {
		t.Errorf("expected sandbox name to start with %q, got %q", namePrefix, sandboxName)
	}
	if namespace != testNamespace {
		t.Errorf("expected namespace %q, got %q", testNamespace, namespace)
	}
	if workloadName != "" {
		if sandboxLabels[WorkloadNameLabelKey] != workloadName {
			t.Errorf("expected workload name label %q, got %q", workloadName, sandboxLabels[WorkloadNameLabelKey])
		}
	} else {
		if v, ok := sandboxLabels[WorkloadNameLabelKey]; ok && v != "" {
			t.Errorf("expected no workload name label, got %q", v)
		}
	}
	if sandboxLabels[SessionIdLabelKey] != sessionID {
		t.Errorf("expected sandbox label %s=%q, got %q", SessionIdLabelKey, sessionID, sandboxLabels[SessionIdLabelKey])
	}
}

func assertAgentRuntimePodLabels(t *testing.T, podLabels map[string]string, sandboxName, sessionID string) {
	t.Helper()
	if podLabels["app"] != "my-agent" {
		t.Errorf("expected pod label app=my-agent, got %q", podLabels["app"])
	}
	if podLabels[SessionIdLabelKey] != sessionID {
		t.Errorf("expected session id label %q, got %q", sessionID, podLabels[SessionIdLabelKey])
	}
	if podLabels[SandboxNameLabelKey] != sandboxName {
		t.Errorf("expected sandbox name label %q, got %q", sandboxName, podLabels[SandboxNameLabelKey])
	}
}

func assertOwnerReference(t *testing.T, owner metav1.OwnerReference) {
	t.Helper()
	if owner.Name != testCodeInterpreterWarmPool {
		t.Errorf("expected owner name %q, got %q", testCodeInterpreterWarmPool, owner.Name)
	}
	if owner.UID != testCodeInterpreterUID {
		t.Errorf("expected owner UID %q, got %q", testCodeInterpreterUID, owner.UID)
	}
	if owner.APIVersion != "runtime.agentcube.volcano.sh/v1alpha1" {
		t.Errorf("expected owner APIVersion runtime.agentcube.volcano.sh/v1alpha1, got %q", owner.APIVersion)
	}
	if owner.Kind != "CodeInterpreter" {
		t.Errorf("expected owner kind CodeInterpreter, got %q", owner.Kind)
	}
}

func TestBuildCodeInterpreterEnvVars(t *testing.T) {
	setCachedPublicKeyForTest(t, "test-public-key")

	tests := []struct {
		name        string
		templateEnv []corev1.EnvVar
		authMode    runtimev1alpha1.AuthModeType
		expected    []corev1.EnvVar
	}{
		{
			name: "authMode none does not inject key",
			templateEnv: []corev1.EnvVar{
				{Name: "FOO", Value: "bar"},
			},
			authMode: runtimev1alpha1.AuthModeNone,
			expected: []corev1.EnvVar{
				{Name: "FOO", Value: "bar"},
			},
		},
		{
			name: "authMode picod injects public key",
			templateEnv: []corev1.EnvVar{
				{Name: "FOO", Value: "bar"},
			},
			authMode: runtimev1alpha1.AuthModePicoD,
			expected: []corev1.EnvVar{
				{Name: "FOO", Value: "bar"},
				{Name: "PICOD_AUTH_PUBLIC_KEY", Value: "test-public-key"},
			},
		},
		{
			name:        "nil template env with picod injects public key",
			templateEnv: nil,
			authMode:    runtimev1alpha1.AuthModePicoD,
			expected: []corev1.EnvVar{
				{Name: "PICOD_AUTH_PUBLIC_KEY", Value: "test-public-key"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildCodeInterpreterEnvVars(tt.templateEnv, tt.authMode)
			if len(result) != len(tt.expected) {
				t.Fatalf("expected len %d, got %d", len(tt.expected), len(result))
			}
			for i := range result {
				if result[i].Name != tt.expected[i].Name || result[i].Value != tt.expected[i].Value {
					t.Errorf("at index %d: expected %+v, got %+v", i, tt.expected[i], result[i])
				}
			}
		})
	}
}

func TestBuildSandboxByAgentRuntime_NotFound(t *testing.T) {
	store := cache.NewStore(cache.MetaNamespaceKeyFunc)
	informer := &fakeInformer{store: store}
	ifm := &Informers{
		AgentRuntimeInformer: informer,
	}

	_, _, err := buildSandboxByAgentRuntime(testNamespace, "missing", ifm)
	if !errors.Is(err, api.ErrAgentRuntimeNotFound) {
		t.Fatalf("expected error %v, got %v", api.ErrAgentRuntimeNotFound, err)
	}
}

func TestBuildSandboxByAgentRuntime_Success(t *testing.T) {
	agentRuntime := &runtimev1alpha1.AgentRuntime{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "runtime.agentcube.volcano.sh/v1alpha1",
			Kind:       "AgentRuntime",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      testAgentRuntimeName,
			Namespace: testNamespace,
		},
		Spec: runtimev1alpha1.AgentRuntimeSpec{
			Ports: []runtimev1alpha1.TargetPort{
				{Port: 8080, Protocol: runtimev1alpha1.ProtocolTypeHTTP, PathPrefix: "/"},
			},
			Template: &runtimev1alpha1.SandboxTemplate{
				Labels: map[string]string{"app": "my-agent"},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "agent", Image: "my-agent-image:latest"},
					},
				},
			},
			MaxSessionDuration: &metav1.Duration{Duration: 2 * time.Hour},
			SessionTimeout:     &metav1.Duration{Duration: 30 * time.Minute},
		},
	}

	store := cache.NewStore(cache.MetaNamespaceKeyFunc)
	addRuntimeObjectToStore(t, store, agentRuntime, "AgentRuntime")

	informer := &fakeInformer{store: store}
	ifm := &Informers{
		AgentRuntimeInformer: informer,
	}

	sandbox, entry, err := buildSandboxByAgentRuntime(testNamespace, testAgentRuntimeName, ifm)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if sandbox == nil {
		t.Fatal("expected sandbox not to be nil")
	}
	if entry == nil {
		t.Fatal("expected entry not to be nil")
	}

	assertSandboxMetadata(t, sandbox.Labels, sandbox.Name, sandbox.Namespace, testAgentRuntimeName+"-", testAgentRuntimeName, entry.SessionID)
	assertAgentRuntimePodLabels(t, sandbox.Spec.PodTemplate.ObjectMeta.Labels, sandbox.Name, entry.SessionID)

	// Validate TTL (MaxSessionDuration)
	if sandbox.Spec.Lifecycle.ShutdownTime == nil {
		t.Error("expected shutdown time to be set")
	}

	// Validate Entry
	if entry.Kind != types.SandboxKind {
		t.Errorf("expected entry kind %q, got %q", types.SandboxKind, entry.Kind)
	}
	if entry.IdleTimeout != 30*time.Minute {
		t.Errorf("expected idle timeout 30m, got %v", entry.IdleTimeout)
	}
	if len(entry.Ports) != 1 || entry.Ports[0].Port != 8080 {
		t.Errorf("expected 1 port with number 8080, got %v", entry.Ports)
	}
}

func TestBuildSandboxByCodeInterpreter_NotFound(t *testing.T) {
	store := cache.NewStore(cache.MetaNamespaceKeyFunc)
	informer := &fakeInformer{store: store}
	ifm := &Informers{
		CodeInterpreterInformer: informer,
	}

	_, _, _, err := buildSandboxByCodeInterpreter(testNamespace, "missing", ifm)
	if !errors.Is(err, api.ErrCodeInterpreterNotFound) {
		t.Fatalf("expected error %v, got %v", api.ErrCodeInterpreterNotFound, err)
	}
}

func TestBuildSandboxByCodeInterpreter_PicodAuthFailsWithoutKey(t *testing.T) {
	codeInterpreter := &runtimev1alpha1.CodeInterpreter{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "runtime.agentcube.volcano.sh/v1alpha1",
			Kind:       "CodeInterpreter",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ci-picod-no-key",
			Namespace: testNamespace,
		},
		Spec: runtimev1alpha1.CodeInterpreterSpec{
			AuthMode: runtimev1alpha1.AuthModePicoD,
			Template: &runtimev1alpha1.CodeInterpreterSandboxTemplate{
				Image: "my-ci-image:latest",
			},
		},
	}

	setCachedPublicKeyForTest(t, "") // Empty key to trigger error

	store := cache.NewStore(cache.MetaNamespaceKeyFunc)
	addRuntimeObjectToStore(t, store, codeInterpreter, "CodeInterpreter")

	informer := &fakeInformer{store: store}
	ifm := &Informers{
		CodeInterpreterInformer: informer,
	}

	_, _, _, err := buildSandboxByCodeInterpreter(testNamespace, "ci-picod-no-key", ifm)
	if !errors.Is(err, api.ErrPublicKeyMissing) {
		t.Fatalf("expected error %v, got %v", api.ErrPublicKeyMissing, err)
	}
}

func TestBuildSandboxByCodeInterpreter_SuccessNoWarmPool(t *testing.T) {
	codeInterpreter := &runtimev1alpha1.CodeInterpreter{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "runtime.agentcube.volcano.sh/v1alpha1",
			Kind:       "CodeInterpreter",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ci-no-wp",
			Namespace: testNamespace,
		},
		Spec: runtimev1alpha1.CodeInterpreterSpec{
			AuthMode: runtimev1alpha1.AuthModeNone,
			Template: &runtimev1alpha1.CodeInterpreterSandboxTemplate{
				Image: "my-ci-image:latest",
			},
			MaxSessionDuration: &metav1.Duration{Duration: 4 * time.Hour},
			SessionTimeout:     &metav1.Duration{Duration: 10 * time.Minute},
		},
	}

	store := cache.NewStore(cache.MetaNamespaceKeyFunc)
	addRuntimeObjectToStore(t, store, codeInterpreter, "CodeInterpreter")

	informer := &fakeInformer{store: store}
	ifm := &Informers{
		CodeInterpreterInformer: informer,
	}

	sandbox, claim, entry, err := buildSandboxByCodeInterpreter(testNamespace, "ci-no-wp", ifm)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if sandbox == nil {
		t.Fatal("expected sandbox not to be nil")
	}
	if entry == nil {
		t.Fatal("expected entry not to be nil")
	}
	if claim != nil {
		t.Fatal("expected claim to be nil for non-warm pool path")
	}

	if !strings.HasPrefix(sandbox.Name, "ci-no-wp-") {
		t.Errorf("expected sandbox name to start with 'ci-no-wp-', got %q", sandbox.Name)
	}
	if entry.Kind != types.SandboxKind {
		t.Errorf("expected entry.Kind to be %q, got %q", types.SandboxKind, entry.Kind)
	}
	podSpec := sandbox.Spec.PodTemplate.Spec
	if len(podSpec.Containers) != 1 || podSpec.Containers[0].Image != "my-ci-image:latest" {
		t.Errorf("expected pod container image 'my-ci-image:latest', got %+v", podSpec.Containers)
	}
}

func TestBuildSandboxByCodeInterpreter_SuccessWithWarmPool(t *testing.T) {
	codeInterpreter := &runtimev1alpha1.CodeInterpreter{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "runtime.agentcube.volcano.sh/v1alpha1",
			Kind:       "CodeInterpreter",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      testCodeInterpreterWarmPool,
			Namespace: testNamespace,
			UID:       testCodeInterpreterUID,
		},
		Spec: runtimev1alpha1.CodeInterpreterSpec{
			AuthMode:     runtimev1alpha1.AuthModeNone,
			WarmPoolSize: ptr.To(int32(5)),
			Template: &runtimev1alpha1.CodeInterpreterSandboxTemplate{
				Image: "my-ci-image:latest",
			},
			MaxSessionDuration: &metav1.Duration{Duration: 4 * time.Hour},
			SessionTimeout:     &metav1.Duration{Duration: 10 * time.Minute},
		},
	}

	store := cache.NewStore(cache.MetaNamespaceKeyFunc)
	addRuntimeObjectToStore(t, store, codeInterpreter, "CodeInterpreter")

	informer := &fakeInformer{store: store}
	ifm := &Informers{
		CodeInterpreterInformer: informer,
	}

	sandbox, claim, entry, err := buildSandboxByCodeInterpreter(testNamespace, testCodeInterpreterWarmPool, ifm)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if sandbox == nil {
		t.Fatal("expected sandbox not to be nil")
	}
	if entry == nil {
		t.Fatal("expected entry not to be nil")
	}
	if claim == nil {
		t.Fatal("expected claim not to be nil for warm pool path")
	}

	assertSandboxMetadata(t, sandbox.Labels, sandbox.Name, sandbox.Namespace, testCodeInterpreterWarmPool+"-", "", entry.SessionID)
	if entry.Kind != types.SandboxClaimsKind {
		t.Errorf("expected entry.Kind to be %q, got %q", types.SandboxClaimsKind, entry.Kind)
	}
	if claim.Spec.TemplateRef.Name != testCodeInterpreterWarmPool {
		t.Errorf("expected templateRef name %q, got %q", testCodeInterpreterWarmPool, claim.Spec.TemplateRef.Name)
	}
	if len(claim.OwnerReferences) != 1 {
		t.Fatalf("expected 1 owner reference, got %d", len(claim.OwnerReferences))
	}
	assertOwnerReference(t, claim.OwnerReferences[0])
}
