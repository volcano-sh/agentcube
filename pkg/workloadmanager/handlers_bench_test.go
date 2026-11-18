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
	"sync/atomic"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
	extensionsv1alpha1 "sigs.k8s.io/agent-sandbox/extensions/api/v1alpha1"
)

// BenchmarkWaitForClaimSandboxReady measures how long waitForClaimSandboxReady
// takes to notice that a SandboxClaim has been bound to a ready Sandbox. The
// fake API server binds the claim claimBindDelay after the wait starts, as the
// agent-sandbox controller does when it adopts a sandbox from a warm pool. The
// reported ns/op is therefore the wait latency, and gets/op the number of
// claim and sandbox reads issued to the API server.
func BenchmarkWaitForClaimSandboxReady(b *testing.B) {
	for _, claimBindDelay := range []time.Duration{20 * time.Millisecond, 300 * time.Millisecond} {
		b.Run("bind"+claimBindDelay.String(), func(b *testing.B) {
			claim := &extensionsv1alpha1.SandboxClaim{
				ObjectMeta: metav1.ObjectMeta{Name: "ci-claim", Namespace: "ns-1"},
			}
			unboundObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(claim)
			if err != nil {
				b.Fatal(err)
			}
			bound := claim.DeepCopy()
			bound.Status.SandboxStatus.Name = "sandbox-1"
			boundObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(bound)
			if err != nil {
				b.Fatal(err)
			}
			sandboxObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(readySandbox())
			if err != nil {
				b.Fatal(err)
			}

			var gets atomic.Int64
			var start atomic.Int64
			dynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
			dynamicClient.PrependReactor("get", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
				gets.Add(1)
				switch action.GetResource() {
				case SandboxClaimGVR:
					if time.Since(time.Unix(0, start.Load())) < claimBindDelay {
						return true, &unstructured.Unstructured{Object: unboundObj}, nil
					}
					return true, &unstructured.Unstructured{Object: boundObj}, nil
				case SandboxGVR:
					return true, &unstructured.Unstructured{Object: sandboxObj}, nil
				}
				return false, nil, nil
			})

			s := &Server{}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				start.Store(time.Now().UnixNano())
				if _, err := s.waitForClaimSandboxReady(context.Background(), dynamicClient, claim); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(gets.Load())/float64(b.N), "gets/op")
		})
	}
}
