package agentd

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/scale/scheme"
	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestAgentdReconciler_Reconcile_WithLastActivity(t *testing.T) {
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
						"agentcube.volcano.sh/last-activity": now.Add(-5 * time.Minute).Format(time.RFC3339),
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
					Name:      "test-sandbox",
					Namespace: "default",
					Annotations: map[string]string{
						"agentcube.volcano.sh/last-activity": now.Add(-20 * time.Minute).Format(time.RFC3339),
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
			name: "Non-running sandbox should not be processed",
			sandbox: &sandboxv1alpha1.Sandbox{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-sandbox",
					Namespace: "default",
					Annotations: map[string]string{
						"agentcube.volcano.sh/last-activity": now.Add(-20 * time.Minute).Format(time.RFC3339),
					},
				},
				Status: sandboxv1alpha1.SandboxStatus{
					Conditions: []metav1.Condition{
						{
							Type:   string(sandboxv1alpha1.SandboxConditionReady),
							Status: metav1.ConditionFalse,
						},
					},
				},
			},
			expectDeletion: false,
			expectRequeue:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(tt.sandbox).
				Build()

			reconciler := &AgentdReconciler{
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
				assert.True(t, errors.IsNotFound(err))
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
