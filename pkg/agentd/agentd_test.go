package agentd

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/scale/scheme"
	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/volcano-sh/agentcube/pkg/workloadmanager"
)

func setupTestScheme() *runtime.Scheme {
	testScheme := runtime.NewScheme()
	utilruntime.Must(scheme.AddToScheme(testScheme))
	utilruntime.Must(sandboxv1alpha1.AddToScheme(testScheme))
	return testScheme
}

// TestReconciler_Reconcile_RuntimeRegistration tests runtime registration scenarios
func TestReconciler_Reconcile_RuntimeRegistration(t *testing.T) {
	testScheme := setupTestScheme()

	tests := []struct {
		name           string
		sandbox        *sandboxv1alpha1.Sandbox
		expectRequeue  bool
		expectDeletion bool
	}{
		{
			name: "Newly registered sandbox without last-activity-time should not be deleted",
			sandbox: &sandboxv1alpha1.Sandbox{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "new-sandbox",
					Namespace: "default",
					// No last-activity-time annotation
				},
			},
			expectRequeue:  false,
			expectDeletion: false,
		},
		{
			name: "Sandbox with empty last-activity-time annotation should not be deleted",
			sandbox: &sandboxv1alpha1.Sandbox{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "empty-annotation-sandbox",
					Namespace: "default",
					Annotations: map[string]string{
						workloadmanager.LastActivityAnnotationKey: "",
					},
				},
			},
			expectRequeue:  false,
			expectDeletion: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(tt.sandbox).
				Build()

			reconciler := &Reconciler{
				Client: fakeClient,
				Scheme: testScheme,
			}

			result, err := reconciler.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      tt.sandbox.Name,
					Namespace: tt.sandbox.Namespace,
				},
			})

			assert.NoError(t, err)
			if tt.expectRequeue {
				assert.True(t, result.RequeueAfter > 0)
			} else {
				assert.Equal(t, time.Duration(0), result.RequeueAfter)
			}

			// Verify sandbox still exists
			sandbox := &sandboxv1alpha1.Sandbox{}
			err = fakeClient.Get(context.Background(), types.NamespacedName{
				Name:      tt.sandbox.Name,
				Namespace: tt.sandbox.Namespace,
			}, sandbox)

			if tt.expectDeletion {
				assert.Error(t, err)
				assert.True(t, k8serrors.IsNotFound(err))
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestReconciler_Reconcile_LifecycleOrchestration tests lifecycle orchestration scenarios
func TestReconciler_Reconcile_LifecycleOrchestration(t *testing.T) {
	testScheme := setupTestScheme()
	now := time.Now()

	tests := []struct {
		name              string
		sandbox           *sandboxv1alpha1.Sandbox
		expectDeletion    bool
		expectRequeue     bool
		expectedRequeueAt time.Duration
		description       string
	}{
		{
			name: "Sandbox with recent activity should be requeued",
			sandbox: &sandboxv1alpha1.Sandbox{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "recent-activity-sandbox",
					Namespace: "default",
					Annotations: map[string]string{
						workloadmanager.LastActivityAnnotationKey: now.Add(-5 * time.Minute).Format(time.RFC3339),
					},
				},
			},
			expectDeletion:    false,
			expectRequeue:     true,
			expectedRequeueAt: 10 * time.Minute, // 15 min timeout - 5 min elapsed
			description:       "Should requeue when sandbox has recent activity",
		},
		{
			name: "Sandbox exactly at expiration boundary should be deleted",
			sandbox: &sandboxv1alpha1.Sandbox{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "expired-sandbox",
					Namespace: "default",
					Annotations: map[string]string{
						workloadmanager.LastActivityAnnotationKey: now.Add(-SessionExpirationTimeout).Format(time.RFC3339),
					},
				},
			},
			expectDeletion: true,
			expectRequeue:  false,
			description:    "Should delete when exactly at expiration time",
		},
		{
			name: "Sandbox past expiration should be deleted",
			sandbox: &sandboxv1alpha1.Sandbox{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "past-expired-sandbox",
					Namespace: "default",
					Annotations: map[string]string{
						workloadmanager.LastActivityAnnotationKey: now.Add(-SessionExpirationTimeout - 1*time.Minute).Format(time.RFC3339),
					},
				},
			},
			expectDeletion: true,
			expectRequeue:  false,
			description:    "Should delete when past expiration time",
		},
		{
			name: "Sandbox with activity just before expiration should be requeued",
			sandbox: &sandboxv1alpha1.Sandbox{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "near-expiration-sandbox",
					Namespace: "default",
					Annotations: map[string]string{
						workloadmanager.LastActivityAnnotationKey: now.Add(-SessionExpirationTimeout + 1*time.Minute).Format(time.RFC3339),
					},
				},
			},
			expectDeletion:    false,
			expectRequeue:     true,
			expectedRequeueAt: 1 * time.Minute,
			description:       "Should requeue when close to expiration",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(tt.sandbox).
				Build()

			reconciler := &Reconciler{
				Client: fakeClient,
				Scheme: testScheme,
			}

			result, err := reconciler.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      tt.sandbox.Name,
					Namespace: tt.sandbox.Namespace,
				},
			})

			assert.NoError(t, err, tt.description)

			if tt.expectRequeue {
				assert.True(t, result.RequeueAfter > 0, "Expected requeue but got 0")
				if tt.expectedRequeueAt > 0 {
					// Allow 5 second tolerance for timing
					assert.InDelta(t, tt.expectedRequeueAt.Seconds(), result.RequeueAfter.Seconds(), 5.0,
						"Requeue time should be approximately %v", tt.expectedRequeueAt)
				}
			} else {
				assert.Equal(t, time.Duration(0), result.RequeueAfter, "Should not requeue")
			}

			// Verify deletion status
			sandbox := &sandboxv1alpha1.Sandbox{}
			err = fakeClient.Get(context.Background(), types.NamespacedName{
				Name:      tt.sandbox.Name,
				Namespace: tt.sandbox.Namespace,
			}, sandbox)

			if tt.expectDeletion {
				assert.Error(t, err, "Expected sandbox to be deleted")
				assert.True(t, k8serrors.IsNotFound(err), "Error should be NotFound")
			} else {
				assert.NoError(t, err, "Sandbox should still exist")
			}
		})
	}
}

// TestReconciler_Reconcile_ErrorPaths tests various error scenarios
func TestReconciler_Reconcile_ErrorPaths(t *testing.T) {
	testScheme := setupTestScheme()

	tests := []struct {
		name          string
		setupClient   func() client.Client
		request       reconcile.Request
		expectError   bool
		expectRequeue bool
		errorContains string
		description   string
	}{
		{
			name: "Sandbox not found should return no error",
			setupClient: func() client.Client {
				return fake.NewClientBuilder().
					WithScheme(testScheme).
					Build()
			},
			request: reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      "non-existent",
					Namespace: "default",
				},
			},
			expectError: false,
			description: "NotFound errors should be ignored and return success",
		},
		{
			name: "Invalid time format should return error and requeue",
			setupClient: func() client.Client {
				sandbox := &sandboxv1alpha1.Sandbox{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "invalid-time-sandbox",
						Namespace: "default",
						Annotations: map[string]string{
							workloadmanager.LastActivityAnnotationKey: "invalid-time-format",
						},
					},
				}
				return fake.NewClientBuilder().
					WithScheme(testScheme).
					WithObjects(sandbox).
					Build()
			},
			request: reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      "invalid-time-sandbox",
					Namespace: "default",
				},
			},
			expectError:   true,
			expectRequeue: true,
			description:   "Invalid time format should cause error and requeue",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := tt.setupClient()

			reconciler := &Reconciler{
				Client: fakeClient,
				Scheme: testScheme,
			}

			result, err := reconciler.Reconcile(context.Background(), tt.request)

			if tt.expectError {
				assert.Error(t, err, tt.description)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
				if tt.expectRequeue {
					assert.True(t, result.RequeueAfter > 0, "Should requeue on error")
				}
			} else {
				assert.NoError(t, err, tt.description)
			}
		})
	}
}

// TestSessionExpirationTimeout tests the timeout constant
func TestSessionExpirationTimeout(t *testing.T) {
	assert.Equal(t, 15*time.Minute, SessionExpirationTimeout, "SessionExpirationTimeout should be 15 minutes")
}

// TestReconciler_Reconcile_EdgeCases tests edge cases
func TestReconciler_Reconcile_EdgeCases(t *testing.T) {
	testScheme := setupTestScheme()
	now := time.Now()

	tests := []struct {
		name           string
		sandbox        *sandboxv1alpha1.Sandbox
		expectRequeue  bool
		expectDeletion bool
		description    string
	}{
		{
			name: "Sandbox with future last-activity-time should not be deleted",
			sandbox: &sandboxv1alpha1.Sandbox{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "future-time-sandbox",
					Namespace: "default",
					Annotations: map[string]string{
						workloadmanager.LastActivityAnnotationKey: now.Add(1 * time.Hour).Format(time.RFC3339),
					},
				},
			},
			expectRequeue:  true,
			expectDeletion: false,
			description:    "Future timestamps should be handled correctly",
		},
		{
			name: "Sandbox with multiple annotations should work correctly",
			sandbox: &sandboxv1alpha1.Sandbox{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "multi-annotation-sandbox",
					Namespace: "default",
					Annotations: map[string]string{
						workloadmanager.LastActivityAnnotationKey: now.Add(-5 * time.Minute).Format(time.RFC3339),
						"other-annotation":                        "other-value",
					},
				},
			},
			expectRequeue:  true,
			expectDeletion: false,
			description:    "Multiple annotations should not interfere",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(tt.sandbox).
				Build()

			reconciler := &Reconciler{
				Client: fakeClient,
				Scheme: testScheme,
			}

			result, err := reconciler.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      tt.sandbox.Name,
					Namespace: tt.sandbox.Namespace,
				},
			})

			assert.NoError(t, err, tt.description)

			if tt.expectRequeue {
				assert.True(t, result.RequeueAfter > 0, "Expected requeue but got 0")
			} else {
				assert.Equal(t, time.Duration(0), result.RequeueAfter, "Should not requeue")
			}

			// Verify deletion status
			sandbox := &sandboxv1alpha1.Sandbox{}
			err = fakeClient.Get(context.Background(), types.NamespacedName{
				Name:      tt.sandbox.Name,
				Namespace: tt.sandbox.Namespace,
			}, sandbox)

			if tt.expectDeletion {
				assert.Error(t, err, "Expected sandbox to be deleted")
				assert.True(t, k8serrors.IsNotFound(err), "Error should be NotFound")
			} else {
				assert.NoError(t, err, "Sandbox should still exist")
			}
		})
	}
}
