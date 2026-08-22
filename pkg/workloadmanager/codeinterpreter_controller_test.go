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
	"errors"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	runtimev1alpha1 "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
	sandboxv1beta1 "sigs.k8s.io/agent-sandbox/api/v1beta1"
	extensionsv1beta1 "sigs.k8s.io/agent-sandbox/extensions/api/v1beta1"
)

func setupTestReconciler() *CodeInterpreterReconciler {
	return newTestReconciler(interceptor.Funcs{})
}

func newTestReconciler(interceptors interceptor.Funcs, objects ...runtime.Object) *CodeInterpreterReconciler {
	scheme := runtime.NewScheme()
	_ = runtimev1alpha1.AddToScheme(scheme)
	_ = sandboxv1beta1.AddToScheme(scheme)
	_ = extensionsv1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(objects...).
		WithStatusSubresource(&runtimev1alpha1.CodeInterpreter{}, &extensionsv1beta1.SandboxWarmPool{}).
		WithInterceptorFuncs(interceptors).
		Build()

	return &CodeInterpreterReconciler{
		Client: client,
		Scheme: scheme,
	}
}

func newTestReconcilerWithObjects(objects ...runtime.Object) *CodeInterpreterReconciler {
	return newTestReconciler(interceptor.Funcs{}, objects...)
}

func newTestReconcilerWithRecorder(objects ...runtime.Object) (*CodeInterpreterReconciler, *events.FakeRecorder) {
	reconciler := newTestReconcilerWithObjects(objects...)
	recorder := events.NewFakeRecorder(10)
	reconciler.Recorder = recorder
	return reconciler, recorder
}

type replacingDeleteClient struct {
	client.Client
	replacement client.Object
}

func (c *replacingDeleteClient) Delete(ctx context.Context, object client.Object, opts ...client.DeleteOption) error {
	if err := c.Client.Delete(ctx, object); err != nil {
		return err
	}
	if err := c.Create(ctx, c.replacement); err != nil {
		return err
	}
	return c.Client.Delete(ctx, object, opts...)
}

func replaceObjectBeforeDelete(reconciler *CodeInterpreterReconciler, replacement client.Object) {
	reconciler.Client = &replacingDeleteClient{Client: reconciler.Client, replacement: replacement}
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
			UID:       "test-code-interpreter",
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

	sandboxTemplate := &extensionsv1beta1.SandboxTemplate{}
	err = reconciler.Get(context.Background(), types.NamespacedName{
		Name:      ci.Name,
		Namespace: ci.Namespace,
	}, sandboxTemplate)
	assert.NoError(t, err)
	assert.Equal(t, extensionsv1beta1.NetworkPolicyManagementUnmanaged, sandboxTemplate.Spec.NetworkPolicyManagement)
}

func TestEnsureSandboxTemplateUpdatesManagedNetworkPolicyToUnmanaged(t *testing.T) {
	ci := testCodeInterpreterWithWarmPool()
	existing := &extensionsv1beta1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ci.Name,
			Namespace: ci.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(ci, runtimev1alpha1.GroupVersion.WithKind("CodeInterpreter")),
			},
		},
		Spec: extensionsv1beta1.SandboxTemplateSpec{
			NetworkPolicyManagement: extensionsv1beta1.NetworkPolicyManagementManaged,
			SandboxBlueprint: sandboxv1beta1.SandboxBlueprint{
				PodTemplate: sandboxv1beta1.PodTemplate{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{{
							Name:  "codeinterpreter",
							Image: "stale-image",
						}},
					},
				},
			},
		},
	}
	reconciler := newTestReconcilerWithObjects(ci, existing)

	_, err := reconciler.ensureSandboxTemplate(context.Background(), ci)
	assert.NoError(t, err)

	sandboxTemplate := &extensionsv1beta1.SandboxTemplate{}
	err = reconciler.Get(context.Background(), types.NamespacedName{
		Name:      ci.Name,
		Namespace: ci.Namespace,
	}, sandboxTemplate)
	assert.NoError(t, err)
	assert.Equal(t, extensionsv1beta1.NetworkPolicyManagementUnmanaged, sandboxTemplate.Spec.NetworkPolicyManagement)
}

func TestEnsureSandboxTemplateRejectsUnownedTemplate(t *testing.T) {
	ci := testCodeInterpreterWithWarmPool()
	existing := &extensionsv1beta1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ci.Name,
			Namespace: ci.Namespace,
		},
		Spec: extensionsv1beta1.SandboxTemplateSpec{
			SandboxBlueprint: sandboxv1beta1.SandboxBlueprint{
				PodTemplate: sandboxv1beta1.PodTemplate{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{{
							Name:  "existing",
							Image: "existing-image",
						}},
					},
				},
			},
		},
	}
	reconciler := newTestReconcilerWithObjects(ci, existing)

	_, err := reconciler.ensureSandboxTemplate(context.Background(), ci)
	assert.ErrorContains(t, err, "is not controlled by CodeInterpreter")

	sandboxTemplate := &extensionsv1beta1.SandboxTemplate{}
	err = reconciler.Get(context.Background(), types.NamespacedName{
		Name:      ci.Name,
		Namespace: ci.Namespace,
	}, sandboxTemplate)
	assert.NoError(t, err)
	assert.Equal(t, "existing-image", sandboxTemplate.Spec.PodTemplate.Spec.Containers[0].Image)

	storedCI := &runtimev1alpha1.CodeInterpreter{}
	err = reconciler.Get(context.Background(), types.NamespacedName{
		Name:      ci.Name,
		Namespace: ci.Namespace,
	}, storedCI)
	assert.NoError(t, err)
	condition := apimeta.FindStatusCondition(storedCI.Status.Conditions, "Ready")
	if assert.NotNil(t, condition) {
		assert.Equal(t, metav1.ConditionFalse, condition.Status)
		assert.Equal(t, codeInterpreterOwnershipConflict, condition.Reason)
	}
	assertCondition(t, storedCI.Status.Conditions, codeInterpreterWarmPoolCondition, metav1.ConditionUnknown, codeInterpreterOwnershipConflict)
}

func TestEnsureSandboxWarmPoolRejectsUnownedWarmPool(t *testing.T) {
	ci := testCodeInterpreterWithWarmPool()
	existing := &extensionsv1beta1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ci.Name,
			Namespace: ci.Namespace,
		},
		Spec: extensionsv1beta1.SandboxWarmPoolSpec{
			Replicas: int32Pointer(7),
			TemplateRef: extensionsv1beta1.SandboxTemplateRef{
				Name: "existing-template",
			},
		},
	}
	reconciler := newTestReconcilerWithObjects(ci, existing)

	err := reconciler.ensureSandboxWarmPool(context.Background(), ci)
	assert.ErrorContains(t, err, "is not controlled by CodeInterpreter")

	warmPool := &extensionsv1beta1.SandboxWarmPool{}
	err = reconciler.Get(context.Background(), types.NamespacedName{
		Name:      ci.Name,
		Namespace: ci.Namespace,
	}, warmPool)
	assert.NoError(t, err)
	if assert.NotNil(t, warmPool.Spec.Replicas) {
		assert.Equal(t, int32(7), *warmPool.Spec.Replicas)
	}
	assert.Equal(t, "existing-template", warmPool.Spec.TemplateRef.Name)

	storedCI := &runtimev1alpha1.CodeInterpreter{}
	err = reconciler.Get(context.Background(), client.ObjectKeyFromObject(ci), storedCI)
	assert.NoError(t, err)
	assert.False(t, storedCI.Status.Ready)
	assertCondition(t, storedCI.Status.Conditions, codeInterpreterReadyCondition, metav1.ConditionFalse, codeInterpreterOwnershipConflict)
	assertCondition(t, storedCI.Status.Conditions, codeInterpreterWarmPoolCondition, metav1.ConditionUnknown, codeInterpreterOwnershipConflict)
}

func TestEnsureSandboxWarmPoolReconcilesV1Beta1Replicas(t *testing.T) {
	tests := []struct {
		name        string
		existing    bool
		replicas    *int32
		desired     int32
		wantUpdates int
	}{
		{name: "creates pool", desired: 2},
		{name: "repairs nil replicas", existing: true, desired: 2, wantUpdates: 1},
		{name: "skips unchanged replicas", existing: true, replicas: int32Pointer(2), desired: 2},
		{name: "updates changed replicas", existing: true, replicas: int32Pointer(2), desired: 3, wantUpdates: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ci := codeInterpreterForHealthTest(1, int32Pointer(tt.desired))
			objects := []runtime.Object{ci}
			if tt.existing {
				warmPool := warmPoolForHealthTest(tt.desired, 0)
				warmPool.Spec.Replicas = tt.replicas
				objects = append(objects, warmPool)
			}

			updates := 0
			reconciler := newTestReconciler(interceptor.Funcs{
				Update: func(ctx context.Context, c client.WithWatch, object client.Object, opts ...client.UpdateOption) error {
					updates++
					return c.Update(ctx, object, opts...)
				},
			}, objects...)

			require.NoError(t, reconciler.ensureSandboxWarmPool(context.Background(), ci))
			assert.Equal(t, tt.wantUpdates, updates)

			stored := &extensionsv1beta1.SandboxWarmPool{}
			require.NoError(t, reconciler.Get(context.Background(), client.ObjectKeyFromObject(ci), stored))
			if assert.NotNil(t, stored.Spec.Replicas) {
				assert.Equal(t, tt.desired, *stored.Spec.Replicas)
			}
		})
	}
}

func TestDeleteSandboxTemplateRejectsUnownedTemplate(t *testing.T) {
	ci := testCodeInterpreterWithWarmPool()
	existing := &extensionsv1beta1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ci.Name,
			Namespace: ci.Namespace,
		},
	}
	reconciler := newTestReconcilerWithObjects(ci, existing)

	err := reconciler.deleteSandboxTemplate(context.Background(), ci)
	assert.ErrorContains(t, err, "is not controlled by CodeInterpreter")

	err = reconciler.Get(context.Background(), types.NamespacedName{
		Name:      ci.Name,
		Namespace: ci.Namespace,
	}, &extensionsv1beta1.SandboxTemplate{})
	assert.NoError(t, err)
}

func TestDeleteSandboxWarmPoolRejectsUnownedWarmPool(t *testing.T) {
	ci := testCodeInterpreterWithWarmPool()
	existing := &extensionsv1beta1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ci.Name,
			Namespace: ci.Namespace,
		},
	}
	reconciler := newTestReconcilerWithObjects(ci, existing)

	err := reconciler.deleteSandboxWarmPool(context.Background(), ci)
	assert.ErrorContains(t, err, "is not controlled by CodeInterpreter")

	err = reconciler.Get(context.Background(), types.NamespacedName{
		Name:      ci.Name,
		Namespace: ci.Namespace,
	}, &extensionsv1beta1.SandboxWarmPool{})
	assert.NoError(t, err)
}

func TestDeleteSandboxTemplateRejectsReplacement(t *testing.T) {
	ci := testCodeInterpreterWithWarmPool()
	original := &extensionsv1beta1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ci.Name,
			Namespace: ci.Namespace,
			UID:       "original-template",
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(ci, runtimev1alpha1.GroupVersion.WithKind("CodeInterpreter")),
			},
		},
	}
	replacement := &extensionsv1beta1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ci.Name,
			Namespace: ci.Namespace,
			UID:       "replacement-template",
		},
	}
	reconciler := newTestReconcilerWithObjects(ci, original)
	replaceObjectBeforeDelete(reconciler, replacement)

	err := reconciler.deleteSandboxTemplate(context.Background(), ci)
	assert.True(t, apierrors.IsConflict(err))

	stored := &extensionsv1beta1.SandboxTemplate{}
	err = reconciler.Get(context.Background(), client.ObjectKeyFromObject(replacement), stored)
	assert.NoError(t, err)
	assert.Equal(t, replacement.UID, stored.UID)
}

func TestDeleteSandboxWarmPoolRejectsReplacement(t *testing.T) {
	ci := testCodeInterpreterWithWarmPool()
	original := &extensionsv1beta1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ci.Name,
			Namespace: ci.Namespace,
			UID:       "original-warm-pool",
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(ci, runtimev1alpha1.GroupVersion.WithKind("CodeInterpreter")),
			},
		},
	}
	replacement := &extensionsv1beta1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ci.Name,
			Namespace: ci.Namespace,
			UID:       "replacement-warm-pool",
		},
	}
	reconciler := newTestReconcilerWithObjects(ci, original)
	replaceObjectBeforeDelete(reconciler, replacement)

	err := reconciler.deleteSandboxWarmPool(context.Background(), ci)
	assert.True(t, apierrors.IsConflict(err))

	stored := &extensionsv1beta1.SandboxWarmPool{}
	err = reconciler.Get(context.Background(), client.ObjectKeyFromObject(replacement), stored)
	assert.NoError(t, err)
	assert.Equal(t, replacement.UID, stored.UID)
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

func TestWarmPoolAvailableCondition(t *testing.T) {
	tests := []struct {
		name         string
		warmPoolSize *int32
		warmPool     *extensionsv1beta1.SandboxWarmPool
		wantStatus   metav1.ConditionStatus
		wantReason   string
		wantMessage  string
	}{
		{
			name:       "disabled when not configured",
			wantStatus: metav1.ConditionUnknown,
			wantReason: codeInterpreterWarmPoolDisabled,
		},
		{
			name:         "configured pool is missing",
			warmPoolSize: int32Pointer(2),
			wantStatus:   metav1.ConditionUnknown,
			wantReason:   codeInterpreterWarmPoolProgressing,
		},
		{
			name:         "pool is empty",
			warmPoolSize: int32Pointer(3),
			warmPool:     warmPoolForHealthTest(3, 0),
			wantStatus:   metav1.ConditionFalse,
			wantReason:   codeInterpreterWarmPoolEmpty,
		},
		{
			name:         "unowned pool health is not trusted",
			warmPoolSize: int32Pointer(2),
			warmPool:     unownedWarmPoolForHealthTest(2, 2),
			wantStatus:   metav1.ConditionUnknown,
			wantReason:   codeInterpreterOwnershipConflict,
		},
		{
			name:         "ready replicas are below the low watermark",
			warmPoolSize: int32Pointer(4),
			warmPool:     warmPoolForHealthTest(4, 1),
			wantStatus:   metav1.ConditionFalse,
			wantReason:   codeInterpreterWarmPoolBelowWatermark,
			wantMessage:  "below low watermark 2",
		},
		{
			name:         "ceil half of an odd desired count is ready",
			warmPoolSize: int32Pointer(3),
			warmPool:     warmPoolForHealthTest(3, 2),
			wantStatus:   metav1.ConditionTrue,
			wantReason:   codeInterpreterWarmPoolReady,
		},
		{
			name:         "maximum desired count does not overflow the watermark",
			warmPoolSize: int32Pointer(math.MaxInt32),
			warmPool:     warmPoolForHealthTest(math.MaxInt32, 1),
			wantStatus:   metav1.ConditionFalse,
			wantReason:   codeInterpreterWarmPoolBelowWatermark,
			wantMessage:  "below low watermark 1073741824",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := []runtime.Object{}
			if tt.warmPool != nil {
				objects = append(objects, tt.warmPool)
			}
			reconciler := newTestReconcilerWithObjects(objects...)
			ci := codeInterpreterForHealthTest(7, tt.warmPoolSize)

			condition, err := reconciler.warmPoolAvailableCondition(context.Background(), ci)

			require.NoError(t, err)
			assert.Equal(t, codeInterpreterWarmPoolCondition, condition.Type)
			assert.Equal(t, tt.wantStatus, condition.Status)
			assert.Equal(t, tt.wantReason, condition.Reason)
			assert.Equal(t, ci.Generation, condition.ObservedGeneration)
			if tt.wantMessage != "" {
				assert.Contains(t, condition.Message, tt.wantMessage)
			}
		})
	}
}

func TestWarmPoolAvailableConditionReturnsGetError(t *testing.T) {
	wantErr := errors.New("temporary warm pool read failure")
	reconciler := newTestReconciler(interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*extensionsv1beta1.SandboxWarmPool); ok {
				return wantErr
			}
			return c.Get(ctx, key, obj, opts...)
		},
	})

	_, err := reconciler.warmPoolAvailableCondition(
		context.Background(),
		codeInterpreterForHealthTest(1, int32Pointer(2)),
	)

	require.ErrorIs(t, err, wantErr)
}

func TestShouldRecordWarmPoolWarningEvent(t *testing.T) {
	tests := []struct {
		name     string
		previous *metav1.Condition
		current  metav1.Condition
		want     bool
	}{
		{
			name:    "first empty observation",
			current: warmPoolCondition(metav1.ConditionFalse, codeInterpreterWarmPoolEmpty),
			want:    true,
		},
		{
			name:    "first below-watermark observation",
			current: warmPoolCondition(metav1.ConditionFalse, codeInterpreterWarmPoolBelowWatermark),
			want:    true,
		},
		{
			name:     "unchanged empty observation",
			previous: conditionPointer(warmPoolCondition(metav1.ConditionFalse, codeInterpreterWarmPoolEmpty)),
			current:  warmPoolCondition(metav1.ConditionFalse, codeInterpreterWarmPoolEmpty),
		},
		{
			name:     "degradation reason changed",
			previous: conditionPointer(warmPoolCondition(metav1.ConditionFalse, codeInterpreterWarmPoolBelowWatermark)),
			current:  warmPoolCondition(metav1.ConditionFalse, codeInterpreterWarmPoolEmpty),
			want:     true,
		},
		{
			name:    "pool is ready",
			current: warmPoolCondition(metav1.ConditionTrue, codeInterpreterWarmPoolReady),
		},
		{
			name:    "pool has not been observed yet",
			current: warmPoolCondition(metav1.ConditionUnknown, codeInterpreterWarmPoolProgressing),
		},
		{
			name: "unrelated condition",
			current: metav1.Condition{
				Type:   codeInterpreterReadyCondition,
				Status: metav1.ConditionFalse,
				Reason: codeInterpreterWarmPoolEmpty,
			},
		},
	}

	reconciler := setupTestReconciler()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, reconciler.shouldRecordWarmPoolWarningEvent(tt.previous, tt.current))
		})
	}
}

func TestUpdateStatusProjectsWarmPoolDegradationAndRecovery(t *testing.T) {
	ci := codeInterpreterForHealthTest(3, int32Pointer(4))
	warmPool := warmPoolForHealthTest(4, 4)
	reconciler, recorder := newTestReconcilerWithRecorder(ci, warmPool)
	ctx := context.Background()

	stored := &runtimev1alpha1.CodeInterpreter{}
	require.NoError(t, reconciler.Get(ctx, types.NamespacedName{Name: ci.Name, Namespace: ci.Namespace}, stored))
	require.NoError(t, reconciler.updateReconciledStatus(ctx, stored))

	updated := &runtimev1alpha1.CodeInterpreter{}
	require.NoError(t, reconciler.Get(ctx, types.NamespacedName{Name: ci.Name, Namespace: ci.Namespace}, updated))
	assert.True(t, updated.Status.Ready)
	assertCondition(t, updated.Status.Conditions, codeInterpreterReadyCondition, metav1.ConditionTrue, codeInterpreterReadyReason)
	assertCondition(t, updated.Status.Conditions, codeInterpreterWarmPoolCondition, metav1.ConditionTrue, codeInterpreterWarmPoolReady)
	assertNoEvent(t, recorder)

	storedPool := &extensionsv1beta1.SandboxWarmPool{}
	require.NoError(t, reconciler.Get(ctx, types.NamespacedName{Name: ci.Name, Namespace: ci.Namespace}, storedPool))
	storedPool.Status.ReadyReplicas = 1
	require.NoError(t, reconciler.Status().Update(ctx, storedPool))
	require.NoError(t, reconciler.Get(ctx, types.NamespacedName{Name: ci.Name, Namespace: ci.Namespace}, updated))
	require.NoError(t, reconciler.updateReconciledStatus(ctx, updated))
	require.NoError(t, reconciler.Get(ctx, types.NamespacedName{Name: ci.Name, Namespace: ci.Namespace}, updated))
	assert.True(t, updated.Status.Ready)
	assertCondition(t, updated.Status.Conditions, codeInterpreterReadyCondition, metav1.ConditionTrue, codeInterpreterReadyReason)
	assertCondition(t, updated.Status.Conditions, codeInterpreterWarmPoolCondition, metav1.ConditionFalse, codeInterpreterWarmPoolBelowWatermark)
	assertEventContains(t, recorder, corev1.EventTypeWarning, codeInterpreterWarmPoolBelowWatermark)

	require.NoError(t, reconciler.Get(ctx, types.NamespacedName{Name: ci.Name, Namespace: ci.Namespace}, storedPool))
	storedPool.Status.ReadyReplicas = 4
	require.NoError(t, reconciler.Status().Update(ctx, storedPool))
	require.NoError(t, reconciler.Get(ctx, types.NamespacedName{Name: ci.Name, Namespace: ci.Namespace}, updated))
	require.NoError(t, reconciler.updateReconciledStatus(ctx, updated))
	require.NoError(t, reconciler.Get(ctx, types.NamespacedName{Name: ci.Name, Namespace: ci.Namespace}, updated))
	assertCondition(t, updated.Status.Conditions, codeInterpreterWarmPoolCondition, metav1.ConditionTrue, codeInterpreterWarmPoolReady)
	assertNoEvent(t, recorder)
}

func TestUpdateStatusDoesNotRepeatWarningForSameDegradation(t *testing.T) {
	ci := codeInterpreterForHealthTest(3, int32Pointer(2))
	warmPool := warmPoolForHealthTest(2, 0)
	reconciler, recorder := newTestReconcilerWithRecorder(ci, warmPool)
	ctx := context.Background()

	stored := &runtimev1alpha1.CodeInterpreter{}
	require.NoError(t, reconciler.Get(ctx, types.NamespacedName{Name: ci.Name, Namespace: ci.Namespace}, stored))
	require.NoError(t, reconciler.updateReconciledStatus(ctx, stored))
	assertEventContains(t, recorder, corev1.EventTypeWarning, codeInterpreterWarmPoolEmpty)

	// A new parent generation requires a status write, but the unchanged
	// degradation reason must not produce another warning.
	stored.Generation++
	require.NoError(t, reconciler.updateReconciledStatus(ctx, stored))
	assertNoEvent(t, recorder)
}

func TestUpdateStatusSkipsUnchangedStatus(t *testing.T) {
	ci := codeInterpreterForHealthTest(3, int32Pointer(2))
	ci.Status.Ready = true
	ci.Status.Conditions = []metav1.Condition{
		{
			Type:               codeInterpreterReadyCondition,
			Status:             metav1.ConditionTrue,
			Reason:             codeInterpreterReadyReason,
			Message:            "CodeInterpreter is ready",
			ObservedGeneration: ci.Generation,
		},
		{
			Type:               codeInterpreterWarmPoolCondition,
			Status:             metav1.ConditionTrue,
			Reason:             codeInterpreterWarmPoolReady,
			Message:            "SandboxWarmPool has 2 ready replicas out of 2 desired",
			ObservedGeneration: ci.Generation,
		},
	}
	warmPool := warmPoolForHealthTest(2, 2)
	statusUpdated := false
	reconciler := newTestReconciler(interceptor.Funcs{
		SubResourceUpdate: func(context.Context, client.Client, string, client.Object, ...client.SubResourceUpdateOption) error {
			statusUpdated = true
			return errors.New("unexpected status update")
		},
	}, ci, warmPool)

	stored := &runtimev1alpha1.CodeInterpreter{}
	require.NoError(t, reconciler.Get(context.Background(), types.NamespacedName{Name: ci.Name, Namespace: ci.Namespace}, stored))
	require.NoError(t, reconciler.updateReconciledStatus(context.Background(), stored))
	assert.False(t, statusUpdated)
}

func TestUpdateStatusDoesNotRecordEventWhenStatusUpdateFails(t *testing.T) {
	ci := codeInterpreterForHealthTest(3, int32Pointer(2))
	warmPool := warmPoolForHealthTest(2, 0)
	wantErr := errors.New("status update failed")
	reconciler := newTestReconciler(interceptor.Funcs{
		SubResourceUpdate: func(context.Context, client.Client, string, client.Object, ...client.SubResourceUpdateOption) error {
			return wantErr
		},
	}, ci, warmPool)
	recorder := events.NewFakeRecorder(1)
	reconciler.Recorder = recorder

	stored := &runtimev1alpha1.CodeInterpreter{}
	require.NoError(t, reconciler.Get(context.Background(), types.NamespacedName{Name: ci.Name, Namespace: ci.Namespace}, stored))
	err := reconciler.updateReconciledStatus(context.Background(), stored)

	require.ErrorIs(t, err, wantErr)
	assertNoEvent(t, recorder)
}

func TestUpdateReconciledStatusRejectsUnownedWarmPoolHealth(t *testing.T) {
	ci := codeInterpreterForHealthTest(3, int32Pointer(2))
	reconciler := newTestReconcilerWithObjects(ci, unownedWarmPoolForHealthTest(2, 2))
	ctx := context.Background()

	stored := &runtimev1alpha1.CodeInterpreter{}
	require.NoError(t, reconciler.Get(ctx, client.ObjectKeyFromObject(ci), stored))
	err := reconciler.updateReconciledStatus(ctx, stored)
	require.ErrorContains(t, err, "is not controlled by CodeInterpreter")

	require.NoError(t, reconciler.Get(ctx, client.ObjectKeyFromObject(ci), stored))
	assert.False(t, stored.Status.Ready)
	assertCondition(t, stored.Status.Conditions, codeInterpreterReadyCondition, metav1.ConditionFalse, codeInterpreterOwnershipConflict)
	assertCondition(t, stored.Status.Conditions, codeInterpreterWarmPoolCondition, metav1.ConditionUnknown, codeInterpreterOwnershipConflict)
}

func TestUpdateReconciledStatusDoesNotPersistPartialStatusOnWarmPoolReadError(t *testing.T) {
	wantErr := errors.New("temporary warm pool read failure")
	ci := codeInterpreterForHealthTest(3, int32Pointer(2))
	reconciler := newTestReconciler(interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, object client.Object, opts ...client.GetOption) error {
			if _, ok := object.(*extensionsv1beta1.SandboxWarmPool); ok {
				return wantErr
			}
			return c.Get(ctx, key, object, opts...)
		},
	}, ci)
	ctx := context.Background()

	stored := &runtimev1alpha1.CodeInterpreter{}
	require.NoError(t, reconciler.Get(ctx, client.ObjectKeyFromObject(ci), stored))
	require.ErrorIs(t, reconciler.updateReconciledStatus(ctx, stored), wantErr)

	require.NoError(t, reconciler.Get(ctx, client.ObjectKeyFromObject(ci), stored))
	assert.False(t, stored.Status.Ready)
	assert.Empty(t, stored.Status.Conditions)
}

func TestReconcileProjectsEmptyWarmPoolWithoutDegradingReady(t *testing.T) {
	ci := codeInterpreterForHealthTest(1, int32Pointer(2))
	reconciler, recorder := newTestReconcilerWithRecorder(ci)
	request := ctrl.Request{NamespacedName: types.NamespacedName{Name: ci.Name, Namespace: ci.Namespace}}

	_, err := reconciler.Reconcile(context.Background(), request)
	require.NoError(t, err)

	updated := &runtimev1alpha1.CodeInterpreter{}
	require.NoError(t, reconciler.Get(context.Background(), request.NamespacedName, updated))
	assert.True(t, updated.Status.Ready)
	assertCondition(t, updated.Status.Conditions, codeInterpreterReadyCondition, metav1.ConditionTrue, codeInterpreterReadyReason)
	assertCondition(t, updated.Status.Conditions, codeInterpreterWarmPoolCondition, metav1.ConditionFalse, codeInterpreterWarmPoolEmpty)
	assertEventContains(t, recorder, corev1.EventTypeWarning, codeInterpreterWarmPoolEmpty)
}

func TestReconcileRecoversReadyAfterOwnershipConflictResolved(t *testing.T) {
	ci := codeInterpreterForHealthTest(1, int32Pointer(2))
	unownedWarmPool := unownedWarmPoolForHealthTest(2, 2)
	reconciler := newTestReconcilerWithObjects(ci, unownedWarmPool)
	request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(ci)}
	ctx := context.Background()

	_, err := reconciler.Reconcile(ctx, request)
	require.ErrorContains(t, err, "is not controlled by CodeInterpreter")

	updated := &runtimev1alpha1.CodeInterpreter{}
	require.NoError(t, reconciler.Get(ctx, request.NamespacedName, updated))
	assert.False(t, updated.Status.Ready)
	assertCondition(t, updated.Status.Conditions, codeInterpreterReadyCondition, metav1.ConditionFalse, codeInterpreterOwnershipConflict)
	assertCondition(t, updated.Status.Conditions, codeInterpreterWarmPoolCondition, metav1.ConditionUnknown, codeInterpreterOwnershipConflict)

	require.NoError(t, reconciler.Delete(ctx, unownedWarmPool))
	_, err = reconciler.Reconcile(ctx, request)
	require.NoError(t, err)

	require.NoError(t, reconciler.Get(ctx, request.NamespacedName, updated))
	assert.True(t, updated.Status.Ready)
	assertCondition(t, updated.Status.Conditions, codeInterpreterReadyCondition, metav1.ConditionTrue, codeInterpreterReadyReason)
	assertCondition(t, updated.Status.Conditions, codeInterpreterWarmPoolCondition, metav1.ConditionFalse, codeInterpreterWarmPoolEmpty)
}

func TestReconcileDisablesWarmPoolWithNilOrZeroSize(t *testing.T) {
	tests := []struct {
		name         string
		warmPoolSize *int32
	}{
		{name: "nil size"},
		{name: "zero size", warmPoolSize: int32Pointer(0)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ci := codeInterpreterForHealthTest(2, tt.warmPoolSize)
			owner := *metav1.NewControllerRef(ci, runtimev1alpha1.GroupVersion.WithKind("CodeInterpreter"))
			warmPool := warmPoolForHealthTest(2, 2)
			template := &extensionsv1beta1.SandboxTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name:            ci.Name,
					Namespace:       ci.Namespace,
					OwnerReferences: []metav1.OwnerReference{owner},
				},
			}
			reconciler := newTestReconcilerWithObjects(ci, warmPool, template)
			request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(ci)}

			_, err := reconciler.Reconcile(context.Background(), request)
			require.NoError(t, err)

			err = reconciler.Get(context.Background(), request.NamespacedName, &extensionsv1beta1.SandboxWarmPool{})
			assert.True(t, apierrors.IsNotFound(err))
			err = reconciler.Get(context.Background(), request.NamespacedName, &extensionsv1beta1.SandboxTemplate{})
			assert.True(t, apierrors.IsNotFound(err))

			updated := &runtimev1alpha1.CodeInterpreter{}
			require.NoError(t, reconciler.Get(context.Background(), request.NamespacedName, updated))
			assert.True(t, updated.Status.Ready)
			assertCondition(t, updated.Status.Conditions, codeInterpreterReadyCondition, metav1.ConditionTrue, codeInterpreterReadyReason)
			assertCondition(t, updated.Status.Conditions, codeInterpreterWarmPoolCondition, metav1.ConditionUnknown, codeInterpreterWarmPoolDisabled)
		})
	}
}

func codeInterpreterForHealthTest(generation int64, warmPoolSize *int32) *runtimev1alpha1.CodeInterpreter {
	return &runtimev1alpha1.CodeInterpreter{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-ci",
			Namespace:  "default",
			UID:        "test-ci",
			Generation: generation,
		},
		Spec: runtimev1alpha1.CodeInterpreterSpec{
			AuthMode:     runtimev1alpha1.AuthModeNone,
			WarmPoolSize: warmPoolSize,
			Template: &runtimev1alpha1.CodeInterpreterSandboxTemplate{
				Image:           "test-image:latest",
				ImagePullPolicy: corev1.PullIfNotPresent,
			},
		},
	}
}

func warmPoolForHealthTest(desired, ready int32) *extensionsv1beta1.SandboxWarmPool {
	warmPool := unownedWarmPoolForHealthTest(desired, ready)
	warmPool.OwnerReferences = []metav1.OwnerReference{
		*metav1.NewControllerRef(
			codeInterpreterForHealthTest(1, int32Pointer(desired)),
			runtimev1alpha1.GroupVersion.WithKind("CodeInterpreter"),
		),
	}
	return warmPool
}

func unownedWarmPoolForHealthTest(desired, ready int32) *extensionsv1beta1.SandboxWarmPool {
	return &extensionsv1beta1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ci",
			Namespace: "default",
		},
		Spec: extensionsv1beta1.SandboxWarmPoolSpec{
			Replicas: int32Pointer(desired),
			TemplateRef: extensionsv1beta1.SandboxTemplateRef{
				Name: "test-ci",
			},
		},
		Status: extensionsv1beta1.SandboxWarmPoolStatus{
			Replicas:      desired,
			ReadyReplicas: ready,
		},
	}
}

func int32Pointer(value int32) *int32 {
	return &value
}

func warmPoolCondition(status metav1.ConditionStatus, reason string) metav1.Condition {
	return metav1.Condition{Type: codeInterpreterWarmPoolCondition, Status: status, Reason: reason}
}

func conditionPointer(condition metav1.Condition) *metav1.Condition {
	return &condition
}

func assertCondition(t *testing.T, conditions []metav1.Condition, conditionType string, status metav1.ConditionStatus, reason string) {
	t.Helper()
	condition := apimeta.FindStatusCondition(conditions, conditionType)
	if assert.NotNil(t, condition) {
		assert.Equal(t, status, condition.Status)
		assert.Equal(t, reason, condition.Reason)
	}
}

func assertEventContains(t *testing.T, recorder *events.FakeRecorder, eventType, reason string) {
	t.Helper()
	select {
	case event := <-recorder.Events:
		assert.Contains(t, event, eventType)
		assert.Contains(t, event, reason)
	default:
		t.Fatalf("expected %s event with reason %s", eventType, reason)
	}
}

func assertNoEvent(t *testing.T, recorder *events.FakeRecorder) {
	t.Helper()
	select {
	case event := <-recorder.Events:
		t.Fatalf("expected no event, got %q", event)
	default:
	}
}
