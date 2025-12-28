package agentd

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/volcano-sh/agentcube/pkg/workloadmanager"
)

func TestReconciler_Reconcile_WithLastActivity(t *testing.T) {
	testScheme := runtime.NewScheme()
	utilruntime.Must(scheme.AddToScheme(testScheme))
	utilruntime.Must(sandboxv1alpha1.AddToScheme(testScheme))

	now := time.Now()

	tests := []struct {
		name              string
		sandbox           *sandboxv1alpha1.Sandbox
		expectDeletion    bool
		expectRequeue     bool
		expectedRequeueAt time.Duration
	}{
		{
			name: "Sandbox with recent activity should not be deleted",
			sandbox: &sandboxv1alpha1.Sandbox{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-sandbox",
					Namespace: "default",
					Annotations: map[string]string{
						"last-activity-time": now.Add(-5 * time.Minute).Format(time.RFC3339),
					},
				},
				Status: sandboxv1alpha1.SandboxStatus{
					Conditions: []metav1.Condition{
						{
							Type:   string(sandboxv1alpha1.SandboxConditionReady),
							Status: metav1.ConditionTrue,
						},
					},
				},
			},
			expectDeletion:    false,
			expectRequeue:     true,
			expectedRequeueAt: 10 * time.Minute,
		},
		{
			name: "Sandbox with expired activity should be deleted",
			sandbox: &sandboxv1alpha1.Sandbox{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-sandbox-expired",
					Namespace: "default",
					Annotations: map[string]string{
						"last-activity-time": now.Add(-20 * time.Minute).Format(time.RFC3339),
					},
				},
				Status: sandboxv1alpha1.SandboxStatus{
					Conditions: []metav1.Condition{
						{
							Type:   string(sandboxv1alpha1.SandboxConditionReady),
							Status: metav1.ConditionTrue,
						},
					},
				},
			},
			expectDeletion: true,
			expectRequeue:  false,
		},
		{
			name:           "Non-running sandbox should not be processed",
			sandbox:        nil, // handled by t.Skip inside the test loop
			expectDeletion: false,
			expectRequeue:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.sandbox == nil {
				t.Skip("pending requirement decision – skipped to avoid false negative")
				return
			}
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
				if tt.expectedRequeueAt > 0 {
					assert.InDelta(t, tt.expectedRequeueAt.Seconds(), result.RequeueAfter.Seconds(), 5.0)
				}
			} else {
				assert.Equal(t, time.Duration(0), result.RequeueAfter)
			}

			deletedSandbox := &sandboxv1alpha1.Sandbox{}
			err = fakeClient.Get(context.Background(), types.NamespacedName{
				Name:      tt.sandbox.Name,
				Namespace: tt.sandbox.Namespace,
			}, deletedSandbox)

			if tt.expectDeletion {
				assert.Error(t, err)
				assert.True(t, k8serrors.IsNotFound(err))
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestReconciler_MalformedTimestamp_Requeues30s(t *testing.T) {
	r := newFakeReconciler(
		sandboxWithAnnotation("bad-ts", workloadmanager.LastActivityAnnotationKey, "not-a-rfc3339"),
	)
	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "bad-ts", Namespace: "default"},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parsing")
	assert.Equal(t, 30*time.Second, res.RequeueAfter)
}

func TestReconciler_EmptyAnnotation_Ignored(t *testing.T) {
	r := newFakeReconciler(
		sandboxWithAnnotation("empty", workloadmanager.LastActivityAnnotationKey, ""),
	)
	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "empty", Namespace: "default"},
	})
	assert.NoError(t, err)
	assert.Zero(t, res.RequeueAfter)
}

func TestReconciler_NotFound_ReturnsNil(t *testing.T) {
	r := newFakeReconciler() // no objects
	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "missing", Namespace: "default"},
	})
	assert.NoError(t, err)
	assert.Zero(t, res.RequeueAfter)
}

func TestReconciler_DeleteFails_ReturnsError(t *testing.T) {
	sdb := sandboxWithAnnotation("del-fail", workloadmanager.LastActivityAnnotationKey,
		time.Now().Add(-20*time.Minute).Format(time.RFC3339))

	// fake client that always fails Delete
	cli := &deleteFailingClient{
		Client: fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(sdb).Build(),
	}
	r := &Reconciler{Client: cli, Scheme: newScheme()}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "del-fail", Namespace: "default"},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "fake delete failed")
}

func TestReconciler_SetupWithManager_Ok(t *testing.T) {
	scheme := newScheme()
	mgr, err := manager.New(&rest.Config{}, manager.Options{
		Scheme: scheme,
		Logger: zap.New(zap.UseDevMode(true)),
		NewClient: func(_ *rest.Config, _ client.Options) (client.Client, error) {
			return fake.NewClientBuilder().WithScheme(scheme).Build(), nil
		},
	})
	require.NoError(t, err)

	r := &Reconciler{Scheme: scheme}
	assert.NoError(t, r.SetupWithManager(mgr))
}

func TestReconciler_15MinBoundary(t *testing.T) {
	edge := time.Now().Add(-SessionExpirationTimeout) // exactly 15 min ago
	sdb := sandboxWithAnnotation("edge", workloadmanager.LastActivityAnnotationKey,
		edge.Format(time.RFC3339))

	r := newFakeReconciler(sdb)
	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "edge", Namespace: "default"},
	})
	assert.NoError(t, err)
	assert.Zero(t, res.RequeueAfter) // deletion branch

	err = r.Get(context.Background(), types.NamespacedName{Name: "edge", Namespace: "default"}, &sandboxv1alpha1.Sandbox{})
	assert.True(t, k8serrors.IsNotFound(err))
}

func newFakeReconciler(objs ...client.Object) *Reconciler {
	s := runtime.NewScheme()
	utilruntime.Must(scheme.AddToScheme(s))
	utilruntime.Must(sandboxv1alpha1.AddToScheme(s))
	return &Reconciler{
		Client: fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).Build(),
		Scheme: s,
	}
}

func sandboxWithAnnotation(name, key, value string) *sandboxv1alpha1.Sandbox {
	return &sandboxv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   "default",
			Annotations: map[string]string{key: value},
		},
		Status: sandboxv1alpha1.SandboxStatus{
			Conditions: []metav1.Condition{{
				Type:   string(sandboxv1alpha1.SandboxConditionReady),
				Status: metav1.ConditionTrue,
			}},
		},
	}
}

func newScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(scheme.AddToScheme(s))
	utilruntime.Must(sandboxv1alpha1.AddToScheme(s))
	return s
}

type deleteFailingClient struct {
	client.Client
}

func (d *deleteFailingClient) Delete(_ context.Context, _ client.Object, _ ...client.DeleteOption) error {
	return k8serrors.NewServiceUnavailable("fake delete failed")
}