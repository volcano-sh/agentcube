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
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	runtimev1alpha1 "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
	extensionsv1alpha1 "sigs.k8s.io/agent-sandbox/extensions/api/v1alpha1"
)

func setupTestReconciler() *CodeInterpreterReconciler {
	return setupTestReconcilerWithInterceptors(interceptor.Funcs{})
}

func setupTestReconcilerWithInterceptors(interceptors interceptor.Funcs) *CodeInterpreterReconciler {
	scheme := runtime.NewScheme()
	_ = runtimev1alpha1.AddToScheme(scheme)
	_ = sandboxv1alpha1.AddToScheme(scheme)
	_ = extensionsv1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&runtimev1alpha1.CodeInterpreter{}).
		WithStatusSubresource(&extensionsv1alpha1.SandboxWarmPool{}).
		WithInterceptorFuncs(interceptors).
		Build()

	return &CodeInterpreterReconciler{
		Client: client,
		Scheme: scheme,
	}
}

func setupTestReconcilerWithRecorder(bufferSize int) (*CodeInterpreterReconciler, *record.FakeRecorder) {
	reconciler := setupTestReconciler()
	recorder := record.NewFakeRecorder(bufferSize)
	reconciler.Recorder = recorder
	return reconciler, recorder
}

func int32Ptr(v int32) *int32 {
	return &v
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

func TestWarmPoolAvailableCondition(t *testing.T) {
	tests := []struct {
		name         string
		warmPoolSize *int32
		warmPool     *extensionsv1alpha1.SandboxWarmPool
		wantStatus   metav1.ConditionStatus
		wantReason   string
		wantErr      bool
	}{
		{
			name:         "disabled when warm pool is not configured",
			warmPoolSize: nil,
			wantStatus:   metav1.ConditionUnknown,
			wantReason:   codeInterpreterWarmPoolDisabled,
		},
		{
			name:         "false when configured warm pool is missing",
			warmPoolSize: int32Ptr(2),
			wantStatus:   metav1.ConditionFalse,
			wantReason:   codeInterpreterWarmPoolNotFound,
		},
		{
			name:         "false when no ready replicas",
			warmPoolSize: int32Ptr(3),
			warmPool:     testSandboxWarmPool(3, 0),
			wantStatus:   metav1.ConditionFalse,
			wantReason:   codeInterpreterWarmPoolEmpty,
		},
		{
			name:         "false when below low watermark",
			warmPoolSize: int32Ptr(4),
			warmPool:     testSandboxWarmPool(4, 1),
			wantStatus:   metav1.ConditionFalse,
			wantReason:   codeInterpreterWarmPoolBelowWatermark,
		},
		{
			name:         "true when low watermark is met",
			warmPoolSize: int32Ptr(4),
			warmPool:     testSandboxWarmPool(4, 2),
			wantStatus:   metav1.ConditionTrue,
			wantReason:   codeInterpreterWarmPoolReady,
		},
		{
			name:         "true when single warm pool replica is ready",
			warmPoolSize: int32Ptr(1),
			warmPool:     testSandboxWarmPool(1, 1),
			wantStatus:   metav1.ConditionTrue,
			wantReason:   codeInterpreterWarmPoolReady,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reconciler := setupTestReconciler()
			if tt.warmPool != nil {
				assert.NoError(t, reconciler.Create(context.Background(), tt.warmPool))
			}

			ci := &runtimev1alpha1.CodeInterpreter{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-ci",
					Namespace:  "default",
					Generation: 7,
				},
				Spec: runtimev1alpha1.CodeInterpreterSpec{
					WarmPoolSize: tt.warmPoolSize,
				},
			}

			condition, err := reconciler.warmPoolAvailableCondition(context.Background(), ci)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, codeInterpreterWarmPoolCondition, condition.Type)
			assert.Equal(t, tt.wantStatus, condition.Status)
			assert.Equal(t, tt.wantReason, condition.Reason)
			assert.Equal(t, ci.Generation, condition.ObservedGeneration)
		})
	}
}

func TestWarmPoolAvailableConditionReturnsGetError(t *testing.T) {
	reconciler := setupTestReconcilerWithInterceptors(interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*extensionsv1alpha1.SandboxWarmPool); ok {
				return fmt.Errorf("temporary client error")
			}
			return c.Get(ctx, key, obj, opts...)
		},
	})

	ci := &runtimev1alpha1.CodeInterpreter{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ci",
			Namespace: "default",
		},
		Spec: runtimev1alpha1.CodeInterpreterSpec{
			WarmPoolSize: int32Ptr(2),
		},
	}

	_, err := reconciler.warmPoolAvailableCondition(context.Background(), ci)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get SandboxWarmPool for status")
}

func TestShouldRecordWarmPoolWarningEvent(t *testing.T) {
	tests := []struct {
		name     string
		previous *metav1.Condition
		current  metav1.Condition
		want     bool
	}{
		{
			name: "records first empty warning",
			current: metav1.Condition{
				Type:   codeInterpreterWarmPoolCondition,
				Status: metav1.ConditionFalse,
				Reason: codeInterpreterWarmPoolEmpty,
			},
			want: true,
		},
		{
			name: "does not repeat same warning",
			previous: &metav1.Condition{
				Type:   codeInterpreterWarmPoolCondition,
				Status: metav1.ConditionFalse,
				Reason: codeInterpreterWarmPoolEmpty,
			},
			current: metav1.Condition{
				Type:   codeInterpreterWarmPoolCondition,
				Status: metav1.ConditionFalse,
				Reason: codeInterpreterWarmPoolEmpty,
			},
			want: false,
		},
		{
			name: "records warning when reason changes",
			previous: &metav1.Condition{
				Type:   codeInterpreterWarmPoolCondition,
				Status: metav1.ConditionFalse,
				Reason: codeInterpreterWarmPoolBelowWatermark,
			},
			current: metav1.Condition{
				Type:   codeInterpreterWarmPoolCondition,
				Status: metav1.ConditionFalse,
				Reason: codeInterpreterWarmPoolEmpty,
			},
			want: true,
		},
		{
			name: "does not record ready condition",
			current: metav1.Condition{
				Type:   codeInterpreterWarmPoolCondition,
				Status: metav1.ConditionTrue,
				Reason: codeInterpreterWarmPoolReady,
			},
			want: false,
		},
		{
			name: "does not record disabled condition",
			current: metav1.Condition{
				Type:   codeInterpreterWarmPoolCondition,
				Status: metav1.ConditionUnknown,
				Reason: codeInterpreterWarmPoolDisabled,
			},
			want: false,
		},
		{
			name: "does not record unrelated false reason",
			current: metav1.Condition{
				Type:   codeInterpreterWarmPoolCondition,
				Status: metav1.ConditionFalse,
				Reason: "OtherReason",
			},
			want: false,
		},
	}

	reconciler := setupTestReconciler()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, reconciler.shouldRecordWarmPoolWarningEvent(tt.previous, tt.current))
		})
	}
}

func TestUpdateStatusSetsWarmPoolAvailableCondition(t *testing.T) {
	reconciler := setupTestReconciler()
	ci := &runtimev1alpha1.CodeInterpreter{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-ci",
			Namespace:  "default",
			Generation: 3,
		},
		Spec: runtimev1alpha1.CodeInterpreterSpec{
			WarmPoolSize: int32Ptr(2),
		},
	}
	assert.NoError(t, reconciler.Create(context.Background(), ci))
	assert.NoError(t, reconciler.Create(context.Background(), testSandboxWarmPool(2, 2)))

	assert.NoError(t, reconciler.updateStatus(context.Background(), ci))

	updated := &runtimev1alpha1.CodeInterpreter{}
	assert.NoError(t, reconciler.Get(context.Background(), clientObjectKey(ci.Namespace, ci.Name), updated))
	assert.True(t, updated.Status.Ready)

	ready := apimeta.FindStatusCondition(updated.Status.Conditions, codeInterpreterReadyCondition)
	if assert.NotNil(t, ready) {
		assert.Equal(t, metav1.ConditionTrue, ready.Status)
	}

	warmPool := apimeta.FindStatusCondition(updated.Status.Conditions, codeInterpreterWarmPoolCondition)
	if assert.NotNil(t, warmPool) {
		assert.Equal(t, metav1.ConditionTrue, warmPool.Status)
		assert.Equal(t, codeInterpreterWarmPoolReady, warmPool.Reason)
		assert.Equal(t, ci.Generation, warmPool.ObservedGeneration)
	}
}

func TestUpdateStatusSkipsUnchangedStatus(t *testing.T) {
	ctx := context.Background()
	reconciler := setupTestReconciler()
	ci := &runtimev1alpha1.CodeInterpreter{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-ci",
			Namespace:  "default",
			Generation: 3,
		},
		Spec: runtimev1alpha1.CodeInterpreterSpec{
			WarmPoolSize: int32Ptr(2),
		},
		Status: runtimev1alpha1.CodeInterpreterStatus{
			Ready: true,
			Conditions: []metav1.Condition{
				{
					Type:               codeInterpreterReadyCondition,
					Status:             metav1.ConditionTrue,
					Reason:             codeInterpreterReadyReason,
					Message:            "CodeInterpreter is ready",
					ObservedGeneration: 3,
				},
				{
					Type:               codeInterpreterWarmPoolCondition,
					Status:             metav1.ConditionTrue,
					Reason:             codeInterpreterWarmPoolReady,
					Message:            "SandboxWarmPool has 2 ready replicas out of 2 desired",
					ObservedGeneration: 3,
				},
			},
		},
	}
	assert.NoError(t, reconciler.Create(ctx, ci))
	assert.NoError(t, reconciler.Create(ctx, testSandboxWarmPool(2, 2)))

	assert.NoError(t, reconciler.updateStatus(ctx, ci))
}

func TestRecordEventSkipsNilRecorder(t *testing.T) {
	reconciler := setupTestReconciler()
	ci := testCodeInterpreterWithWarmPool(1)

	assert.NotPanics(t, func() {
		reconciler.recordEvent(ci, corev1.EventTypeWarning, codeInterpreterWarmPoolEmpty, "warm pool empty")
	})
}

func TestUpdateStatusReturnsStatusUpdateError(t *testing.T) {
	ctx := context.Background()
	reconciler := setupTestReconcilerWithInterceptors(interceptor.Funcs{
		SubResourceUpdate: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
			if subResourceName == "status" {
				return fmt.Errorf("status update failed")
			}
			return c.SubResource(subResourceName).Update(ctx, obj, opts...)
		},
	})
	ci := &runtimev1alpha1.CodeInterpreter{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ci",
			Namespace: "default",
		},
	}
	assert.NoError(t, reconciler.Create(ctx, ci))

	err := reconciler.updateStatus(ctx, ci)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "status update failed")
}

func TestReconcileReportsWarmPoolEmptyInsteadOfOnlyReady(t *testing.T) {
	ctx := context.Background()
	reconciler, recorder := setupTestReconcilerWithRecorder(10)
	ci := testCodeInterpreterWithWarmPool(2)
	assert.NoError(t, reconciler.Create(ctx, ci))

	_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: clientObjectKey(ci.Namespace, ci.Name)})
	assert.NoError(t, err)

	updated := &runtimev1alpha1.CodeInterpreter{}
	assert.NoError(t, reconciler.Get(ctx, clientObjectKey(ci.Namespace, ci.Name), updated))
	assert.True(t, updated.Status.Ready)

	ready := apimeta.FindStatusCondition(updated.Status.Conditions, codeInterpreterReadyCondition)
	if assert.NotNil(t, ready) {
		assert.Equal(t, metav1.ConditionTrue, ready.Status)
	}

	warmPool := apimeta.FindStatusCondition(updated.Status.Conditions, codeInterpreterWarmPoolCondition)
	if assert.NotNil(t, warmPool) {
		assert.Equal(t, metav1.ConditionFalse, warmPool.Status)
		assert.Equal(t, codeInterpreterWarmPoolEmpty, warmPool.Reason)
		assert.Contains(t, warmPool.Message, "0 ready replicas out of 2 desired")
	}

	assertEventContains(t, recorder, corev1.EventTypeWarning, codeInterpreterWarmPoolEmpty)
}

func TestReconcileReportsWarmPoolBelowWatermark(t *testing.T) {
	ctx := context.Background()
	reconciler, recorder := setupTestReconcilerWithRecorder(10)
	ci := testCodeInterpreterWithWarmPool(4)
	assert.NoError(t, reconciler.Create(ctx, ci))
	assert.NoError(t, reconciler.Create(ctx, testSandboxWarmPool(4, 1)))

	_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: clientObjectKey(ci.Namespace, ci.Name)})
	assert.NoError(t, err)

	updated := &runtimev1alpha1.CodeInterpreter{}
	assert.NoError(t, reconciler.Get(ctx, clientObjectKey(ci.Namespace, ci.Name), updated))
	warmPool := apimeta.FindStatusCondition(updated.Status.Conditions, codeInterpreterWarmPoolCondition)
	if assert.NotNil(t, warmPool) {
		assert.Equal(t, metav1.ConditionFalse, warmPool.Status)
		assert.Equal(t, codeInterpreterWarmPoolBelowWatermark, warmPool.Reason)
		assert.Contains(t, warmPool.Message, "below low watermark 2")
	}

	assertEventContains(t, recorder, corev1.EventTypeWarning, codeInterpreterWarmPoolBelowWatermark)
}

func TestReconcileUpdatesWarmPoolAvailableWhenPoolRecovers(t *testing.T) {
	ctx := context.Background()
	reconciler, recorder := setupTestReconcilerWithRecorder(10)
	ci := testCodeInterpreterWithWarmPool(2)
	assert.NoError(t, reconciler.Create(ctx, ci))

	request := ctrl.Request{NamespacedName: clientObjectKey(ci.Namespace, ci.Name)}
	_, err := reconciler.Reconcile(ctx, request)
	assert.NoError(t, err)
	assertEventContains(t, recorder, corev1.EventTypeWarning, codeInterpreterWarmPoolEmpty)

	warmPool := &extensionsv1alpha1.SandboxWarmPool{}
	assert.NoError(t, reconciler.Get(ctx, clientObjectKey(ci.Namespace, ci.Name), warmPool))
	warmPool.Status.Replicas = 2
	warmPool.Status.ReadyReplicas = 2
	assert.NoError(t, reconciler.Status().Update(ctx, warmPool))

	_, err = reconciler.Reconcile(ctx, request)
	assert.NoError(t, err)

	updated := &runtimev1alpha1.CodeInterpreter{}
	assert.NoError(t, reconciler.Get(ctx, clientObjectKey(ci.Namespace, ci.Name), updated))
	condition := apimeta.FindStatusCondition(updated.Status.Conditions, codeInterpreterWarmPoolCondition)
	if assert.NotNil(t, condition) {
		assert.Equal(t, metav1.ConditionTrue, condition.Status)
		assert.Equal(t, codeInterpreterWarmPoolReady, condition.Reason)
		assert.Contains(t, condition.Message, "2 ready replicas out of 2 desired")
	}
	assertNoEvent(t, recorder)
}

func TestReconcileDeletesWarmPoolWhenDisabled(t *testing.T) {
	ctx := context.Background()
	reconciler := setupTestReconciler()
	ci := testCodeInterpreterWithWarmPool(2)
	assert.NoError(t, reconciler.Create(ctx, ci))

	request := ctrl.Request{NamespacedName: clientObjectKey(ci.Namespace, ci.Name)}
	_, err := reconciler.Reconcile(ctx, request)
	assert.NoError(t, err)

	warmPool := &extensionsv1alpha1.SandboxWarmPool{}
	assert.NoError(t, reconciler.Get(ctx, clientObjectKey(ci.Namespace, ci.Name), warmPool))

	updated := &runtimev1alpha1.CodeInterpreter{}
	assert.NoError(t, reconciler.Get(ctx, clientObjectKey(ci.Namespace, ci.Name), updated))
	updated.Spec.WarmPoolSize = nil
	assert.NoError(t, reconciler.Update(ctx, updated))

	_, err = reconciler.Reconcile(ctx, request)
	assert.NoError(t, err)
	assert.Error(t, reconciler.Get(ctx, clientObjectKey(ci.Namespace, ci.Name), warmPool))
}

func testCodeInterpreterWithWarmPool(size int32) *runtimev1alpha1.CodeInterpreter {
	return &runtimev1alpha1.CodeInterpreter{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-ci",
			Namespace:  "default",
			Generation: 1,
		},
		Spec: runtimev1alpha1.CodeInterpreterSpec{
			WarmPoolSize: int32Ptr(size),
			AuthMode:     runtimev1alpha1.AuthModeNone,
			Template: &runtimev1alpha1.CodeInterpreterSandboxTemplate{
				Image:           "test-image:latest",
				ImagePullPolicy: corev1.PullIfNotPresent,
			},
		},
	}
}

func testSandboxWarmPool(desired, ready int32) *extensionsv1alpha1.SandboxWarmPool {
	return &extensionsv1alpha1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ci",
			Namespace: "default",
		},
		Spec: extensionsv1alpha1.SandboxWarmPoolSpec{
			Replicas: desired,
			TemplateRef: extensionsv1alpha1.SandboxTemplateRef{
				Name: "test-ci",
			},
		},
		Status: extensionsv1alpha1.SandboxWarmPoolStatus{
			Replicas:      desired,
			ReadyReplicas: ready,
		},
	}
}

func clientObjectKey(namespace, name string) client.ObjectKey {
	return client.ObjectKey{Namespace: namespace, Name: name}
}

func assertEventContains(t *testing.T, recorder *record.FakeRecorder, eventType, reason string) {
	t.Helper()
	select {
	case event := <-recorder.Events:
		assert.Contains(t, event, eventType)
		assert.Contains(t, event, reason)
	default:
		t.Fatalf("expected %s event with reason %s", eventType, reason)
	}
}

func assertNoEvent(t *testing.T, recorder *record.FakeRecorder) {
	t.Helper()
	select {
	case event := <-recorder.Events:
		t.Fatalf("expected no event, got %q", event)
	default:
	}
}
