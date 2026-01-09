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
	runtimev1alpha1 "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

func TestConvertToPodTemplate_AuthMode(t *testing.T) {
	reconciler := &CodeInterpreterReconciler{}

	// Set up a mock public key for testing
	publicKeyCacheMutex.Lock()
	cachedPublicKey = "test-public-key-content"
	publicKeyCacheMutex.Unlock()
	defer func() {
		publicKeyCacheMutex.Lock()
		cachedPublicKey = ""
		publicKeyCacheMutex.Unlock()
	}()

	tests := []struct {
		name             string
		authMode         runtimev1alpha1.AuthModeType
		existingEnvVars  []corev1.EnvVar
		expectPublicKey  bool
		expectedEnvCount int
		description      string
	}{
		{
			name:             "picod mode should inject PICOD_AUTH_PUBLIC_KEY",
			authMode:         runtimev1alpha1.AuthModePicoD,
			existingEnvVars:  []corev1.EnvVar{},
			expectPublicKey:  true,
			expectedEnvCount: 1,
			description:      "When AuthMode is picod, PICOD_AUTH_PUBLIC_KEY should be injected",
		},
		{
			name:             "empty authMode (default) should inject PICOD_AUTH_PUBLIC_KEY",
			authMode:         "", // empty string, defaults to picod behavior
			existingEnvVars:  []corev1.EnvVar{},
			expectPublicKey:  true,
			expectedEnvCount: 1,
			description:      "When AuthMode is empty (default), PICOD_AUTH_PUBLIC_KEY should be injected",
		},
		{
			name:             "none mode should NOT inject PICOD_AUTH_PUBLIC_KEY",
			authMode:         runtimev1alpha1.AuthModeNone,
			existingEnvVars:  []corev1.EnvVar{},
			expectPublicKey:  false,
			expectedEnvCount: 0,
			description:      "When AuthMode is none, PICOD_AUTH_PUBLIC_KEY should NOT be injected",
		},
		{
			name:     "picod mode with existing env vars should preserve them and add public key",
			authMode: runtimev1alpha1.AuthModePicoD,
			existingEnvVars: []corev1.EnvVar{
				{Name: "EXISTING_VAR", Value: "existing_value"},
			},
			expectPublicKey:  true,
			expectedEnvCount: 2,
			description:      "Existing env vars should be preserved when adding public key",
		},
		{
			name:     "none mode with existing env vars should preserve them without adding public key",
			authMode: runtimev1alpha1.AuthModeNone,
			existingEnvVars: []corev1.EnvVar{
				{Name: "EXISTING_VAR", Value: "existing_value"},
			},
			expectPublicKey:  false,
			expectedEnvCount: 1,
			description:      "Existing env vars should be preserved when not adding public key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			template := &runtimev1alpha1.CodeInterpreterSandboxTemplate{
				Image:       "test-image:latest",
				Environment: tt.existingEnvVars,
			}

			ci := &runtimev1alpha1.CodeInterpreter{
				Spec: runtimev1alpha1.CodeInterpreterSpec{
					AuthMode: tt.authMode,
				},
			}

			result := reconciler.convertToPodTemplate(template, ci)

			// Check env var count
			envVars := result.Spec.Containers[0].Env
			assert.Equal(t, tt.expectedEnvCount, len(envVars), tt.description)

			// Check if PICOD_AUTH_PUBLIC_KEY exists
			hasPublicKey := false
			for _, env := range envVars {
				if env.Name == "PICOD_AUTH_PUBLIC_KEY" {
					hasPublicKey = true
					assert.Equal(t, "test-public-key-content", env.Value, "Public key value should match cached value")
				}
			}
			assert.Equal(t, tt.expectPublicKey, hasPublicKey, tt.description)
		})
	}
}

func TestConvertToPodTemplate_EnvVarSliceCopy(t *testing.T) {
	reconciler := &CodeInterpreterReconciler{}

	// Set up a mock public key for testing
	publicKeyCacheMutex.Lock()
	cachedPublicKey = "test-public-key"
	publicKeyCacheMutex.Unlock()
	defer func() {
		publicKeyCacheMutex.Lock()
		cachedPublicKey = ""
		publicKeyCacheMutex.Unlock()
	}()

	originalEnvVars := []corev1.EnvVar{
		{Name: "VAR1", Value: "value1"},
		{Name: "VAR2", Value: "value2"},
	}

	template := &runtimev1alpha1.CodeInterpreterSandboxTemplate{
		Image:       "test-image:latest",
		Environment: originalEnvVars,
	}

	ci := &runtimev1alpha1.CodeInterpreter{
		Spec: runtimev1alpha1.CodeInterpreterSpec{
			AuthMode: runtimev1alpha1.AuthModePicoD, // This will append to envVars
		},
	}

	// Call convertToPodTemplate
	_ = reconciler.convertToPodTemplate(template, ci)

	// Verify original slice was NOT modified
	assert.Equal(t, 2, len(template.Environment), "Original template.Environment should not be modified")
	assert.Equal(t, 2, len(originalEnvVars), "Original slice should not be modified")

	// Verify no PICOD_AUTH_PUBLIC_KEY was added to original
	for _, env := range template.Environment {
		assert.NotEqual(t, "PICOD_AUTH_PUBLIC_KEY", env.Name, "Original template should not contain injected key")
	}
}
