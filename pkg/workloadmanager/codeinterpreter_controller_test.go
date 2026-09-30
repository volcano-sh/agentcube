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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	runtimev1alpha1 "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
	extensionsv1alpha1 "sigs.k8s.io/agent-sandbox/extensions/api/v1alpha1"
)

func setupTestReconciler() *CodeInterpreterReconciler {
	scheme := runtime.NewScheme()
	_ = runtimev1alpha1.AddToScheme(scheme)
	_ = sandboxv1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	client := fake.NewClientBuilder().WithScheme(scheme).Build()

	return &CodeInterpreterReconciler{
		Client: client,
		Scheme: scheme,
	}
}

func newTestReconcilerWithObjects(objects ...runtime.Object) *CodeInterpreterReconciler {
	scheme := runtime.NewScheme()
	_ = runtimev1alpha1.AddToScheme(scheme)
	_ = sandboxv1alpha1.AddToScheme(scheme)
	_ = extensionsv1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	client := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objects...).Build()

	return &CodeInterpreterReconciler{
		Client: client,
		Scheme: scheme,
	}
}

func stringPtr(s string) *string {
	return &s
}

func testCodeInterpreterWithWarmPool() *runtimev1alpha1.CodeInterpreter {
	warmPoolSize := int32(2)
	return &runtimev1alpha1.CodeInterpreter{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-code-interpreter",
			Namespace: "default",
		},
		Spec: runtimev1alpha1.CodeInterpreterSpec{
			AuthMode:     runtimev1alpha1.AuthModeNone,
			WarmPoolSize: &warmPoolSize,
			Template: &runtimev1alpha1.CodeInterpreterSandboxTemplate{
				Image:           "picod:latest",
				ImagePullPolicy: corev1.PullIfNotPresent,
			},
		},
	}
}

func TestEnsureSandboxTemplateDisablesAgentSandboxDefaultNetworkPolicy(t *testing.T) {
	reconciler := newTestReconcilerWithObjects()
	ci := testCodeInterpreterWithWarmPool()

	_, err := reconciler.ensureSandboxTemplate(context.Background(), ci)
	assert.NoError(t, err)

	sandboxTemplate := &extensionsv1alpha1.SandboxTemplate{}
	err = reconciler.Get(context.Background(), types.NamespacedName{
		Name:      ci.Name,
		Namespace: ci.Namespace,
	}, sandboxTemplate)
	assert.NoError(t, err)
	assert.Equal(t, extensionsv1alpha1.NetworkPolicyManagementUnmanaged, sandboxTemplate.Spec.NetworkPolicyManagement)
}

func TestEnsureSandboxTemplateUpdatesManagedNetworkPolicyToUnmanaged(t *testing.T) {
	ci := testCodeInterpreterWithWarmPool()
	existing := &extensionsv1alpha1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ci.Name,
			Namespace: ci.Namespace,
		},
		Spec: extensionsv1alpha1.SandboxTemplateSpec{
			NetworkPolicyManagement: extensionsv1alpha1.NetworkPolicyManagementManaged,
			PodTemplate: sandboxv1alpha1.PodTemplate{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "codeinterpreter",
						Image: "stale-image",
					}},
				},
			},
		},
	}
	reconciler := newTestReconcilerWithObjects(existing)

	_, err := reconciler.ensureSandboxTemplate(context.Background(), ci)
	assert.NoError(t, err)

	sandboxTemplate := &extensionsv1alpha1.SandboxTemplate{}
	err = reconciler.Get(context.Background(), types.NamespacedName{
		Name:      ci.Name,
		Namespace: ci.Namespace,
	}, sandboxTemplate)
	assert.NoError(t, err)
	assert.Equal(t, extensionsv1alpha1.NetworkPolicyManagementUnmanaged, sandboxTemplate.Spec.NetworkPolicyManagement)
}

func TestConvertToPodTemplate_PodMetadata(t *testing.T) {
	reconciler := setupTestReconciler()

	tests := []struct {
		name                string
		labels              map[string]string
		annotations         map[string]string
		expectedLabels      map[string]string
		expectedAnnotations map[string]string
	}{
		{
			name:                "labels and annotations are copied to the pod template",
			labels:              map[string]string{"team": "data-science"},
			annotations:         map[string]string{"sidecar.istio.io/inject": "false"},
			expectedLabels:      map[string]string{"team": "data-science"},
			expectedAnnotations: map[string]string{"sidecar.istio.io/inject": "false"},
		},
		{
			name:                "nil metadata stays nil",
			labels:              nil,
			annotations:         nil,
			expectedLabels:      nil,
			expectedAnnotations: nil,
		},
		{
			name:                "empty metadata is normalized to nil",
			labels:              map[string]string{},
			annotations:         map[string]string{},
			expectedLabels:      nil,
			expectedAnnotations: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			template := &runtimev1alpha1.CodeInterpreterSandboxTemplate{
				Image:       "test-image:latest",
				Labels:      tt.labels,
				Annotations: tt.annotations,
			}
			ci := &runtimev1alpha1.CodeInterpreter{
				Spec: runtimev1alpha1.CodeInterpreterSpec{
					AuthMode: runtimev1alpha1.AuthModeNone,
				},
			}

			result := reconciler.convertToPodTemplate(template, ci)

			assert.Equal(t, tt.expectedLabels, result.ObjectMeta.Labels)
			assert.Equal(t, tt.expectedAnnotations, result.ObjectMeta.Annotations)
		})
	}
}

func TestConvertToPodTemplate_PodMetadataDoesNotMutateTemplate(t *testing.T) {
	reconciler := setupTestReconciler()
	template := &runtimev1alpha1.CodeInterpreterSandboxTemplate{
		Image:       "test-image:latest",
		Labels:      map[string]string{"team": "data-science"},
		Annotations: map[string]string{"sidecar.istio.io/inject": "false"},
	}
	ci := &runtimev1alpha1.CodeInterpreter{
		Spec: runtimev1alpha1.CodeInterpreterSpec{
			AuthMode: runtimev1alpha1.AuthModeNone,
		},
	}

	result := reconciler.convertToPodTemplate(template, ci)
	require.NotNil(t, result.ObjectMeta.Labels)
	require.NotNil(t, result.ObjectMeta.Annotations)
	result.ObjectMeta.Labels["extra"] = "value"
	result.ObjectMeta.Annotations["extra"] = "value"

	assert.Equal(t, map[string]string{"team": "data-science"}, template.Labels)
	assert.Equal(t, map[string]string{"sidecar.istio.io/inject": "false"}, template.Annotations)
}

func TestEnsureSandboxTemplate_PodMetadata(t *testing.T) {
	testCI := func() *runtimev1alpha1.CodeInterpreter {
		ci := testCodeInterpreterWithWarmPool()
		ci.Spec.Template.Environment = []corev1.EnvVar{{Name: "ENV1", Value: "value1"}}
		return ci
	}
	withMetadata := func(ci *runtimev1alpha1.CodeInterpreter) *runtimev1alpha1.CodeInterpreter {
		ci.Spec.Template.Labels = map[string]string{"team": "data-science"}
		ci.Spec.Template.Annotations = map[string]string{"sidecar.istio.io/inject": "false"}
		return ci
	}
	existingTemplate := func(ci *runtimev1alpha1.CodeInterpreter) *extensionsv1alpha1.SandboxTemplate {
		return &extensionsv1alpha1.SandboxTemplate{
			ObjectMeta: metav1.ObjectMeta{
				Name:      ci.Name,
				Namespace: ci.Namespace,
			},
			Spec: extensionsv1alpha1.SandboxTemplateSpec{
				NetworkPolicyManagement: extensionsv1alpha1.NetworkPolicyManagementUnmanaged,
				PodTemplate:             setupTestReconciler().convertToPodTemplate(ci.Spec.Template, ci),
			},
		}
	}

	tests := []struct {
		name                string
		ci                  *runtimev1alpha1.CodeInterpreter
		existing            *extensionsv1alpha1.SandboxTemplate
		expectedLabels      map[string]string
		expectedAnnotations map[string]string
		expectUpdate        bool
	}{
		{
			name:                "new template carries pod metadata",
			ci:                  withMetadata(testCI()),
			existing:            nil,
			expectedLabels:      map[string]string{"team": "data-science"},
			expectedAnnotations: map[string]string{"sidecar.istio.io/inject": "false"},
		},
		{
			name:                "existing template is updated when only pod metadata changed",
			ci:                  withMetadata(testCI()),
			existing:            existingTemplate(testCI()),
			expectedLabels:      map[string]string{"team": "data-science"},
			expectedAnnotations: map[string]string{"sidecar.istio.io/inject": "false"},
			expectUpdate:        true,
		},
		{
			name:                "existing template without pod metadata is left unchanged",
			ci:                  testCI(),
			existing:            existingTemplate(testCI()),
			expectedLabels:      nil,
			expectedAnnotations: nil,
			expectUpdate:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var reconciler *CodeInterpreterReconciler
			if tt.existing != nil {
				reconciler = newTestReconcilerWithObjects(tt.existing)
			} else {
				reconciler = newTestReconcilerWithObjects()
			}
			key := types.NamespacedName{Name: tt.ci.Name, Namespace: tt.ci.Namespace}

			before := &extensionsv1alpha1.SandboxTemplate{}
			if tt.existing != nil {
				assert.NoError(t, reconciler.Get(context.Background(), key, before))
			}

			_, err := reconciler.ensureSandboxTemplate(context.Background(), tt.ci)
			assert.NoError(t, err)

			sandboxTemplate := &extensionsv1alpha1.SandboxTemplate{}
			assert.NoError(t, reconciler.Get(context.Background(), key, sandboxTemplate))
			assert.Equal(t, tt.expectedLabels, sandboxTemplate.Spec.PodTemplate.ObjectMeta.Labels)
			assert.Equal(t, tt.expectedAnnotations, sandboxTemplate.Spec.PodTemplate.ObjectMeta.Annotations)
			if tt.existing != nil {
				assert.Equal(t, tt.expectUpdate, before.ResourceVersion != sandboxTemplate.ResourceVersion,
					"unexpected SandboxTemplate update")
			}
		})
	}
}

func TestConvertToPodTemplate_RuntimeClassName_TableDriven(t *testing.T) {
	reconciler := setupTestReconciler()

	tests := []struct {
		name                 string
		runtimeClassName     *string
		expectedRuntimeClass *string
	}{
		{
			name:                 "empty string should be normalized to nil",
			runtimeClassName:     stringPtr(""),
			expectedRuntimeClass: nil,
		},
		{
			name:                 "nil should remain nil",
			runtimeClassName:     nil,
			expectedRuntimeClass: nil,
		},
		{
			name:                 "valid runtime class preserved",
			runtimeClassName:     stringPtr("gvisor"),
			expectedRuntimeClass: stringPtr("gvisor"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			template := &runtimev1alpha1.CodeInterpreterSandboxTemplate{
				Image:            "test-image:latest",
				ImagePullPolicy:  corev1.PullIfNotPresent,
				RuntimeClassName: tt.runtimeClassName,
			}

			ci := &runtimev1alpha1.CodeInterpreter{
				Spec: runtimev1alpha1.CodeInterpreterSpec{
					AuthMode: runtimev1alpha1.AuthModePicoD,
				},
			}

			result := reconciler.convertToPodTemplate(template, ci)

			if tt.expectedRuntimeClass == nil {
				assert.Nil(t, result.Spec.RuntimeClassName)
			} else {
				if assert.NotNil(t, result.Spec.RuntimeClassName) {
					assert.Equal(t, *tt.expectedRuntimeClass, *result.Spec.RuntimeClassName)
				}
			}
		})
	}
}

// Note: TestConvertToPodTemplate_AllFields removed - it only verified that
// struct fields match what was set in the template, which is trivial field copying.
// The meaningful behavior (normalization, auth mode handling) is tested in other tests.

func TestConvertToPodTemplate_AuthMode(t *testing.T) {
	reconciler := setupTestReconciler()

	tests := []struct {
		name               string
		authMode           runtimev1alpha1.AuthModeType
		environment        []corev1.EnvVar
		expectedEnvLen     int
		expectExactEnvLen  bool
		expectPublicKeyVar bool
	}{
		{
			name:               "auth mode none - no public key injected",
			authMode:           runtimev1alpha1.AuthModeNone,
			environment:        []corev1.EnvVar{{Name: "ENV1", Value: "value1"}},
			expectedEnvLen:     1,
			expectExactEnvLen:  true,
			expectPublicKeyVar: false,
		},
		{
			name:               "auth mode PicoD - inject public key and preserve existing env",
			authMode:           runtimev1alpha1.AuthModePicoD,
			environment:        []corev1.EnvVar{{Name: "ENV1", Value: "value1"}},
			expectedEnvLen:     2, // at least original + public key
			expectExactEnvLen:  false,
			expectPublicKeyVar: true,
		},
		{
			name:               "auth mode PicoD - only public key when no environment variables",
			authMode:           runtimev1alpha1.AuthModePicoD,
			environment:        []corev1.EnvVar{},
			expectedEnvLen:     1,
			expectExactEnvLen:  true,
			expectPublicKeyVar: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			template := &runtimev1alpha1.CodeInterpreterSandboxTemplate{
				Image:           "test-image:latest",
				ImagePullPolicy: corev1.PullIfNotPresent,
				Environment:     tt.environment,
			}

			ci := &runtimev1alpha1.CodeInterpreter{
				Spec: runtimev1alpha1.CodeInterpreterSpec{
					AuthMode: tt.authMode,
				},
			}

			result := reconciler.convertToPodTemplate(template, ci)

			envVars := result.Spec.Containers[0].Env
			if tt.expectExactEnvLen {
				assert.Equal(t, tt.expectedEnvLen, len(envVars))
			} else {
				assert.GreaterOrEqual(t, len(envVars), tt.expectedEnvLen)
			}

			foundPublicKey := false
			for _, env := range envVars {
				if env.Name == "PICOD_AUTH_PUBLIC_KEY" {
					foundPublicKey = true
					break
				}
			}

			if tt.expectPublicKeyVar {
				assert.True(t, foundPublicKey, "PICOD_AUTH_PUBLIC_KEY should be injected")
			} else {
				assert.False(t, foundPublicKey, "PICOD_AUTH_PUBLIC_KEY should not be injected")
			}
		})
	}
}

// Note: TestConvertToPodTemplate_EmptyCommandAndArgs and
// TestConvertToPodTemplate_NilCommandAndArgs removed - they only verified that
// empty/nil values are preserved, which is trivial field copying behavior.
