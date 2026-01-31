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

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	runtimev1alpha1 "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
)

func setupTestReconciler() *CodeInterpreterReconciler {
	scheme := runtime.NewScheme()
	_ = runtimev1alpha1.AddToScheme(scheme)
	_ = sandboxv1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	// Create a minimal manager for testing
	cfg := &rest.Config{
		Host: "https://test",
	}
	mgr, _ := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme,
	})

	return &CodeInterpreterReconciler{
		Client: client,
		Scheme: scheme,
		mgr:    mgr,
	}
}

func stringPtr(s string) *string {
	return &s
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
