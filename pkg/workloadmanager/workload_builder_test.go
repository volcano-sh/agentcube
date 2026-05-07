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
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/ptr"

	runtimev1alpha1 "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
	"github.com/volcano-sh/agentcube/pkg/common/types"
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

// fakeInformerWithStore is a minimal cache.SharedIndexInformer whose store can be
// pre-populated with objects. Only GetStore() is expected to be called.
type fakeInformerWithStore struct {
	cache.SharedIndexInformer
	store cache.Store
}

func (f *fakeInformerWithStore) GetStore() cache.Store {
	return f.store
}

func toUnstructured(t *testing.T, obj interface{}) *unstructured.Unstructured {
	t.Helper()
	m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		t.Fatalf("failed to convert to unstructured: %v", err)
	}
	return &unstructured.Unstructured{Object: m}
}

func makeAgentRuntimeInformer(t *testing.T, ar *runtimev1alpha1.AgentRuntime) cache.SharedIndexInformer {
	t.Helper()
	store := cache.NewStore(cache.MetaNamespaceKeyFunc)
	if err := store.Add(toUnstructured(t, ar)); err != nil {
		t.Fatalf("failed to add agent runtime to store: %v", err)
	}
	return &fakeInformerWithStore{store: store}
}

func makeCodeInterpreterInformer(t *testing.T, ci *runtimev1alpha1.CodeInterpreter) cache.SharedIndexInformer {
	t.Helper()
	store := cache.NewStore(cache.MetaNamespaceKeyFunc)
	if err := store.Add(toUnstructured(t, ci)); err != nil {
		t.Fatalf("failed to add code interpreter to store: %v", err)
	}
	return &fakeInformerWithStore{store: store}
}

// TestBuildSandboxByAgentRuntime_MergesExtraEnvVars verifies that extraEnvVars
// are appended to the first container of the AgentRuntime template.
func TestBuildSandboxByAgentRuntime_MergesExtraEnvVars(t *testing.T) {
	ar := &runtimev1alpha1.AgentRuntime{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "runtime.agentcube.io/v1alpha1",
			Kind:       "AgentRuntime",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ar",
			Namespace: "default",
		},
		Spec: runtimev1alpha1.AgentRuntimeSpec{
			Template: &runtimev1alpha1.SandboxTemplate{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "agent",
							Image: "agent:latest",
							Env: []corev1.EnvVar{
								{Name: "EXISTING", Value: "old"},
							},
						},
					},
				},
			},
		},
	}

	ifm := &Informers{
		AgentRuntimeInformer: makeAgentRuntimeInformer(t, ar),
	}

	extraEnvVars := map[string]string{
		"NEW_VAR":  "new_value",
		"NEW_VAR2": "new_value2",
	}

	sandbox, entry, err := buildSandboxByAgentRuntime("default", "test-ar", ifm, extraEnvVars)
	if err != nil {
		t.Fatalf("buildSandboxByAgentRuntime failed: %v", err)
	}
	if entry == nil {
		t.Fatal("expected non-nil sandbox entry")
	}

	containers := sandbox.Spec.PodTemplate.Spec.Containers
	if len(containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(containers))
	}

	env := containers[0].Env
	if len(env) != 3 {
		t.Fatalf("expected 3 env vars, got %d: %v", len(env), env)
	}

	envMap := make(map[string]string, len(env))
	for _, e := range env {
		envMap[e.Name] = e.Value
	}

	if envMap["EXISTING"] != "old" {
		t.Errorf("expected EXISTING=old, got %q", envMap["EXISTING"])
	}
	if envMap["NEW_VAR"] != "new_value" {
		t.Errorf("expected NEW_VAR=new_value, got %q", envMap["NEW_VAR"])
	}
	if envMap["NEW_VAR2"] != "new_value2" {
		t.Errorf("expected NEW_VAR2=new_value2, got %q", envMap["NEW_VAR2"])
	}
}

// TestBuildSandboxByAgentRuntime_NoExtraEnvVars verifies that when extraEnvVars
// is nil/empty, the original container env is preserved unchanged.
func TestBuildSandboxByAgentRuntime_NoExtraEnvVars(t *testing.T) {
	ar := &runtimev1alpha1.AgentRuntime{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "runtime.agentcube.io/v1alpha1",
			Kind:       "AgentRuntime",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ar",
			Namespace: "default",
		},
		Spec: runtimev1alpha1.AgentRuntimeSpec{
			Template: &runtimev1alpha1.SandboxTemplate{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "agent",
							Image: "agent:latest",
							Env: []corev1.EnvVar{
								{Name: "EXISTING", Value: "old"},
							},
						},
					},
				},
			},
		},
	}

	ifm := &Informers{
		AgentRuntimeInformer: makeAgentRuntimeInformer(t, ar),
	}

	sandbox, _, err := buildSandboxByAgentRuntime("default", "test-ar", ifm, nil)
	if err != nil {
		t.Fatalf("buildSandboxByAgentRuntime failed: %v", err)
	}

	containers := sandbox.Spec.PodTemplate.Spec.Containers
	if len(containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(containers))
	}
	if len(containers[0].Env) != 1 {
		t.Fatalf("expected 1 env var, got %d", len(containers[0].Env))
	}
	if containers[0].Env[0].Name != "EXISTING" || containers[0].Env[0].Value != "old" {
		t.Errorf("expected EXISTING=old, got %s=%s", containers[0].Env[0].Name, containers[0].Env[0].Value)
	}
}

// TestBuildSandboxByCodeInterpreter_MergesExtraEnvVars verifies that extraEnvVars
// are merged into the code interpreter sandbox environment variables.
func TestBuildSandboxByCodeInterpreter_MergesExtraEnvVars(t *testing.T) {
	ci := &runtimev1alpha1.CodeInterpreter{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "runtime.agentcube.io/v1alpha1",
			Kind:       "CodeInterpreter",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ci",
			Namespace: "default",
		},
		Spec: runtimev1alpha1.CodeInterpreterSpec{
			AuthMode: runtimev1alpha1.AuthModeNone,
			Template: &runtimev1alpha1.CodeInterpreterSandboxTemplate{
				Image: "ci:latest",
				Environment: []corev1.EnvVar{
					{Name: "BASE", Value: "base_value"},
				},
			},
		},
	}

	ifm := &Informers{
		CodeInterpreterInformer: makeCodeInterpreterInformer(t, ci),
	}

	extraEnvVars := map[string]string{
		"EXTRA": "extra_value",
	}

	sandbox, claim, entry, err := buildSandboxByCodeInterpreter("default", "test-ci", ifm, extraEnvVars)
	if err != nil {
		t.Fatalf("buildSandboxByCodeInterpreter failed: %v", err)
	}
	if entry == nil {
		t.Fatal("expected non-nil sandbox entry")
	}
	if claim != nil {
		t.Fatal("expected nil claim for non-warm-pool code interpreter")
	}

	containers := sandbox.Spec.PodTemplate.Spec.Containers
	if len(containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(containers))
	}

	env := containers[0].Env
	if len(env) != 2 {
		t.Fatalf("expected 2 env vars, got %d: %v", len(env), env)
	}

	envMap := make(map[string]string, len(env))
	for _, e := range env {
		envMap[e.Name] = e.Value
	}

	if envMap["BASE"] != "base_value" {
		t.Errorf("expected BASE=base_value, got %q", envMap["BASE"])
	}
	if envMap["EXTRA"] != "extra_value" {
		t.Errorf("expected EXTRA=extra_value, got %q", envMap["EXTRA"])
	}
}

// TestBuildSandboxByCodeInterpreter_WarmPool_MarksExtraEnvVars verifies that
// when warm pool is enabled and extraEnvVars are provided, the sandbox gets
// an annotation marking the presence of extra env vars.
func TestBuildSandboxByCodeInterpreter_WarmPool_MarksExtraEnvVars(t *testing.T) {
	ci := &runtimev1alpha1.CodeInterpreter{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "runtime.agentcube.io/v1alpha1",
			Kind:       "CodeInterpreter",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ci",
			Namespace: "default",
		},
		Spec: runtimev1alpha1.CodeInterpreterSpec{
			AuthMode:     runtimev1alpha1.AuthModeNone,
			WarmPoolSize: ptr.To[int32](3),
			Template: &runtimev1alpha1.CodeInterpreterSandboxTemplate{
				Image: "ci:latest",
			},
		},
	}

	ifm := &Informers{
		CodeInterpreterInformer: makeCodeInterpreterInformer(t, ci),
	}

	extraEnvVars := map[string]string{
		"EXTRA": "extra_value",
	}

	sandbox, claim, entry, err := buildSandboxByCodeInterpreter("default", "test-ci", ifm, extraEnvVars)
	if err != nil {
		t.Fatalf("buildSandboxByCodeInterpreter failed: %v", err)
	}
	if entry == nil {
		t.Fatal("expected non-nil sandbox entry")
	}
	if claim == nil {
		t.Fatal("expected non-nil claim for warm pool code interpreter")
	}
	if entry.Kind != types.SandboxClaimsKind {
		t.Errorf("expected Kind=%s, got %s", types.SandboxClaimsKind, entry.Kind)
	}

	if sandbox.Annotations == nil {
		t.Fatal("expected annotations to be set")
	}
	if sandbox.Annotations["agentcube.io/extra-env-vars"] != "true" {
		t.Errorf("expected extra-env-vars annotation to be 'true', got %q", sandbox.Annotations["agentcube.io/extra-env-vars"])
	}
}

// TestBuildSandboxByCodeInterpreter_WarmPool_NoExtraEnvVars verifies that
// when warm pool is enabled but no extraEnvVars are provided, no annotation is set.
func TestBuildSandboxByCodeInterpreter_WarmPool_NoExtraEnvVars(t *testing.T) {
	ci := &runtimev1alpha1.CodeInterpreter{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "runtime.agentcube.io/v1alpha1",
			Kind:       "CodeInterpreter",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ci",
			Namespace: "default",
		},
		Spec: runtimev1alpha1.CodeInterpreterSpec{
			AuthMode:     runtimev1alpha1.AuthModeNone,
			WarmPoolSize: ptr.To[int32](3),
			Template: &runtimev1alpha1.CodeInterpreterSandboxTemplate{
				Image: "ci:latest",
			},
		},
	}

	ifm := &Informers{
		CodeInterpreterInformer: makeCodeInterpreterInformer(t, ci),
	}

	sandbox, _, _, err := buildSandboxByCodeInterpreter("default", "test-ci", ifm, nil)
	if err != nil {
		t.Fatalf("buildSandboxByCodeInterpreter failed: %v", err)
	}

	if sandbox.Annotations != nil && sandbox.Annotations["agentcube.io/extra-env-vars"] != "" {
		t.Errorf("expected no extra-env-vars annotation, got %q", sandbox.Annotations["agentcube.io/extra-env-vars"])
	}
}
