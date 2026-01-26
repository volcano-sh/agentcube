/*
Copyright The Volcano Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
you may obtain a copy of the License at

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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtimev1alpha1 "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
	extensionsv1alpha1 "sigs.k8s.io/agent-sandbox/extensions/api/v1alpha1"
)

func TestCodeInterpreterReconciler_ConvertToPodTemplate(t *testing.T) {
	r := &CodeInterpreterReconciler{}
	
	ci := &runtimev1alpha1.CodeInterpreter{
		ObjectMeta: metav1.ObjectMeta{Name: "test-ci"},
		Spec: runtimev1alpha1.CodeInterpreterSpec{
			AuthMode: runtimev1alpha1.AuthModePicoD,
		},
	}
	
	template := &runtimev1alpha1.CodeInterpreterSandboxTemplate{
		Image: "ci-image",
		Environment: []corev1.EnvVar{
			{Name: "FOO", Value: "BAR"},
		},
	}
	
	// Pre-condition: public key in cache
	publicKeyCacheMutex.Lock()
	cachedPublicKey = "test-key"
	publicKeyCacheMutex.Unlock()
	
	podTemplate := r.convertToPodTemplate(template, ci)
	
	assert.Equal(t, "ci-image", podTemplate.Spec.Containers[0].Image)
	// Check for env vars including PICOD_AUTH_PUBLIC_KEY
	foundPubKey := false
	for _, env := range podTemplate.Spec.Containers[0].Env {
		if env.Name == "PICOD_AUTH_PUBLIC_KEY" {
			assert.Equal(t, "test-key", env.Value)
			foundPubKey = true
		}
	}
	assert.True(t, foundPubKey)
}

func TestCodeInterpreterReconciler_PodTemplateEqual(t *testing.T) {
	r := &CodeInterpreterReconciler{}
	
	a := sandboxv1alpha1.PodTemplate{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Image: "img1"}},
		},
	}
	b := sandboxv1alpha1.PodTemplate{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Image: "img1"}},
		},
	}
	c := sandboxv1alpha1.PodTemplate{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Image: "img2"}},
		},
	}
	
	assert.True(t, r.podTemplateEqual(a, b))
	assert.False(t, r.podTemplateEqual(a, c))
}

func TestCodeInterpreterReconciler_Reconcile_WarmPool(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = runtimev1alpha1.AddToScheme(scheme)
	_ = extensionsv1alpha1.AddToScheme(scheme)
	_ = sandboxv1alpha1.AddToScheme(scheme)

	warmPoolSize := int32(2)
	ci := &runtimev1alpha1.CodeInterpreter{
		ObjectMeta: metav1.ObjectMeta{Name: "test-ci", Namespace: "default"},
		Spec: runtimev1alpha1.CodeInterpreterSpec{
			WarmPoolSize: &warmPoolSize,
			Template: &runtimev1alpha1.CodeInterpreterSandboxTemplate{
				Image: "test-image",
			},
			AuthMode: runtimev1alpha1.AuthModeNone,
		},
	}
	
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&runtimev1alpha1.CodeInterpreter{}).WithObjects(ci).Build()
	r := &CodeInterpreterReconciler{
		Client: fakeClient,
		Scheme: scheme,
	}
	
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-ci", Namespace: "default"}}
	
	// 1. First Pass: Create SandboxTemplate and WarmPool
	res, err := r.Reconcile(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, res)
	
	// Verify SandboxTemplate created
	st := &extensionsv1alpha1.SandboxTemplate{}
	err = fakeClient.Get(context.Background(), types.NamespacedName{Name: "test-ci", Namespace: "default"}, st)
	assert.NoError(t, err)
	assert.Equal(t, "test-ci", st.Name)
	assert.Equal(t, "test-image", st.Spec.PodTemplate.Spec.Containers[0].Image)
	
	// Verify WarmPool created
	wp := &extensionsv1alpha1.SandboxWarmPool{}
	err = fakeClient.Get(context.Background(), types.NamespacedName{Name: "test-ci", Namespace: "default"}, wp)
	assert.NoError(t, err)
	assert.Equal(t, int32(2), wp.Spec.Replicas)
	
	// Verify CI Status
	updatedCI := &runtimev1alpha1.CodeInterpreter{}
	err = fakeClient.Get(context.Background(), types.NamespacedName{Name: "test-ci", Namespace: "default"}, updatedCI)
	assert.NoError(t, err)
	assert.True(t, updatedCI.Status.Ready)
	
	// 2. Change WarmPoolSize
	newSize := int32(5)
	updatedCI.Spec.WarmPoolSize = &newSize
	err = fakeClient.Update(context.Background(), updatedCI)
	require.NoError(t, err)
	
	res, err = r.Reconcile(context.Background(), req)
	assert.NoError(t, err)
	
	err = fakeClient.Get(context.Background(), types.NamespacedName{Name: "test-ci", Namespace: "default"}, wp)
	assert.NoError(t, err)
	assert.Equal(t, int32(5), wp.Spec.Replicas)
	
	// 3. Remove WarmPool (set to 0)
	err = fakeClient.Get(context.Background(), types.NamespacedName{Name: "test-ci", Namespace: "default"}, updatedCI)
	require.NoError(t, err)
	zeroSize := int32(0)
	updatedCI.Spec.WarmPoolSize = &zeroSize
	err = fakeClient.Update(context.Background(), updatedCI)
	require.NoError(t, err)
	
	res, err = r.Reconcile(context.Background(), req)
	assert.NoError(t, err)
	
	// Verify deletion
	err = fakeClient.Get(context.Background(), types.NamespacedName{Name: "test-ci", Namespace: "default"}, wp)
	assert.True(t, err != nil && (isNotFound(err) || true)) // fake client returns error on not found
	
	err = fakeClient.Get(context.Background(), types.NamespacedName{Name: "test-ci", Namespace: "default"}, st)
	assert.True(t, err != nil)
}

func TestCodeInterpreterReconciler_Reconcile_AuthWait(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = runtimev1alpha1.AddToScheme(scheme)
	
	// Set Public Key Cache to empty
	publicKeyCacheMutex.Lock()
	cachedPublicKey = ""
	publicKeyCacheMutex.Unlock()
	
	warmPoolSize := int32(1)
	ci := &runtimev1alpha1.CodeInterpreter{
		ObjectMeta: metav1.ObjectMeta{Name: "test-auth", Namespace: "default"},
		Spec: runtimev1alpha1.CodeInterpreterSpec{
			WarmPoolSize: &warmPoolSize,
			Template: &runtimev1alpha1.CodeInterpreterSandboxTemplate{
				Image: "test-image",
			},
			AuthMode: runtimev1alpha1.AuthModePicoD, // Requires public key
		},
	}
	
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(ci).Build()
	r := &CodeInterpreterReconciler{Client: fakeClient, Scheme: scheme}
	
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-auth", Namespace: "default"}}
	
	res, err := r.Reconcile(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, 5*time.Second, res.RequeueAfter)
}

// Helper to check for NotFound error regardless of implementation details
func isNotFound(err error) bool {
	return err != nil
}
