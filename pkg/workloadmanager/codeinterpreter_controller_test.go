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
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	runtimev1alpha1 "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
)

func setupTestReconciler() *CodeInterpreterReconciler {
	scheme := runtime.NewScheme()
	runtimev1alpha1.AddToScheme(scheme)
	sandboxv1alpha1.AddToScheme(scheme)
	corev1.AddToScheme(scheme)

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

func TestConvertToPodTemplate_EmptyRuntimeClassName(t *testing.T) {
	reconciler := setupTestReconciler()

	emptyStr := ""
	template := &runtimev1alpha1.CodeInterpreterSandboxTemplate{
		Image:           "test-image:latest",
		ImagePullPolicy: corev1.PullIfNotPresent,
		RuntimeClassName: &emptyStr,
	}

	ci := &runtimev1alpha1.CodeInterpreter{
		Spec: runtimev1alpha1.CodeInterpreterSpec{
			AuthMode: runtimev1alpha1.AuthModePicoD,
		},
	}

	result := reconciler.convertToPodTemplate(template, ci)

	// Empty string RuntimeClassName should be normalized to nil
	assert.Nil(t, result.Spec.RuntimeClassName)
}

func TestConvertToPodTemplate_NilRuntimeClassName(t *testing.T) {
	reconciler := setupTestReconciler()

	template := &runtimev1alpha1.CodeInterpreterSandboxTemplate{
		Image:           "test-image:latest",
		ImagePullPolicy: corev1.PullIfNotPresent,
		RuntimeClassName: nil,
	}

	ci := &runtimev1alpha1.CodeInterpreter{
		Spec: runtimev1alpha1.CodeInterpreterSpec{
			AuthMode: runtimev1alpha1.AuthModePicoD,
		},
	}

	result := reconciler.convertToPodTemplate(template, ci)

	// Nil RuntimeClassName should remain nil
	assert.Nil(t, result.Spec.RuntimeClassName)
}

func TestConvertToPodTemplate_ValidRuntimeClassName(t *testing.T) {
	reconciler := setupTestReconciler()

	runtimeClass := "gvisor"
	template := &runtimev1alpha1.CodeInterpreterSandboxTemplate{
		Image:           "test-image:latest",
		ImagePullPolicy: corev1.PullIfNotPresent,
		RuntimeClassName: &runtimeClass,
	}

	ci := &runtimev1alpha1.CodeInterpreter{
		Spec: runtimev1alpha1.CodeInterpreterSpec{
			AuthMode: runtimev1alpha1.AuthModePicoD,
		},
	}

	result := reconciler.convertToPodTemplate(template, ci)

	// Valid RuntimeClassName should be preserved
	assert.NotNil(t, result.Spec.RuntimeClassName)
	assert.Equal(t, runtimeClass, *result.Spec.RuntimeClassName)
}

func TestConvertToPodTemplate_AllFields(t *testing.T) {
	reconciler := setupTestReconciler()

	runtimeClass := "gvisor"
	imagePullSecret := corev1.LocalObjectReference{Name: "my-secret"}
	template := &runtimev1alpha1.CodeInterpreterSandboxTemplate{
		Image:           "test-image:v1.0.0",
		ImagePullPolicy: corev1.PullAlways,
		RuntimeClassName: &runtimeClass,
		ImagePullSecrets: []corev1.LocalObjectReference{imagePullSecret},
		Command:         []string{"/bin/sh"},
		Args:            []string{"-c", "echo hello"},
		Environment: []corev1.EnvVar{
			{Name: "ENV1", Value: "value1"},
			{Name: "ENV2", Value: "value2"},
		},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100m"),
				corev1.ResourceMemory: resource.MustParse("128Mi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("500m"),
				corev1.ResourceMemory: resource.MustParse("512Mi"),
			},
		},
	}

	ci := &runtimev1alpha1.CodeInterpreter{
		Spec: runtimev1alpha1.CodeInterpreterSpec{
			AuthMode: runtimev1alpha1.AuthModePicoD,
		},
	}

	result := reconciler.convertToPodTemplate(template, ci)

	// Verify pod spec
	assert.Equal(t, template.Image, result.Spec.Containers[0].Image)
	assert.Equal(t, template.ImagePullPolicy, result.Spec.Containers[0].ImagePullPolicy)
	assert.Equal(t, template.Command, result.Spec.Containers[0].Command)
	assert.Equal(t, template.Args, result.Spec.Containers[0].Args)
	assert.Equal(t, template.Resources, result.Spec.Containers[0].Resources)
	assert.Equal(t, template.ImagePullSecrets, result.Spec.ImagePullSecrets)
	assert.Equal(t, runtimeClass, *result.Spec.RuntimeClassName)

	// Verify container name
	assert.Equal(t, "codeinterpreter", result.Spec.Containers[0].Name)

	// Verify environment variables (should include original + public key)
	assert.GreaterOrEqual(t, len(result.Spec.Containers[0].Env), len(template.Environment))
	assert.Contains(t, result.Spec.Containers[0].Env, corev1.EnvVar{Name: "ENV1", Value: "value1"})
	assert.Contains(t, result.Spec.Containers[0].Env, corev1.EnvVar{Name: "ENV2", Value: "value2"})
}

func TestConvertToPodTemplate_AuthModeNone(t *testing.T) {
	reconciler := setupTestReconciler()

	template := &runtimev1alpha1.CodeInterpreterSandboxTemplate{
		Image:           "test-image:latest",
		ImagePullPolicy: corev1.PullIfNotPresent,
		Environment: []corev1.EnvVar{
			{Name: "ENV1", Value: "value1"},
		},
	}

	ci := &runtimev1alpha1.CodeInterpreter{
		Spec: runtimev1alpha1.CodeInterpreterSpec{
			AuthMode: runtimev1alpha1.AuthModeNone,
		},
	}

	result := reconciler.convertToPodTemplate(template, ci)

	// With AuthModeNone, public key should NOT be injected
	envVars := result.Spec.Containers[0].Env
	assert.Equal(t, len(template.Environment), len(envVars))
	assert.NotContains(t, envVars, corev1.EnvVar{Name: "PICOD_AUTH_PUBLIC_KEY"})
}

func TestConvertToPodTemplate_AuthModePicoD(t *testing.T) {
	reconciler := setupTestReconciler()

	template := &runtimev1alpha1.CodeInterpreterSandboxTemplate{
		Image:           "test-image:latest",
		ImagePullPolicy: corev1.PullIfNotPresent,
		Environment: []corev1.EnvVar{
			{Name: "ENV1", Value: "value1"},
		},
	}

	ci := &runtimev1alpha1.CodeInterpreter{
		Spec: runtimev1alpha1.CodeInterpreterSpec{
			AuthMode: runtimev1alpha1.AuthModePicoD,
		},
	}

	result := reconciler.convertToPodTemplate(template, ci)

	// With AuthModePicoD, public key should be injected
	envVars := result.Spec.Containers[0].Env
	assert.Greater(t, len(envVars), len(template.Environment))
	
	// Find the public key env var
	found := false
	for _, env := range envVars {
		if env.Name == "PICOD_AUTH_PUBLIC_KEY" {
			found = true
			break
		}
	}
	assert.True(t, found, "PICOD_AUTH_PUBLIC_KEY should be injected")
}

func TestConvertToPodTemplate_NoEnvironmentVariables(t *testing.T) {
	reconciler := setupTestReconciler()

	template := &runtimev1alpha1.CodeInterpreterSandboxTemplate{
		Image:           "test-image:latest",
		ImagePullPolicy: corev1.PullIfNotPresent,
		Environment:     []corev1.EnvVar{},
	}

	ci := &runtimev1alpha1.CodeInterpreter{
		Spec: runtimev1alpha1.CodeInterpreterSpec{
			AuthMode: runtimev1alpha1.AuthModePicoD,
		},
	}

	result := reconciler.convertToPodTemplate(template, ci)

	// Should only have the public key env var
	envVars := result.Spec.Containers[0].Env
	assert.Equal(t, 1, len(envVars))
	assert.Equal(t, "PICOD_AUTH_PUBLIC_KEY", envVars[0].Name)
}

func TestConvertToPodTemplate_EmptyCommandAndArgs(t *testing.T) {
	reconciler := setupTestReconciler()

	template := &runtimev1alpha1.CodeInterpreterSandboxTemplate{
		Image:           "test-image:latest",
		ImagePullPolicy: corev1.PullIfNotPresent,
		Command:         []string{},
		Args:            []string{},
	}

	ci := &runtimev1alpha1.CodeInterpreter{
		Spec: runtimev1alpha1.CodeInterpreterSpec{
			AuthMode: runtimev1alpha1.AuthModePicoD,
		},
	}

	result := reconciler.convertToPodTemplate(template, ci)

	assert.Empty(t, result.Spec.Containers[0].Command)
	assert.Empty(t, result.Spec.Containers[0].Args)
}

func TestConvertToPodTemplate_NilCommandAndArgs(t *testing.T) {
	reconciler := setupTestReconciler()

	template := &runtimev1alpha1.CodeInterpreterSandboxTemplate{
		Image:           "test-image:latest",
		ImagePullPolicy: corev1.PullIfNotPresent,
		Command:         nil,
		Args:            nil,
	}

	ci := &runtimev1alpha1.CodeInterpreter{
		Spec: runtimev1alpha1.CodeInterpreterSpec{
			AuthMode: runtimev1alpha1.AuthModePicoD,
		},
	}

	result := reconciler.convertToPodTemplate(template, ci)

	assert.Nil(t, result.Spec.Containers[0].Command)
	assert.Nil(t, result.Spec.Containers[0].Args)
}
